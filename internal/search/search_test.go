package search

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

func setupTestDB(t *testing.T) (*db.DB, func()) {
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
	dbConn, err := db.New(tempDir, logger, loc)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("failed to create database: %v", err)
	}

	// Return database and cleanup function
	return dbConn, func() {
		dbConn.Close()
		os.RemoveAll(tempDir)
	}
}

func TestTextSearch(t *testing.T) {
	dbConn, cleanup := setupTestDB(t)
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
	err := dbConn.Store(ctx, transaction, details)
	if err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}

	// Test text search
	textResults, totalCount, err := TextSearch(ctx, dbConn, "coffee", OrderByRelevance(), WithDays(30), WithLimit(10))
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

// --- Mocks for embeddings and vector storage ---
type mockEmbeddingProvider struct{}

func (m *mockEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{1.0, 2.0, 3.0}, nil
}

func (m *mockEmbeddingProvider) GetEmbeddingModelName() string {
	return "mock-model"
}

type mockVectorStorage struct {
	results []mockVectorResult
}

type mockVectorResult struct {
	ID         string
	Similarity float32
}

func (m *mockVectorStorage) Query(ctx context.Context, embedding []float32, threshold float32) ([]embeddings.VectorResult, error) {
	var out []embeddings.VectorResult
	for _, r := range m.results {
		if r.Similarity >= threshold {
			out = append(out, embeddings.VectorResult{ID: r.ID, Similarity: r.Similarity})
		}
	}
	return out, nil
}

func (m *mockVectorStorage) StoreEmbedding(ctx context.Context, id string, text string, embedding []float32, metadata embeddings.EmbeddingMetadata) error {
	return nil
}

func (m *mockVectorStorage) HasEmbedding(ctx context.Context, id string) (bool, embeddings.EmbeddingMetadata, error) {
	return false, embeddings.EmbeddingMetadata{}, nil
}

func (m *mockVectorStorage) Close() error {
	return nil
}

func (m *mockVectorStorage) RemoveEmbedding(ctx context.Context, id string) error {
	return nil
}

func TestVectorSearch(t *testing.T) {
	dbConn, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Insert a transaction
	transaction := types.Transaction{
		Date:   time.Now().Format("02/01/2006"),
		Amount: "100.00",
		Payee:  "Vector Store",
		Bank:   "Test Bank",
	}
	details := &types.TransactionDetails{
		Type:        "purchase",
		Merchant:    "Vector Store",
		Category:    "Test Category",
		Description: "Test vector transaction",
		SearchBody:  "Vector Store Test vector transaction",
	}
	if err := dbConn.Store(ctx, transaction, details); err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}
	id := db.GenerateTransactionID(transaction)

	provider := &mockEmbeddingProvider{}
	vectors := &mockVectorStorage{results: []mockVectorResult{{ID: id, Similarity: 0.99}}}
	logger := log.New(io.Discard)

	results, err := VectorSearch(ctx, logger, dbConn, provider, vectors, "vector", WithLimit(5), WithDays(30), OrderByRelevance())
	if err != nil {
		t.Fatalf("VectorSearch failed: %v", err)
	}
	if results.TotalCount != 1 {
		t.Fatalf("Expected 1 result, got %d", results.TotalCount)
	}
	if len(results.Results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results.Results))
	}
	if results.Results[0].TransactionWithDetails.Details.Merchant != "Vector Store" {
		t.Errorf("Expected merchant 'Vector Store', got '%s'", results.Results[0].TransactionWithDetails.Details.Merchant)
	}
}

