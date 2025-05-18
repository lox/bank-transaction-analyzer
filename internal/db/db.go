package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/shopspring/decimal"

	"crypto/sha256"
	"encoding/hex"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

const (
	OrderByRelevance = "relevance"
	OrderByDate      = "date"
)

// Schema defines the database schema
var schema = `
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
	transfer_reference TEXT,
	-- Tags (comma-separated)
	tags TEXT
);

-- Create virtual table for full-text search
CREATE VIRTUAL TABLE IF NOT EXISTS transactions_fts USING fts5(
	search_body,
	content='transactions',
	content_rowid='rowid'
);

-- Create trigger to keep FTS table in sync
CREATE TRIGGER IF NOT EXISTS transactions_ai AFTER INSERT ON transactions BEGIN
	INSERT INTO transactions_fts(rowid, search_body) VALUES (new.rowid, new.search_body);
END;

CREATE TRIGGER IF NOT EXISTS transactions_ad AFTER DELETE ON transactions BEGIN
	DELETE FROM transactions_fts WHERE rowid = old.rowid;
END;

CREATE TRIGGER IF NOT EXISTS transactions_au AFTER UPDATE ON transactions BEGIN
	DELETE FROM transactions_fts WHERE rowid = old.rowid;
	INSERT INTO transactions_fts(rowid, search_body) VALUES (new.rowid, new.search_body);
END;

-- Create indexes for faster lookups
CREATE INDEX IF NOT EXISTS idx_transactions_payee ON transactions(payee);
CREATE INDEX IF NOT EXISTS idx_transactions_date ON transactions(date);
CREATE INDEX IF NOT EXISTS idx_transactions_type ON transactions(type);
CREATE INDEX IF NOT EXISTS idx_transactions_merchant ON transactions(merchant);
CREATE INDEX IF NOT EXISTS idx_transactions_category ON transactions(details_category);
CREATE INDEX IF NOT EXISTS idx_transactions_amount ON transactions(amount);
CREATE INDEX IF NOT EXISTS idx_transactions_bank ON transactions(bank);

CREATE TABLE IF NOT EXISTS migrations (
    id INTEGER PRIMARY KEY
);
`

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

	// Initialize database schema and apply migrations
	if err := d.Init(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %v", err)
	}

	return d, nil
}

// Init initializes the database with the schema and applies migrations
func (d *DB) Init(ctx context.Context) error {
	// Check if the database exists by checking for transactions table
	var exists bool
	err := d.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM sqlite_master
			WHERE type='table' AND name='transactions'
		)
	`).Scan(&exists)

	if err != nil {
		return fmt.Errorf("failed to check if database exists: %v", err)
	}

	// Initialize database schema if it doesn't exist
	if !exists {
		d.logger.Info("Creating database schema")
		if _, err := d.db.ExecContext(ctx, schema); err != nil {
			return fmt.Errorf("failed to create database schema: %v", err)
		}
		// Mark all migrations as applied
		for _, m := range migrations {
			_, err := d.db.ExecContext(ctx, `INSERT INTO migrations (id) VALUES (?)`, m.ID)
			if err != nil {
				return fmt.Errorf("failed to mark migration %d as applied: %v", m.ID, err)
			}
		}
	} else {
		// Apply migrations if database already exists
		if err := ApplyMigrations(ctx, d.db, func(msg string, args ...interface{}) {
			d.logger.Infof(msg, args...)
		}); err != nil {
			return fmt.Errorf("failed to apply migrations: %v", err)
		}
	}

	return nil
}

// Store stores a transaction and its details in the database
func (d *DB) Store(ctx context.Context, t types.Transaction, details *types.TransactionDetails) error {
	// Generate transaction ID
	id := GenerateTransactionID(t)
	d.logger.Debug("Storing transaction", "id", id, "date", t.Date, "amount", t.Amount, "bank", t.Bank, "payee", t.Payee)

	// Parse date from QIF format
	date, dateErr := time.ParseInLocation("02/01/2006", t.Date, d.timezone)
	if dateErr != nil {
		return fmt.Errorf("failed to parse transaction date: %v", dateErr)
	}

	// Insert or replace transaction
	_, err := d.db.ExecContext(ctx, `
		INSERT OR REPLACE INTO transactions (
			id, date, amount, payee, bank,
			type, merchant, location, details_category, description, card_number, search_body,
			foreign_amount, foreign_currency,
			transfer_to_account, transfer_from_account, transfer_reference,
			tags
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		id, date, t.Amount, t.Payee, t.Bank,
		details.Type, details.Merchant, details.Location, details.Category, details.Description, details.CardNumber, details.SearchBody,
		getForeignAmount(details), getForeignCurrency(details),
		getTransferToAccount(details), getTransferFromAccount(details), getTransferReference(details),
		details.Tags,
	)
	if err != nil {
		return fmt.Errorf("failed to store transaction: %v", err)
	}

	return nil
}

