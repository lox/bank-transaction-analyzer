package analyzer

import (
	"context"
	"fmt"
	"os"
	"sync"

	"runtime"

	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-parser/internal/qif"
	"github.com/philippgille/chromem-go"
)

// EmbeddingStore handles persistence of transaction embeddings
type EmbeddingStore struct {
	db         *chromem.DB
	mu         sync.RWMutex
	dataDir    string
	logger     *log.Logger
	collection *chromem.Collection
}

// NewEmbeddingStore creates a new embedding store
func NewEmbeddingStore(dataDir string, logger *log.Logger) (*EmbeddingStore, error) {
	// Create data directory if it doesn't exist
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %v", err)
	}

	// Initialize persistent DB without compression
	db, err := chromem.NewPersistentDB(dataDir, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create persistent DB: %v", err)
	}

	store := &EmbeddingStore{
		db:      db,
		dataDir: dataDir,
		logger:  logger,
	}

	// Get or create the collection
	collection, err := db.GetOrCreateCollection("transactions", nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get/create collection: %v", err)
	}
	store.collection = collection

	// Log the initial collection size
	count := collection.Count()
	store.logger.Info("Embedding store initialized", "collection_size", count)
	return store, nil
}

// generateKey creates a unique key for a transaction
func generateKey(t qif.Transaction) string {
	return fmt.Sprintf("%s|%s|%s", t.Date, t.Payee, t.Amount)
}

// StoreEmbeddings stores embeddings for a batch of transactions
func (s *EmbeddingStore) StoreEmbeddings(ctx context.Context, transactions []qif.Transaction, embeddings [][]float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("Storing embeddings", "count", len(transactions))

	docs := make([]chromem.Document, len(transactions))
	for i, t := range transactions {
		// Create a unique key for the transaction
		key := generateKey(t)

		// Create the document with the embedding
		docs[i] = chromem.Document{
			ID:        key,
			Content:   fmt.Sprintf("%s %s", t.Payee, t.Amount),
			Embedding: embeddings[i],
			Metadata: map[string]string{
				"date":   t.Date,
				"payee":  t.Payee,
				"amount": t.Amount,
			},
		}
	}

	// Store all documents at once
	err := s.collection.AddDocuments(ctx, docs, runtime.NumCPU())
	if err != nil {
		return fmt.Errorf("failed to store embeddings: %v", err)
	}

	s.logger.Info("Successfully stored embeddings", "count", len(docs))
	return nil
}

// GetEmbeddings retrieves embeddings for transactions
func (s *EmbeddingStore) GetEmbeddings(ctx context.Context, transactions []qif.Transaction) ([][]float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.logger.Info("Retrieving embeddings", "count", len(transactions))
	embeddings := make([][]float32, len(transactions))

	for i, t := range transactions {
		// Create a unique key for the transaction
		key := generateKey(t)

		// Query for the document with the exact key
		results, err := s.collection.Query(ctx, key, 1, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to query embedding for transaction %d: %v", i, err)
		}

		if len(results) == 0 {
			return nil, fmt.Errorf("no embedding found for transaction %d", i)
		}

		// The embedding is already []float32
		embeddings[i] = results[0].Embedding
	}

	s.logger.Info("Successfully retrieved embeddings", "count", len(embeddings))
	return embeddings, nil
}

// FindSimilarTransactions finds transactions similar to the given transaction
func (s *EmbeddingStore) FindSimilarTransactions(ctx context.Context, t qif.Transaction, limit int) ([]qif.Transaction, []float32, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a query text from the transaction
	query := fmt.Sprintf("%s %s", t.Payee, t.Amount)

	// Query for similar documents
	results, err := s.collection.Query(ctx, query, limit, nil, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query similar transactions: %v", err)
	}

	// Convert results to transactions
	transactions := make([]qif.Transaction, len(results))
	similarities := make([]float32, len(results))
	for i, result := range results {
		transactions[i] = qif.Transaction{
			Date:   result.Metadata["date"],
			Payee:  result.Metadata["payee"],
			Amount: result.Metadata["amount"],
		}
		similarities[i] = result.Similarity
	}

	return transactions, similarities, nil
}

// GetEmbeddingStatus returns a map of transaction indices to whether they have embeddings
func (s *EmbeddingStore) GetEmbeddingStatus(ctx context.Context, transactions []qif.Transaction) (map[int]bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := make(map[int]bool)
	for i, t := range transactions {
		key := generateKey(t)
		// Check if the document exists by ID
		doc, err := s.collection.GetByID(ctx, key)
		if err != nil {
			// Any error means the document doesn't exist
			status[i] = false
			continue
		}
		// Document exists if it has an ID
		status[i] = doc.ID != ""
	}
	return status, nil
}

// StoreEmbedding stores an embedding for a single transaction
func (s *EmbeddingStore) StoreEmbedding(ctx context.Context, t qif.Transaction, embedding []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Debug("Storing embedding", "transaction", t)

	// Create a unique key for the transaction
	key := generateKey(t)

	// Create the document with the embedding
	doc := chromem.Document{
		ID:        key,
		Content:   fmt.Sprintf("%s %s", t.Payee, t.Amount),
		Embedding: embedding,
		Metadata: map[string]string{
			"date":   t.Date,
			"payee":  t.Payee,
			"amount": t.Amount,
		},
	}

	// Store the document
	err := s.collection.AddDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to store embedding: %v", err)
	}

	s.logger.Debug("Successfully stored embedding", "transaction", t)
	return nil
}

// Close closes the embedding store
func (s *EmbeddingStore) Close() error {
	return nil // No need to close anything, persistence is handled by chromem
}

// CountMissingEmbeddings returns the number of transactions that don't have embeddings
func (s *EmbeddingStore) CountMissingEmbeddings(ctx context.Context, transactions []qif.Transaction) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status, err := s.GetEmbeddingStatus(ctx, transactions)
	if err != nil {
		return 0, fmt.Errorf("error checking embedding status: %v", err)
	}

	var count int
	for _, exists := range status {
		if !exists {
			count++
		}
	}
	return count, nil
}
