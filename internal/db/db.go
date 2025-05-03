package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/ncruces"
	_ "github.com/ncruces/go-sqlite3"
	_ "github.com/ncruces/go-sqlite3/driver"

	"github.com/shopspring/decimal"

	"crypto/sha256"
	"encoding/hex"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

// DB represents a SQLite database connection
type DB struct {
	db       *sql.DB
	logger   *log.Logger
	timezone *time.Location
}

// New creates a new database connection
func New(dataDir string, logger *log.Logger, timezone *time.Location) (*DB, error) {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	// Open database connection
	dbPath := filepath.Join(dataDir, "transactions.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %v", err)
	}

	// Enable foreign keys and set date format
	_, err = db.Exec(`
		PRAGMA foreign_keys = ON;
		PRAGMA date_format = 'YYYY-MM-DD';
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to set database pragmas: %v", err)
	}

	d := &DB{
		db:       db,
		logger:   logger,
		timezone: timezone,
	}

	// Create tables if they don't exist
	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	return d, nil
}

// createTables creates the necessary tables in the database
func createTables(db *sql.DB) error {
	// Create transactions table
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS transactions (
			id TEXT PRIMARY KEY,
			date DATE NOT NULL,
			amount DECIMAL(15,2) NOT NULL,
			payee TEXT NOT NULL,
			bank TEXT NOT NULL,
			-- Transaction details
			type TEXT NOT NULL,
			merchant TEXT NOT NULL,
			location TEXT,
			details_category TEXT,
			description TEXT,
			card_number TEXT,
			search_body TEXT,
			-- Foreign amount details
			foreign_amount DECIMAL(15,2),
			foreign_currency TEXT,
			-- Transfer details
			transfer_to_account TEXT,
			transfer_from_account TEXT,
			transfer_reference TEXT
		);

		-- Create virtual table for full-text search
		CREATE VIRTUAL TABLE IF NOT EXISTS transactions_fts USING fts5(
			search_body,
			content='transactions',
			content_rowid='rowid'
		);

		-- Create virtual table for vector embeddings
		CREATE VIRTUAL TABLE IF NOT EXISTS transactions_vec USING vec0(
			embedding FLOAT[768]
		);

		-- Create trigger to keep FTS table in sync
		CREATE TRIGGER IF NOT EXISTS transactions_ai AFTER INSERT ON transactions BEGIN
			INSERT INTO transactions_fts(rowid, search_body) VALUES (new.rowid, new.search_body);
		END;

		CREATE TRIGGER IF NOT EXISTS transactions_ad AFTER DELETE ON transactions BEGIN
			DELETE FROM transactions_fts WHERE rowid = old.rowid;
			DELETE FROM transactions_vec WHERE rowid = old.rowid;
		END;

		CREATE TRIGGER IF NOT EXISTS transactions_au AFTER UPDATE ON transactions BEGIN
			DELETE FROM transactions_fts WHERE rowid = old.rowid;
			DELETE FROM transactions_vec WHERE rowid = old.rowid;
			INSERT INTO transactions_fts(rowid, search_body) VALUES (new.rowid, new.search_body);
		END;

		-- Create a view to help debug FTS issues
		CREATE VIEW IF NOT EXISTS transactions_with_fts AS
		SELECT t.rowid, t.id, t.search_body, fts.rowid as fts_rowid
		FROM transactions t
		LEFT JOIN transactions_fts fts ON t.rowid = fts.rowid;
	`)
	if err != nil {
		return fmt.Errorf("failed to create transactions table: %v", err)
	}

	// Create indexes for faster lookups
	indexes := []string{
		"CREATE INDEX IF NOT EXISTS idx_transactions_payee ON transactions(payee)",
		"CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(date)",
		"CREATE INDEX IF NOT EXISTS idx_transactions_type ON transactions(type)",
		"CREATE INDEX IF NOT EXISTS idx_transactions_merchant ON transactions(merchant)",
		"CREATE INDEX IF NOT EXISTS idx_transactions_category ON transactions(details_category)",
		"CREATE INDEX IF NOT EXISTS idx_transactions_amount ON transactions(amount)",
		"CREATE INDEX IF NOT EXISTS idx_transactions_bank ON transactions(bank)",
	}

	for _, index := range indexes {
		if _, err := db.Exec(index); err != nil {
			return fmt.Errorf("failed to create index: %v", err)
		}
	}

	return nil
}