func TestVectorSearch_AllTimeByDefault(t *testing.T) {
	dbConn, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Insert two transactions: one today, one 100 days ago
	today := time.Now()
	oldDate := today.AddDate(0, 0, -100)

	transactions := []types.Transaction{
		{
			Date:   today.Format("02/01/2006"),
			Amount: "100.00",
			Payee:  "Recent Store",
			Bank:   "Test Bank",
		},
		{
			Date:   oldDate.Format("02/01/2006"),
			Amount: "50.00",
			Payee:  "Old Store",
			Bank:   "Test Bank",
		},
	}
	details := []*types.TransactionDetails{
		{
			Type:        "purchase",
			Merchant:    "Recent Store",
			Category:    "Test Category",
			Description: "Recent transaction",
			SearchBody:  "Recent Store Recent transaction",
		},
		{
			Type:        "purchase",
			Merchant:    "Old Store",
			Category:    "Test Category",
			Description: "Old transaction",
			SearchBody:  "Old Store Old transaction",
		},
	}

	for i := range transactions {
		err := dbConn.Store(ctx, transactions[i], details[i])
		if err != nil {
			t.Fatalf("failed to store transaction: %v", err)
		}
	}

	ids := []string{
		db.GenerateTransactionID(transactions[0]),
		db.GenerateTransactionID(transactions[1]),
	}

	provider := &mockEmbeddingProvider{}
	vectors := &mockVectorStorage{
		results: []mockVectorResult{
			{ID: ids[0], Similarity: 0.99},
			{ID: ids[1], Similarity: 0.98},
		},
	}
	logger := log.New(io.Discard)

	// No days, no dateCutoff: should return both
	results, err := VectorSearch(ctx, logger, dbConn, provider, vectors, "store")
	if err != nil {
		t.Fatalf("VectorSearch failed: %v", err)
	}
	if results.TotalCount != 2 {
		t.Fatalf("Expected 2 results for all time, got %d", results.TotalCount)
	}
	if len(results.Results) != 2 {
		t.Fatalf("Expected 2 results for all time, got %d", len(results.Results))
	}

	// With days=30: should only return the recent one
	results, err = VectorSearch(ctx, logger, dbConn, provider, vectors, "store", WithDays(30))
	if err != nil {
		t.Fatalf("VectorSearch failed: %v", err)
	}
	if results.TotalCount != 1 {
		t.Fatalf("Expected 1 result for days=30, got %d", results.TotalCount)
	}
	if len(results.Results) != 1 {
		t.Fatalf("Expected 1 result for days=30, got %d", len(results.Results))
	}
	if results.Results[0].TransactionWithDetails.Details.Merchant != "Recent Store" {
		t.Errorf("Expected merchant 'Recent Store', got '%s'", results.Results[0].TransactionWithDetails.Details.Merchant)
	}
}

func TestHybridSearch(t *testing.T) {
	dbConn, cleanup := setupTestDB(t)
	defer cleanup()
	ctx := context.Background()

	// Insert a transaction
	transaction := types.Transaction{
		Date:   time.Now().Format("02/01/2006"),
		Amount: "100.00",
		Payee:  "Hybrid Store",
		Bank:   "Test Bank",
	}
	details := &types.TransactionDetails{
		Type:        "purchase",
		Merchant:    "Hybrid Store",
		Category:    "Test Category",
		Description: "Test hybrid transaction",
		SearchBody:  "Hybrid Store Test hybrid transaction",
	}
	if err := dbConn.Store(ctx, transaction, details); err != nil {
		t.Fatalf("failed to store transaction: %v", err)
	}
	id := db.GenerateTransactionID(transaction)

	provider := &mockEmbeddingProvider{}
	vectors := &mockVectorStorage{results: []mockVectorResult{{ID: id, Similarity: 0.95}}}
	logger := log.New(io.Discard)

	results, err := HybridSearch(ctx, logger, dbConn, provider, vectors, "hybrid", WithLimit(5), WithDays(30), OrderByRelevance())
	if err != nil {
		t.Fatalf("HybridSearch failed: %v", err)
	}
	if results.TotalCount != 1 {
		t.Fatalf("Expected 1 result, got %d", results.TotalCount)
	}
	if len(results.Results) != 1 {
		t.Fatalf("Expected 1 result, got %d", len(results.Results))
	}
	if results.Results[0].TransactionWithDetails.Details.Merchant != "Hybrid Store" {
		t.Errorf("Expected merchant 'Hybrid Store', got '%s'", results.Results[0].TransactionWithDetails.Details.Merchant)
	}
}
