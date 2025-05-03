package db

import (
	"context"
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

	// Create a logger that discards output
	logger := log.New(io.Discard)
	logger.SetLevel(log.DebugLevel)

	// Create a new database connection
	db, err := New(tempDir, logger, time.Local)
	if err != nil {
		t.Fatalf("failed to create database: %v", err)
	}

	// Return cleanup function
	cleanup := func() {
		db.Close()
		os.RemoveAll(tempDir)
	}

	return db, cleanup
}

func TestStoreAndGetTransaction(t *testing.T) {
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
		CardNumber:  "1234",
		SearchBody:  "Test Store Test Location Test purchase",
	}

	// Store the transaction
	err := db.Store(ctx, transaction, details)
	if err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}

	// Retrieve the transaction
	retrievedDetails, err := db.Get(ctx, transaction)
	if err != nil {
		t.Fatalf("failed to get transaction: %v", err)
	}

	if retrievedDetails == nil {
		t.Fatal("expected to find transaction details, got nil")
	}

	// Verify the details
	if retrievedDetails.Type != details.Type {
		t.Errorf("expected type %q, got %q", details.Type, retrievedDetails.Type)
	}
	if retrievedDetails.Merchant != details.Merchant {
		t.Errorf("expected merchant %q, got %q", details.Merchant, retrievedDetails.Merchant)
	}
	if retrievedDetails.Location != details.Location {
		t.Errorf("expected location %q, got %q", details.Location, retrievedDetails.Location)
	}
	if retrievedDetails.Category != details.Category {
		t.Errorf("expected category %q, got %q", details.Category, retrievedDetails.Category)
	}
	if retrievedDetails.Description != details.Description {
		t.Errorf("expected description %q, got %q", details.Description, retrievedDetails.Description)
	}
	if retrievedDetails.CardNumber != details.CardNumber {
		t.Errorf("expected card number %q, got %q", details.CardNumber, retrievedDetails.CardNumber)
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
	textResults, err := db.SearchTransactionsByText(ctx, "coffee", 30, 10)
	if err != nil {
		t.Fatalf("Failed to search transactions: %v", err)
	}
	if len(textResults) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(textResults))
	}
	if textResults[0].Details.Merchant != "Coffee Shop" {
		t.Errorf("Expected merchant 'Coffee Shop', got '%s'", textResults[0].Details.Merchant)
	}

	// Test hybrid search
	mockEmbedding := make([]float32, 768)
	serializedEmbedding, err := db.SerializeEmbedding(mockEmbedding)
	if err != nil {
		t.Fatalf("Failed to serialize embedding: %v", err)
	}

	results, err := db.SearchTransactions(ctx, "coffee", serializedEmbedding, 30, 10)
	if err != nil {
		t.Fatalf("Failed to search transactions: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results))
	}
	if results[0].TransactionWithDetails.Details.Merchant != "Coffee Shop" {
		t.Errorf("Expected merchant 'Coffee Shop', got '%s'", results[0].TransactionWithDetails.Details.Merchant)
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