// Store stores a transaction and its details in the database
func (d *DB) Store(ctx context.Context, t types.Transaction, details *types.TransactionDetails) error {
	// Generate transaction ID
	id := generateTransactionID(t)
	d.logger.Debug("Storing transaction", "id", id, "date", t.Date, "amount", t.Amount, "bank", t.Bank, "payee", t.Payee)

	// Parse date from QIF format
	date, dateErr := time.ParseInLocation("02/01/2006", t.Date, d.timezone)
	if dateErr != nil {
		return fmt.Errorf("failed to parse transaction date: %v", dateErr)
	}

	// Insert or replace transaction
	result, err := d.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO transactions (
			id, date, amount, payee, bank,
			type, merchant, location, details_category, description, card_number, search_body,
			foreign_amount, foreign_currency,
			transfer_to_account, transfer_from_account, transfer_reference
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		id, date, t.Amount, t.Payee, t.Bank,
		details.Type, details.Merchant, details.Location, details.Category, details.Description, details.CardNumber, details.SearchBody,
		getForeignAmount(details), getForeignCurrency(details),
		getTransferToAccount(details), getTransferFromAccount(details), getTransferReference(details),
	)
	if err != nil {
		return fmt.Errorf("failed to store transaction: %v", err)
	}

	// Log the result of the insert/replace
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		d.logger.Warn("Failed to get rows affected", "error", err)
	} else {
		d.logger.Debug("Transaction stored", "id", id, "rows_affected", rowsAffected)
	}

	// Try to get the rowid after insert
	var rowid int64
	err = d.db.QueryRowContext(ctx, "SELECT rowid FROM transactions WHERE id = ?", id).Scan(&rowid)
	if err != nil {
		d.logger.Error("Failed to get rowid after insert", "id", id, "error", err)
		return fmt.Errorf("failed to get transaction rowid: %v", err)
	}
	d.logger.Debug("Got rowid after insert", "id", id, "rowid", rowid)

	// Check FTS status
	var ftsRowid sql.NullInt64
	err = d.db.QueryRowContext(ctx, "SELECT fts_rowid FROM transactions_with_fts WHERE id = ?", id).Scan(&ftsRowid)
	if err != nil {
		d.logger.Warn("Failed to check FTS status", "id", id, "error", err)
	} else {
		d.logger.Debug("FTS status", "id", id, "fts_rowid", ftsRowid)
	}

	return nil
}

// Get retrieves transaction details from the database
func (d *DB) Get(ctx context.Context, t types.Transaction) (*types.TransactionDetails, error) {
	id := generateTransactionID(t)

	var details types.TransactionDetails
	var date time.Time
	var amount decimal.Decimal
	var bank string
	var foreignAmount sql.NullFloat64
	var foreignCurrency sql.NullString
	var transferToAccount sql.NullString
	var transferFromAccount sql.NullString
	var transferReference sql.NullString

	err := d.db.QueryRowContext(ctx, `
		SELECT date, amount, bank, type, merchant, location, details_category, description, card_number, search_body,
			foreign_amount, foreign_currency,
			transfer_to_account, transfer_from_account, transfer_reference
		FROM transactions WHERE id = ?
	`, id).Scan(
		&date, &amount, &bank, &details.Type, &details.Merchant, &details.Location, &details.Category, &details.Description, &details.CardNumber, &details.SearchBody,
		&foreignAmount, &foreignCurrency,
		&transferToAccount, &transferFromAccount, &transferReference,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get transaction: %v", err)
	}

	// Set foreign amount if present
	if foreignAmount.Valid && foreignCurrency.Valid {
		details.ForeignAmount = &struct {
			Amount   decimal.Decimal `json:"amount"`
			Currency string          `json:"currency"`
		}{
			Amount:   decimal.NewFromFloat(foreignAmount.Float64),
			Currency: foreignCurrency.String,
		}
	}

	// Set transfer details if present
	if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
		details.TransferDetails = &struct {
			ToAccount   string `json:"to_account,omitempty"`
			FromAccount string `json:"from_account,omitempty"`
			Reference   string `json:"reference,omitempty"`
		}{
			ToAccount:   transferToAccount.String,
			FromAccount: transferFromAccount.String,
			Reference:   transferReference.String,
		}
	}

	return &details, nil
}

