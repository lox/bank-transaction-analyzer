package db

import (
	"context"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

func setupTestDB(t *testing.T) (*DB, func()) {
	// Create a temporary directory for the test database
	tempDir, err := os.MkdirTemp("", "bank-transaction-analyzer-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	// Initialize logger
	logger := log.New(io.Discard)

	// Initialize timezone
	loc, err := time.LoadLocation("UTC")
	if err != nil {
		t.Fatalf("failed to load UTC timezone: %v", err)
	}

	// Create database
	db, err := New(tempDir, logger, loc)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to create database: %v", err)
	}

	// Return database and cleanup function
	return db, func() {
		db.Close()
		os.RemoveAll(tempDir)
	}
}

func TestStoreAndGetTransaction(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a test transaction
	transaction := types.Transaction{
		Date:   "01/01/2023",
		Amount: "100.00",
		Payee:  "Test Payee",
		Bank:   "Test Bank",
	}

	details := &types.TransactionDetails{
		Type:        "purchase",
		Merchant:    "Test Merchant",
		Location:    "Test Location",
		Category:    "Test Category",
		Description: "Test Description",
		CardNumber:  "1234",
		SearchBody:  "Test Merchant Test Location Test Description",
	}

	// Store transaction
	err := db.Store(ctx, transaction, details)
	if err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}

	// Retrieve transaction
	retrievedDetails, err := db.Get(ctx, transaction)
	if err != nil {
		t.Fatalf("failed to get transaction: %v", err)
	}

	// Verify details
	if retrievedDetails.Type != details.Type {
		t.Errorf("expected type %s, got %s", details.Type, retrievedDetails.Type)
	}
	if retrievedDetails.Merchant != details.Merchant {
		t.Errorf("expected merchant %s, got %s", details.Merchant, retrievedDetails.Merchant)
	}
	if retrievedDetails.Location != details.Location {
		t.Errorf("expected location %s, got %s", details.Location, retrievedDetails.Location)
	}
	if retrievedDetails.Category != details.Category {
		t.Errorf("expected category %s, got %s", details.Category, retrievedDetails.Category)
	}
	if retrievedDetails.Description != details.Description {
		t.Errorf("expected description %s, got %s", details.Description, retrievedDetails.Description)
	}
	if retrievedDetails.CardNumber != details.CardNumber {
		t.Errorf("expected card number %s, got %s", details.CardNumber, retrievedDetails.CardNumber)
	}
}

func TestSearchTransactions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a test transaction with today's date
	transaction := types.Transaction{
		Date:   time.Now().Format("02/01/2006"),
		Amount: "100.00",
		Payee:  "Coffee Shop",
		Bank:   "Test Bank",
	}

	details := &types.TransactionDetails{
		Type:        "purchase",
		Merchant:    "Coffee Shop",
		Location:    "Downtown",
		Category:    "Food & Dining",
		Description: "Morning coffee",
		SearchBody:  "Coffee Shop Downtown Morning coffee",
	}

	// Store the transaction
	err := db.Store(ctx, transaction, details)
	if err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}

	// Test text search
	textResults, totalCount, err := db.SearchTransactionsByText(ctx, "coffee", 30, 10)
	if err != nil {
		t.Fatalf("Failed to search transactions: %v", err)
	}
	if len(textResults) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(textResults))
	}
	if totalCount != 1 {
		t.Fatalf("Expected total count of 1, got %d", totalCount)
	}
	if textResults[0].TransactionWithDetails.Details.Merchant != "Coffee Shop" {
		t.Errorf("Expected merchant 'Coffee Shop', got '%s'", textResults[0].TransactionWithDetails.Details.Merchant)
	}

	// Check that we have a valid text score (BM25 scores are negative in SQLite)
	if textResults[0].Scores.TextScore >= 0 {
		t.Errorf("Expected negative text score (SQLite BM25), got %f", textResults[0].Scores.TextScore)
	}
}

func TestHasTransaction(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create a test transaction
	transaction := types.Transaction{
		Date:   "01/01/2023",
		Amount: "100.00",
		Payee:  "Test Store",
		Bank:   "Test Bank",
	}

	details := &types.TransactionDetails{
		Type:        "purchase",
		Merchant:    "Test Store",
		Location:    "Test Location",
		Category:    "Shopping",
		Description: "Test purchase",
		SearchBody:  "Test Store Test Location Test purchase",
	}

	// Check if transaction exists (should be false)
	exists, err := db.Has(ctx, transaction)
	if err != nil {
		t.Fatalf("failed to check transaction existence: %v", err)
	}
	if exists {
		t.Error("expected transaction to not exist")
	}

	// Store the transaction
	err = db.Store(ctx, transaction, details)
	if err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}

	// Check if transaction exists (should be true)
	exists, err = db.Has(ctx, transaction)
	if err != nil {
		t.Fatalf("failed to check transaction existence: %v", err)
	}
	if !exists {
		t.Error("expected transaction to exist")
	}
}