// Get retrieves transaction details from the database
func (d *DB) Get(ctx context.Context, t types.Transaction) (*types.TransactionDetails, error) {
	id := GenerateTransactionID(t)

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
		details.ForeignAmount = &types.ForeignAmountDetails{
			Amount:   decimal.NewFromFloat(foreignAmount.Float64),
			Currency: foreignCurrency.String,
		}
	}

	// Set transfer details if present
	if transferToAccount.Valid || transferFromAccount.Valid || transferReference.Valid {
		details.TransferDetails = &types.TransferDetails{
			ToAccount:   transferToAccount.String,
			FromAccount: transferFromAccount.String,
			Reference:   transferReference.String,
		}
	}

	return &details, nil
}

// GenerateTransactionID creates a unique ID for a transaction based on payee, amount, and date
func GenerateTransactionID(t types.Transaction) string {
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
	id := GenerateTransactionID(t)

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

// GetTransactionByID retrieves a transaction by its ID
func (d *DB) GetTransactionByID(ctx context.Context, id string) (*types.TransactionWithDetails, error) {
	query := `
		SELECT t.date, t.amount, t.payee, t.bank,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference
		FROM transactions t
		WHERE t.id = ?
	`

	var t types.TransactionWithDetails
	row := d.db.QueryRowContext(ctx, query, id)

	var date time.Time
	var amount decimal.Decimal
	var foreignAmount sql.NullFloat64
	var foreignCurrency sql.NullString
	var transferToAccount sql.NullString
	var transferFromAccount sql.NullString
	var transferReference sql.NullString

	if err := row.Scan(
		&date, &amount, &t.Payee, &t.Bank,
		&t.Details.Type, &t.Details.Merchant, &t.Details.Location, &t.Details.Category, &t.Details.Description, &t.Details.CardNumber,
		&foreignAmount, &foreignCurrency,
		&transferToAccount, &transferFromAccount, &transferReference,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("transaction with ID %s not found", id)
		}
		return nil, fmt.Errorf("failed to scan transaction: %w", err)
	}

	// Format date and amount as strings
	t.Date = date.Format("02/01/2006")
	t.Amount = amount.String()

	// Set foreign amount if present
	SetForeignAmount(&t, foreignAmount, foreignCurrency)

	// Set transfer details if present
	SetTransferDetails(&t, transferToAccount, transferFromAccount, transferReference)

	return &t, nil
}

// TransactionQueryOptions defines options for filtering and paginating transactions
// (no Query field)
type TransactionQueryOptions struct {
	Days         int
	Limit        int
	Offset       int
	Category     string
	Type         string
	Bank         string
	MinAmount    string
	MaxAmount    string
	AbsMinAmount string // For absolute value filtering
	AbsMaxAmount string // For absolute value filtering
}

// TransactionQueryOption is a function that modifies TransactionQueryOptions
type TransactionQueryOption func(*TransactionQueryOptions)

// FilterByDays sets the number of days to look back
func FilterByDays(days int) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.Days = days
	}
}

// FilterByCategory sets the category filter
func FilterByCategory(category string) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.Category = category
	}
}

// FilterByType sets the type filter
func FilterByType(txType string) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.Type = txType
	}
}

// FilterByBank sets the bank filter
func FilterByBank(bank string) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.Bank = bank
	}
}