// generateTransactionID creates a unique ID for a transaction based on payee, amount, and date
func generateTransactionID(t types.Transaction) string {
	// Create a hash of the transaction details
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("%s|%s|%s|%s", t.Payee, t.Amount, t.Date, t.Bank)))
	return hex.EncodeToString(h.Sum(nil))[:8]
}

// Helper functions to safely extract values from transaction details
func getForeignAmount(details *types.TransactionDetails) sql.NullFloat64 {
	if details.ForeignAmount != nil {
		amount, _ := details.ForeignAmount.Amount.Float64()
		return sql.NullFloat64{Float64: amount, Valid: true}
	}
	return sql.NullFloat64{}
}

func getForeignCurrency(details *types.TransactionDetails) sql.NullString {
	if details.ForeignAmount != nil {
		return sql.NullString{String: details.ForeignAmount.Currency, Valid: true}
	}
	return sql.NullString{}
}

func getTransferToAccount(details *types.TransactionDetails) sql.NullString {
	if details.TransferDetails != nil {
		return sql.NullString{String: details.TransferDetails.ToAccount, Valid: true}
	}
	return sql.NullString{}
}

func getTransferFromAccount(details *types.TransactionDetails) sql.NullString {
	if details.TransferDetails != nil {
		return sql.NullString{String: details.TransferDetails.FromAccount, Valid: true}
	}
	return sql.NullString{}
}

func getTransferReference(details *types.TransactionDetails) sql.NullString {
	if details.TransferDetails != nil {
		return sql.NullString{String: details.TransferDetails.Reference, Valid: true}
	}
	return sql.NullString{}
}

// FilterExistingTransactions filters out transactions that already exist in the database
func (d *DB) FilterExistingTransactions(ctx context.Context, transactions []types.Transaction) ([]types.Transaction, error) {
	var filtered []types.Transaction

	for _, t := range transactions {
		exists, err := d.Has(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("failed to check transaction existence: %v", err)
		}
		if !exists {
			filtered = append(filtered, t)
		}
	}

	return filtered, nil
}