func TestTransactionIDConsistency(t *testing.T) {
	// Create two identical transactions
	t1 := types.Transaction{
		Date:   "01/01/2023",
		Amount: "100.00",
		Payee:  "Test Store",
		Bank:   "Test Bank",
	}
	t2 := types.Transaction{
		Date:   "01/01/2023",
		Amount: "100.00",
		Payee:  "Test Store",
		Bank:   "Test Bank",
	}

	// Generate IDs for both transactions
	id1 := GenerateTransactionID(t1)
	id2 := GenerateTransactionID(t2)

	// IDs should be identical for identical transactions
	if id1 != id2 {
		t.Errorf("expected identical transaction IDs, got %q and %q", id1, id2)
	}

	// Create a slightly different transaction
	t3 := types.Transaction{
		Date:   "01/01/2023",
		Amount: "100.00",
		Payee:  "Test Store",
		Bank:   "Different Bank", // Only difference is the bank
	}

	// Generate ID for the different transaction
	id3 := GenerateTransactionID(t3)

	// ID should be different for different transactions
	if id1 == id3 {
		t.Errorf("expected different transaction IDs for different transactions, got identical IDs")
	}
}

func TestFilterExistingTransactions(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create some test transactions
	transactions := []types.Transaction{
		{
			Date:   "01/01/2023",
			Amount: "100.00",
			Payee:  "Test Store 1",
			Bank:   "Test Bank",
		},
		{
			Date:   "02/01/2023",
			Amount: "200.00",
			Payee:  "Test Store 2",
			Bank:   "Test Bank",
		},
		{
			Date:   "03/01/2023",
			Amount: "300.00",
			Payee:  "Test Store 3",
			Bank:   "Test Bank",
		},
	}

	// Store the first two transactions
	for i := 0; i < 2; i++ {
		details := &types.TransactionDetails{
			Type:        "purchase",
			Merchant:    transactions[i].Payee,
			Category:    "Shopping",
			Description: "Test purchase",
			SearchBody:  transactions[i].Payee + " Test purchase",
		}
		if err := db.Store(ctx, transactions[i], details); err != nil {
			t.Fatalf("failed to store transaction %d: %v", i, err)
		}
	}

	// Filter the transactions
	filtered, err := db.FilterExistingTransactions(ctx, transactions)
	if err != nil {
		t.Fatalf("failed to filter transactions: %v", err)
	}

	// Should only have one transaction (the third one)
	if len(filtered) != 1 {
		t.Errorf("expected 1 filtered transaction, got %d", len(filtered))
	}

	// The remaining transaction should be the third one
	if filtered[0].Payee != transactions[2].Payee {
		t.Errorf("expected filtered transaction to be %q, got %q", transactions[2].Payee, filtered[0].Payee)
	}
}

func TestGetTransactionsWithPagination(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Insert 10 test transactions with sequential dates
	for i := 0; i < 10; i++ {
		// Create transaction with date as today - i days
		date := time.Now().AddDate(0, 0, -i)
		transaction := types.Transaction{
			Date:   date.Format("02/01/2006"),
			Amount: fmt.Sprintf("%d.00", 100+i), // Different amounts to verify ordering
			Payee:  fmt.Sprintf("Test Store %d", i),
			Bank:   "Test Bank",
		}

		details := &types.TransactionDetails{
			Type:        "purchase",
			Merchant:    fmt.Sprintf("Store %d", i),
			Location:    fmt.Sprintf("Location %d", i),
			Category:    "Shopping",
			Description: fmt.Sprintf("Test purchase %d", i),
		}

		if err := db.Store(ctx, transaction, details); err != nil {
			t.Fatalf("failed to store transaction: %v", err)
		}
	}

	// Test GetTransactions with various limit and offset combinations
	testCases := []struct {
		name     string
		limit    int
		offset   int
		expected int // expected number of results
	}{
		{
			name:     "No limit or offset",
			limit:    0,
			offset:   0,
			expected: 10,
		},
		{
			name:     "Limit only",
			limit:    5,
			offset:   0,
			expected: 5,
		},
		{
			name:     "Offset only",
			limit:    0,
			offset:   5, // Should be ignored without a limit
			expected: 10,
		},
		{
			name:     "Limit and offset",
			limit:    3,
			offset:   2,
			expected: 3,
		},
		{
			name:     "Offset beyond results",
			limit:    10,
			offset:   20,
			expected: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			transactions, err := db.GetTransactions(ctx, 30, tc.limit, tc.offset)
			if err != nil {
				t.Fatalf("Failed to get transactions: %v", err)
			}

			if len(transactions) != tc.expected {
				t.Errorf("Expected %d transactions, got %d", tc.expected, len(transactions))
			}

			// If we have at least 2 results, verify they're sorted by date (most recent first)
			if len(transactions) >= 2 {
				for i := 0; i < len(transactions)-1; i++ {
					date1, _ := time.Parse("02/01/2006", transactions[i].Date)
					date2, _ := time.Parse("02/01/2006", transactions[i+1].Date)
					if date1.Before(date2) {
						t.Errorf("Transactions not sorted by date: %s before %s",
							transactions[i].Date, transactions[i+1].Date)
					}
				}
			}

			// When using both limit and offset, check specific expected records
			if tc.limit > 0 && tc.offset > 0 && len(transactions) > 0 {
				// We expect to get records starting from offset position
				// The most recent date is at index 0, so offset 2 means we expect Store 2, 3, 4...
				expectedStartId := tc.offset
				if transactions[0].Details.Merchant != fmt.Sprintf("Store %d", expectedStartId) {
					t.Errorf("Expected first record to be Store %d, got %s",
						expectedStartId, transactions[0].Details.Merchant)
				}
			}
		})
	}
}