// FilterByAmount sets both minimum and maximum amount filters
func FilterByAmount(minAmount, maxAmount string) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.MinAmount = minAmount
		opts.MaxAmount = maxAmount
	}
}

// FilterByAbsoluteAmount sets min and max filters using absolute values
func FilterByAbsoluteAmount(minAmount, maxAmount string) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.AbsMinAmount = minAmount
		opts.AbsMaxAmount = maxAmount
	}
}

// WithLimit sets the limit
func WithLimit(limit int) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.Limit = limit
	}
}

// WithOffset sets the offset
func WithOffset(offset int) TransactionQueryOption {
	return func(opts *TransactionQueryOptions) {
		opts.Offset = offset
	}
}

// Helper to add amount filters to where/params
func addAmountFilters(opts TransactionQueryOptions, where []string, params []any) ([]string, []any) {
	if opts.AbsMinAmount != "" && opts.AbsMaxAmount != "" {
		if opts.AbsMinAmount == opts.AbsMaxAmount {
			where = append(where, "(t.amount = ? OR t.amount = -?)")
			params = append(params, opts.AbsMinAmount, opts.AbsMinAmount)
		} else {
			where = append(where, "(t.amount >= ? OR t.amount <= -?)")
			where = append(where, "(t.amount <= ? AND t.amount >= -?)")
			params = append(params, opts.AbsMinAmount, opts.AbsMinAmount, opts.AbsMaxAmount, opts.AbsMaxAmount)
		}
	} else if opts.AbsMinAmount != "" {
		where = append(where, "(t.amount >= ? OR t.amount <= -?)")
		params = append(params, opts.AbsMinAmount, opts.AbsMinAmount)
	} else if opts.AbsMaxAmount != "" {
		where = append(where, "(t.amount <= ? AND t.amount >= -?)")
		params = append(params, opts.AbsMaxAmount, opts.AbsMaxAmount)
	} else if opts.MinAmount != "" && opts.MaxAmount != "" {
		where = append(where, "t.amount BETWEEN ? AND ?")
		params = append(params, opts.MinAmount, opts.MaxAmount)
	} else if opts.MinAmount != "" {
		where = append(where, "t.amount >= ?")
		params = append(params, opts.MinAmount)
	} else if opts.MaxAmount != "" {
		where = append(where, "t.amount <= ?")
		params = append(params, opts.MaxAmount)
	}
	return where, params
}

// Helper to build WHERE clause and params for transaction queries
func BuildTransactionWhereClause(opts TransactionQueryOptions, withFTS bool) ([]string, []any) {
	var where []string
	var params []any
	if withFTS {
		// FTS query string should be passed as the first param by the caller
		where = append(where, "fts.search_body MATCH ?")
	}
	if opts.Days > 0 {
		where = append(where, "t.date >= date('now', ? )")
		params = append(params, fmt.Sprintf("%d days", -opts.Days))
	}
	if opts.Category != "" {
		where = append(where, "t.details_category = ?")
		params = append(params, opts.Category)
	}
	if opts.Type != "" {
		where = append(where, "t.type = ?")
		params = append(params, opts.Type)
	}
	if opts.Bank != "" {
		where = append(where, "t.bank = ?")
		params = append(params, opts.Bank)
	}
	where, params = addAmountFilters(opts, where, params)
	return where, params
}

// GetTransactions returns transactions with details from the last N days
// with optional limit, offset, and filters for category, type, and bank
func (d *DB) GetTransactions(ctx context.Context, options ...TransactionQueryOption) ([]types.TransactionWithDetails, error) {
	// Set defaults
	opts := TransactionQueryOptions{}
	for _, opt := range options {
		opt(&opts)
	}

	query := `
		SELECT t.date, t.amount, t.payee, t.bank,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.search_body,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference
		FROM transactions t
	`
	where, params := BuildTransactionWhereClause(opts, false)
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY t.date DESC"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
		if opts.Offset > 0 {
			query += fmt.Sprintf(" OFFSET %d", opts.Offset)
		}
	}
	d.logger.Debug("Executing SQL query", "query", query, "params", params)
	rows, err := d.db.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, fmt.Errorf("failed to query transactions: %w", err)
	}
	defer rows.Close()
	var transactions []types.TransactionWithDetails
	for rows.Next() {
		var t types.TransactionWithDetails
		if err := scanTransactionRow(rows, &t); err != nil {
			return nil, err
		}
		transactions = append(transactions, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating transactions: %w", err)
	}
	return transactions, nil
}