// Has checks if a transaction exists in the database
func (d *DB) Has(ctx context.Context, t types.Transaction) (bool, error) {
	id := generateTransactionID(t)

	var exists bool
	err := d.db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM transactions WHERE id = ?)
	`, id).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("failed to check transaction existence: %v", err)
	}

	return exists, nil
}

// Count returns the number of transactions in the database
func (d *DB) Count() (int, error) {
	var count int
	err := d.db.QueryRow(`
		SELECT COUNT(*) FROM transactions
	`).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count transactions: %v", err)
	}

	return count, nil
}

// Close closes the database connection
func (d *DB) Close() error {
	return d.db.Close()
}

// GetTransactions returns transactions with details from the last N days
func (d *DB) GetTransactions(ctx context.Context, days int) ([]types.TransactionWithDetails, error) {
	query := `
		SELECT t.date, t.amount, t.payee, t.bank,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference
		FROM transactions t
		WHERE t.date >= date('now', ? || ' days')
		ORDER BY t.date DESC
	`

	rows, err := d.db.QueryContext(ctx, query, -days)
	if err != nil {
		return nil, fmt.Errorf("failed to query transactions: %w", err)
	}
	defer rows.Close()

	var transactions []types.TransactionWithDetails
	for rows.Next() {
		var t types.TransactionWithDetails
		var date time.Time
		var amount decimal.Decimal
		var foreignAmount sql.NullFloat64
		var foreignCurrency sql.NullString
		var transferToAccount sql.NullString
		var transferFromAccount sql.NullString
		var transferReference sql.NullString

		if err := rows.Scan(
			&date, &amount, &t.Payee, &t.Bank,
			&t.Details.Type, &t.Details.Merchant, &t.Details.Location, &t.Details.Category, &t.Details.Description, &t.Details.CardNumber,
			&foreignAmount, &foreignCurrency,
			&transferToAccount, &transferFromAccount, &transferReference,
		); err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}

		// Format date and amount as strings
		t.Date = date.Format("02/01/2006")
		t.Amount = amount.String()

		// Set foreign amount if present
		if foreignAmount.Valid && foreignCurrency.Valid {
			t.Details.ForeignAmount = &struct {
				Amount   decimal.Decimal `json:"amount"`
				Currency string          `json:"currency"`
			}{
				Amount:   decimal.NewFromFloat(foreignAmount.Float64),
				Currency: foreignCurrency.String,
			}
		}

		// Set transfer details if present
		if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
			t.Details.TransferDetails = &struct {
				ToAccount   string `json:"to_account,omitempty"`
				FromAccount string `json:"from_account,omitempty"`
				Reference   string `json:"reference,omitempty"`
			}{
				ToAccount:   transferToAccount.String,
				FromAccount: transferFromAccount.String,
				Reference:   transferReference.String,
			}
		}

		transactions = append(transactions, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transactions: %w", err)
	}

	return transactions, nil
}

// SearchTransactions searches for transactions using full-text search
func (d *DB) SearchTransactions(ctx context.Context, query string, days int) ([]types.TransactionWithDetails, error) {
	searchQuery := `
		SELECT t.date, t.amount, t.payee, t.bank,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference
		FROM transactions t
		JOIN transactions_fts fts ON t.rowid = fts.rowid
		WHERE fts.search_body MATCH ?
		AND t.date >= date('now', ? || ' days')
		ORDER BY t.date DESC
	`

	rows, err := d.db.QueryContext(ctx, searchQuery, query, -days)
	if err != nil {
		return nil, fmt.Errorf("failed to search transactions: %w", err)
	}
	defer rows.Close()

	var transactions []types.TransactionWithDetails
	for rows.Next() {
		var t types.TransactionWithDetails
		var date time.Time
		var amount decimal.Decimal
		var foreignAmount sql.NullFloat64
		var foreignCurrency sql.NullString
		var transferToAccount sql.NullString
		var transferFromAccount sql.NullString
		var transferReference sql.NullString

		if err := rows.Scan(
			&date, &amount, &t.Payee, &t.Bank,
			&t.Details.Type, &t.Details.Merchant, &t.Details.Location, &t.Details.Category, &t.Details.Description, &t.Details.CardNumber,
			&foreignAmount, &foreignCurrency,
			&transferToAccount, &transferFromAccount, &transferReference,
		); err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}

		// Format date and amount as strings
		t.Date = date.Format("02/01/2006")
		t.Amount = amount.String()

		// Set foreign amount if present
		if foreignAmount.Valid && foreignCurrency.Valid {
			t.Details.ForeignAmount = &struct {
				Amount   decimal.Decimal `json:"amount"`
				Currency string          `json:"currency"`
			}{
				Amount:   decimal.NewFromFloat(foreignAmount.Float64),
				Currency: foreignCurrency.String,
			}
		}

		// Set transfer details if present
		if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
			t.Details.TransferDetails = &struct {
				ToAccount   string `json:"to_account,omitempty"`
				FromAccount string `json:"from_account,omitempty"`
				Reference   string `json:"reference,omitempty"`
			}{
				ToAccount:   transferToAccount.String,
				FromAccount: transferFromAccount.String,
				Reference:   transferReference.String,
			}
		}

		transactions = append(transactions, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transactions: %w", err)
	}

	return transactions, nil
}

// SearchTransactionsByVector searches for transactions using vector similarity
func (d *DB) SearchTransactionsByVector(ctx context.Context, embedding []byte, days int, limit int) ([]types.TransactionWithDetails, error) {
	query := `
		WITH vec_matches AS (
			SELECT
				rowid as transaction_id,
				row_number() OVER (ORDER BY distance) as rank_number,
				distance
			FROM transactions_vec
			WHERE embedding MATCH ? AND k = ?
			AND rowid IN (
				SELECT rowid FROM transactions
				WHERE date >= date('now', ? || ' days')
			)
		)
		SELECT
			t.date, t.amount, t.payee,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference,
			vm.distance
		FROM vec_matches vm
		JOIN transactions t ON t.rowid = vm.transaction_id
		ORDER BY vm.rank_number ASC
	`

	rows, err := d.db.QueryContext(ctx, query, embedding, limit, -days)
	if err != nil {
		return nil, fmt.Errorf("vector search failed: %w", err)
	}
	defer rows.Close()

	var transactions []types.TransactionWithDetails
	for rows.Next() {
		var t types.TransactionWithDetails
		var date time.Time
		var amount decimal.Decimal
		var foreignAmount sql.NullFloat64
		var foreignCurrency sql.NullString
		var transferToAccount sql.NullString
		var transferFromAccount sql.NullString
		var transferReference sql.NullString
		var distance float64

		if err := rows.Scan(
			&date, &amount, &t.Payee,
			&t.Details.Type, &t.Details.Merchant, &t.Details.Location, &t.Details.Category, &t.Details.Description, &t.Details.CardNumber,
			&foreignAmount, &foreignCurrency,
			&transferToAccount, &transferFromAccount, &transferReference,
			&distance,
		); err != nil {
			return nil, fmt.Errorf("failed to scan transaction: %w", err)
		}

		// Format date and amount as strings
		t.Date = date.Format("02/01/2006")
		t.Amount = amount.String()

		// Set foreign amount if present
		if foreignAmount.Valid && foreignCurrency.Valid {
			t.Details.ForeignAmount = &struct {
				Amount   decimal.Decimal `json:"amount"`
				Currency string          `json:"currency"`
			}{
				Amount:   decimal.NewFromFloat(foreignAmount.Float64),
				Currency: foreignCurrency.String,
			}
		}

		// Set transfer details if present
		if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
			t.Details.TransferDetails = &struct {
				ToAccount   string `json:"to_account,omitempty"`
				FromAccount string `json:"from_account,omitempty"`
				Reference   string `json:"reference,omitempty"`
			}{
				ToAccount:   transferToAccount.String,
				FromAccount: transferFromAccount.String,
				Reference:   transferReference.String,
			}
		}

		transactions = append(transactions, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transactions: %w", err)
	}

	return transactions, nil
}

// SearchTransactionsHybrid combines vector and full-text search results using Reciprocal Rank Fusion
func (d *DB) SearchTransactionsHybrid(ctx context.Context, query string, embedding []byte, days int, limit int) ([]types.TransactionSearchResult, error) {
	// Constants for RRF algorithm
	const (
		rrf_k = 60 // Constant k in RRF formula, typically between 60-80
		// Weights for different search methods - give more weight to vector search
		vectorWeight = 2.0 // Increased weight for vector similarity
		textWeight   = 1.0 // Base weight for text search
	)

	// Run vector search query
	vectorQuery := `
		WITH vec_matches AS (
			SELECT
				t.date, t.amount, t.payee,
				t.type, t.merchant, t.location, t.details_category as category, t.description, t.card_number,
				t.foreign_amount, t.foreign_currency,
				t.transfer_to_account, t.transfer_from_account, t.transfer_reference,
				vm.distance as vector_score,
				row_number() OVER (ORDER BY vm.distance ASC) as vector_rank
			FROM transactions t
			JOIN transactions_vec vm ON t.rowid = vm.rowid
			WHERE vm.embedding MATCH ? AND k = ?
			AND t.date >= date('now', ? || ' days')
			ORDER BY vm.distance ASC
			LIMIT ?
		)
		SELECT * FROM vec_matches
	`

	// Run text search query with more flexible matching
	textQuery := `
		WITH text_matches AS (
			SELECT
				t.date, t.amount, t.payee,
				t.type, t.merchant, t.location, t.details_category as category, t.description, t.card_number,
				t.foreign_amount, t.foreign_currency,
				t.transfer_to_account, t.transfer_from_account, t.transfer_reference,
				rank as text_score,
				row_number() OVER (ORDER BY rank DESC) as text_rank
			FROM transactions t
			JOIN transactions_fts fts ON t.rowid = fts.rowid
			WHERE fts.search_body MATCH ? || '*'
			AND t.date >= date('now', ? || ' days')
			ORDER BY rank DESC
			LIMIT ?
		)
		SELECT * FROM text_matches
	`

	// Execute both queries in parallel using goroutines
	var vectorResults, textResults []types.TransactionSearchResult
	var vectorErr, textErr error

	// Create a wait group for parallel execution
	var wg sync.WaitGroup
	wg.Add(2)

	// Run vector search
	go func() {
		defer wg.Done()
		rows, err := d.db.QueryContext(ctx, vectorQuery, embedding, limit, -days, limit)
		if err != nil {
			vectorErr = fmt.Errorf("vector search failed: %w", err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var result types.TransactionSearchResult
			var t = &result.TransactionWithDetails
			var date time.Time
			var amount decimal.Decimal
			var foreignAmount sql.NullFloat64
			var foreignCurrency sql.NullString
			var transferToAccount sql.NullString
			var transferFromAccount sql.NullString
			var transferReference sql.NullString
			var vectorScore float64
			var vectorRank int

			if err := rows.Scan(
				&date,
				&amount,
				&t.Payee,
				&t.Details.Type,
				&t.Details.Merchant,
				&t.Details.Location,
				&t.Details.Category,
				&t.Details.Description,
				&t.Details.CardNumber,
				&foreignAmount,
				&foreignCurrency,
				&transferToAccount,
				&transferFromAccount,
				&transferReference,
				&vectorScore,
				&vectorRank,
			); err != nil {
				vectorErr = fmt.Errorf("failed to scan vector result: %w", err)
				return
			}

			// Format date and amount
			t.Date = date.Format("02/01/2006")
			t.Amount = amount.String()

			// Set foreign amount if present
			if foreignAmount.Valid && foreignCurrency.Valid {
				t.Details.ForeignAmount = &struct {
					Amount   decimal.Decimal `json:"amount"`
					Currency string          `json:"currency"`
				}{
					Amount:   decimal.NewFromFloat(foreignAmount.Float64),
					Currency: foreignCurrency.String,
				}
			}

			// Set transfer details if present
			if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
				t.Details.TransferDetails = &struct {
					ToAccount   string `json:"to_account,omitempty"`
					FromAccount string `json:"from_account,omitempty"`
					Reference   string `json:"reference,omitempty"`
				}{
					ToAccount:   transferToAccount.String,
					FromAccount: transferFromAccount.String,
					Reference:   transferReference.String,
				}
			}

			// Set vector score
			result.Scores.VectorScore = vectorScore

			vectorResults = append(vectorResults, result)
		}

		if err := rows.Err(); err != nil {
			vectorErr = fmt.Errorf("error iterating vector results: %w", err)
			return
		}
	}()

	// Run text search
	go func() {
		defer wg.Done()
		rows, err := d.db.QueryContext(ctx, textQuery, query, -days, limit)
		if err != nil {
			textErr = fmt.Errorf("text search failed: %w", err)
			return
		}
		defer rows.Close()

		for rows.Next() {
			var result types.TransactionSearchResult
			var t = &result.TransactionWithDetails
			var date time.Time
			var amount decimal.Decimal
			var foreignAmount sql.NullFloat64
			var foreignCurrency sql.NullString
			var transferToAccount sql.NullString
			var transferFromAccount sql.NullString
			var transferReference sql.NullString
			var textScore float64
			var textRank int

			if err := rows.Scan(
				&result.Transaction.Date,
				&amount,
				&t.Payee,
				&t.Details.Type,
				&t.Details.Merchant,
				&t.Details.Location,
				&t.Details.Category,
				&t.Details.Description,
				&t.Details.CardNumber,
				&foreignAmount,
				&foreignCurrency,
				&transferToAccount,
				&transferFromAccount,
				&transferReference,
				&textScore,
				&textRank,
			); err != nil {
				textErr = fmt.Errorf("failed to scan text result: %w", err)
				return
			}

			// Format date and amount
			t.Date = date.Format("02/01/2006")
			t.Amount = amount.String()

			// Set foreign amount if present
			if foreignAmount.Valid && foreignCurrency.Valid {
				t.Details.ForeignAmount = &struct {
					Amount   decimal.Decimal `json:"amount"`
					Currency string          `json:"currency"`
				}{
					Amount:   decimal.NewFromFloat(foreignAmount.Float64),
					Currency: foreignCurrency.String,
				}
			}

			// Set transfer details if present
			if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
				t.Details.TransferDetails = &struct {
					ToAccount   string `json:"to_account,omitempty"`
					FromAccount string `json:"from_account,omitempty"`
					Reference   string `json:"reference,omitempty"`
				}{
					ToAccount:   transferToAccount.String,
					FromAccount: transferFromAccount.String,
					Reference:   transferReference.String,
				}
			}

			// Set text score
			result.Scores.TextScore = textScore

			textResults = append(textResults, result)
		}

		if err := rows.Err(); err != nil {
			textErr = fmt.Errorf("error iterating text results: %w", err)
			return
		}
	}()

	// Wait for both searches to complete
	wg.Wait()

	// Check for errors
	if vectorErr != nil {
		return nil, vectorErr
	}
	if textErr != nil {
		return nil, textErr
	}

	// Combine results using RRF
	transactionScores := make(map[string]*types.TransactionSearchResult)

	// Process vector results
	for _, result := range vectorResults {
		id := generateTransactionID(result.Transaction)
		if _, exists := transactionScores[id]; !exists {
			transactionScores[id] = &result
		}
	}

	// Process text results
	for _, result := range textResults {
		id := generateTransactionID(result.Transaction)
		if existing, exists := transactionScores[id]; exists {
			// Merge scores
			existing.Scores.TextScore = result.Scores.TextScore
		} else {
			transactionScores[id] = &result
		}
	}

	// Calculate RRF scores
	var combinedResults []types.TransactionSearchResult
	for _, result := range transactionScores {
		// Calculate RRF score using both vector and text scores
		vectorRRF := vectorWeight / (float64(rrf_k) + result.Scores.VectorScore)
		textRRF := textWeight / (float64(rrf_k) + result.Scores.TextScore)

		// Combine scores with adjusted weights
		result.Scores.RRFScore = vectorRRF + textRRF
		combinedResults = append(combinedResults, *result)
	}

	// Sort by RRF score
	sort.Slice(combinedResults, func(i, j int) bool {
		return combinedResults[i].Scores.RRFScore > combinedResults[j].Scores.RRFScore
	})

	// Limit results
	if len(combinedResults) > limit {
		combinedResults = combinedResults[:limit]
	}

	return combinedResults, nil
}

// SerializeEmbedding serializes a float32 vector for sqlite-vec
func (d *DB) SerializeEmbedding(embedding []float32) ([]byte, error) {
	serialized, err := sqlite_vec.SerializeFloat32(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize embedding: %w", err)
	}
	return serialized, nil
}

// DB returns the underlying database connection
func (d *DB) DB() *sql.DB {
	return d.db
}

// CategoryCount represents a category and its transaction count
type CategoryCount struct {
	Category string
	Count    int
}

// GetCategories returns all unique categories and their counts from the last N days
func (d *DB) GetCategories(ctx context.Context, days int) ([]CategoryCount, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT
			details_category as category,
			COUNT(*) as count
		FROM transactions
		WHERE date >= date('now', ? || ' days')
		AND details_category IS NOT NULL
		AND details_category != ''
		GROUP BY details_category
		ORDER BY count DESC, category ASC
	`, -days)
	if err != nil {
		return nil, fmt.Errorf("failed to query categories: %w", err)
	}
	defer rows.Close()

	var categories []CategoryCount
	for rows.Next() {
		var cat CategoryCount
		if err := rows.Scan(&cat.Category, &cat.Count); err != nil {
			return nil, fmt.Errorf("failed to scan category row: %w", err)
		}
		categories = append(categories, cat)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating categories: %w", err)
	}

	return categories, nil
}
