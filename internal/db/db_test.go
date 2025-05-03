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