// SearchTransactionsByText performs a full-text search on transactions using a query and TransactionQueryOptions
func (d *DB) SearchTransactionsByText(ctx context.Context, query string, orderBy string, opts ...TransactionQueryOption) ([]types.TransactionSearchResult, int, error) {
	options := TransactionQueryOptions{}
	for _, opt := range opts {
		opt(&options)
	}
	where, params := BuildTransactionWhereClause(options, true)
	params = append([]any{query}, params...)
	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}
	// Count query
	countQuery := `
		SELECT COUNT(*)
		FROM transactions t
		JOIN transactions_fts fts ON t.rowid = fts.rowid
		` + whereClause + `
	`
	var totalCount int
	err := d.db.QueryRowContext(ctx, countQuery, params...).Scan(&totalCount)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count text search results: %w", err)
	}
	// Main search query
	orderClause := "ORDER BY text_score DESC"
	if orderBy == OrderByDate {
		orderClause = "ORDER BY t.date DESC"
	}
	searchQuery := `
		SELECT
			t.date, t.amount, t.payee, t.bank,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference,
			bm25(transactions_fts) as text_score
		FROM transactions t
		JOIN transactions_fts fts ON t.rowid = fts.rowid
		` + whereClause + `
		` + orderClause + `
	`
	rows, err := d.db.QueryContext(ctx, searchQuery, params...)
	if err != nil {
		return nil, 0, fmt.Errorf("text search failed: %w", err)
	}
	defer rows.Close()
	var results []types.TransactionSearchResult
	for rows.Next() {
		var t types.TransactionWithDetails
		var textScore float64
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
			&textScore,
		); err != nil {
			return nil, 0, fmt.Errorf("failed to scan transaction: %w", err)
		}
		// Format date and amount as strings
		t.Date = date.Format("02/01/2006")
		t.Amount = amount.String()
		SetForeignAmount(&t, foreignAmount, foreignCurrency)
		SetTransferDetails(&t, transferToAccount, transferFromAccount, transferReference)
		result := types.TransactionSearchResult{
			TransactionWithDetails: t,
			Scores: types.SearchScore{
				TextScore: textScore,
			},
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error iterating text results: %w", err)
	}
	// Apply offset and limit to results
	start := options.Offset
	if start > len(results) {
		start = len(results)
	}
	end := len(results)
	if options.Limit > 0 && start+options.Limit < end {
		end = start + options.Limit
	}
	results = results[start:end]
	return results, totalCount, nil
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

