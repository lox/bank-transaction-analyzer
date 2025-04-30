package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/shopspring/decimal"

	"crypto/sha256"
	"encoding/hex"

	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-analyzer/internal/types"
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

	// Create tables if they don't exist
	if err := createTables(db); err != nil {
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	return &DB{
		db:       db,
		logger:   logger,
		timezone: timezone,
	}, nil
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
			-- Transaction details
			type TEXT NOT NULL,
			merchant TEXT NOT NULL,
			location TEXT,
			details_category TEXT,
			description TEXT,
			card_number TEXT,
			-- Foreign amount details
			foreign_amount DECIMAL(15,2),
			foreign_currency TEXT,
			-- Transfer details
			transfer_to_account TEXT,
			transfer_from_account TEXT,
			transfer_reference TEXT
		)
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

	// Parse date from QIF format
	date, dateErr := time.ParseInLocation("02/01/2006", t.Date, d.timezone)
	if dateErr != nil {
		return fmt.Errorf("failed to parse transaction date: %v", dateErr)
	}

	// Insert or replace transaction
	_, err := d.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO transactions (
			id, date, amount, payee,
			type, merchant, location, details_category, description, card_number,
			foreign_amount, foreign_currency,
			transfer_to_account, transfer_from_account, transfer_reference
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		id, date, t.Amount, t.Payee,
		details.Type, details.Merchant, details.Location, details.Category, details.Description, details.CardNumber,
		getForeignAmount(details), getForeignCurrency(details),
		getTransferToAccount(details), getTransferFromAccount(details), getTransferReference(details),
	)
	if err != nil {
		return fmt.Errorf("failed to store transaction: %v", err)
	}

	d.logger.Debug("Stored transaction", "id", id, "date", t.Date, "amount", t.Amount)
	return nil
}

// Get retrieves transaction details from the database
func (d *DB) Get(ctx context.Context, t types.Transaction) (*types.TransactionDetails, error) {
	id := generateTransactionID(t)

	var details types.TransactionDetails
	var date time.Time
	var amount decimal.Decimal
	var foreignAmount sql.NullFloat64
	var foreignCurrency sql.NullString
	var transferToAccount sql.NullString
	var transferFromAccount sql.NullString
	var transferReference sql.NullString

	err := d.db.QueryRowContext(ctx, `
		SELECT date, amount, type, merchant, location, details_category, description, card_number,
			foreign_amount, foreign_currency,
			transfer_to_account, transfer_from_account, transfer_reference
		FROM transactions WHERE id = ?
	`, id).Scan(
		&date, &amount, &details.Type, &details.Merchant, &details.Location, &details.Category, &details.Description, &details.CardNumber,
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
	h.Write([]byte(fmt.Sprintf("%s|%s|%s", t.Payee, t.Amount, t.Date)))
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
		SELECT t.date, t.amount, t.payee,
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
			&date, &amount, &t.Payee,
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