// GetCategoriesWithBank returns all unique categories and their counts from the last N days, optionally filtered by bank
func (d *DB) GetCategoriesWithBank(ctx context.Context, days int, bank string) ([]CategoryCount, error) {
	if bank == "" {
		return d.GetCategories(ctx, days)
	}
	rows, err := d.db.QueryContext(ctx, `
		SELECT
			details_category as category,
			COUNT(*) as count
		FROM transactions
		WHERE date >= date('now', ? || ' days')
		AND bank = ?
		AND details_category IS NOT NULL
		AND details_category != ''
		GROUP BY details_category
		ORDER BY count DESC, category ASC
	`, -days, bank)
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

// scanTransactionRow scans a transaction row into a TransactionWithDetails struct
func scanTransactionRow(rows *sql.Rows, t *types.TransactionWithDetails) error {
	var date time.Time
	var amount decimal.Decimal
	var searchBody string
	var foreignAmount sql.NullFloat64
	var foreignCurrency sql.NullString
	var transferToAccount sql.NullString
	var transferFromAccount sql.NullString
	var transferReference sql.NullString

	if err := rows.Scan(
		&date, &amount, &t.Payee, &t.Bank,
		&t.Details.Type, &t.Details.Merchant, &t.Details.Location, &t.Details.Category, &t.Details.Description, &t.Details.CardNumber,
		&searchBody,
		&foreignAmount, &foreignCurrency,
		&transferToAccount, &transferFromAccount, &transferReference,
	); err != nil {
		return fmt.Errorf("failed to scan transaction: %w", err)
	}

	// Format date and amount as strings
	t.Date = date.Format("02/01/2006")
	t.Amount = amount.String()
	t.Details.SearchBody = searchBody

	// Set foreign amount if present
	SetForeignAmount(t, foreignAmount, foreignCurrency)

	// Set transfer details if present
	SetTransferDetails(t, transferToAccount, transferFromAccount, transferReference)

	return nil
}

// SetForeignAmount sets the foreign amount details on a transaction if present
func SetForeignAmount(t *types.TransactionWithDetails, amount sql.NullFloat64, currency sql.NullString) {
	if amount.Valid && currency.Valid {
		t.Details.ForeignAmount = &types.ForeignAmountDetails{
			Amount:   decimal.NewFromFloat(amount.Float64),
			Currency: currency.String,
		}
	}
}

// SetTransferDetails sets the transfer details on a transaction if present
func SetTransferDetails(t *types.TransactionWithDetails, toAccount, fromAccount, reference sql.NullString) {
	if toAccount.Valid || fromAccount.Valid || reference.Valid {
		t.Details.TransferDetails = &types.TransferDetails{
			ToAccount:   toAccount.String,
			FromAccount: fromAccount.String,
			Reference:   reference.String,
		}
	}
}

// CountTransactions returns the number of transactions from last N days
func (d *DB) CountTransactions(ctx context.Context, days int) (int, error) {
	var count int
	err := d.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM transactions
		WHERE date >= date('now', ? || ' days')
	`, -days).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count transactions: %w", err)
	}

	return count, nil
}

// UpdateTransaction updates merchant, type, details_category, and tags for a transaction by ID
func (d *DB) UpdateTransaction(ctx context.Context, id string, merchant, txType, category, tags *string) error {
	query := "UPDATE transactions SET "
	params := []interface{}{}
	set := []string{}
	if merchant != nil {
		set = append(set, "merchant = ?")
		params = append(params, *merchant)
	}
	if txType != nil {
		set = append(set, "type = ?")
		params = append(params, *txType)
	}
	if category != nil {
		set = append(set, "details_category = ?")
		params = append(params, *category)
	}
	if tags != nil {
		set = append(set, "tags = ?")
		params = append(params, *tags)
	}
	if len(set) == 0 {
		return fmt.Errorf("no fields to update")
	}
	query += strings.Join(set, ", ") + " WHERE id = ?"
	params = append(params, id)
	_, err := d.db.ExecContext(ctx, query, params...)
	if err != nil {
		return fmt.Errorf("failed to update transaction: %w", err)
	}
	return nil
}

type TransactionIterator struct {
	rows *sql.Rows
	err  error
}

func (d *DB) IterateTransactions(ctx context.Context) *TransactionIterator {
	query := `
		SELECT t.date, t.amount, t.payee, t.bank,
			t.type, t.merchant, t.location, t.details_category, t.description, t.card_number,
			t.search_body,
			t.foreign_amount, t.foreign_currency,
			t.transfer_to_account, t.transfer_from_account, t.transfer_reference
		FROM transactions t
		ORDER BY t.date DESC
	`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		// You may want to handle this differently, e.g., panic or return a special iterator
		panic(err)
	}
	return &TransactionIterator{rows: rows}
}

// Go 1.23 iterator protocol
func (it *TransactionIterator) Next() (*types.TransactionWithDetails, bool) {
	if !it.rows.Next() {
		it.rows.Close()
		return nil, false
	}
	var t types.TransactionWithDetails
	if err := scanTransactionRow(it.rows, &t); err != nil {
		it.err = err
		return nil, false
	}
	return &t, true
}

// DB returns the underlying *sql.DB
func (d *DB) DB() *sql.DB {
	return d.db
}
