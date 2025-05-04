package analyzer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/philippgille/chromem-go"
)

// SimilarityResult represents a single result from a similarity search
type SimilarityResult struct {
	// ID is the transaction ID
	ID string
	// Similarity is the cosine similarity score (0.0-1.0)
	Similarity float32
}

// VectorStorage is an interface for storing and retrieving vector embeddings
type VectorStorage interface {
	// StoreEmbedding stores an embedding with the given transaction ID
	StoreEmbedding(ctx context.Context, id string, text string, embedding []float32) error

	// HasEmbedding checks if an embedding exists for the given transaction ID and content
	// If content is provided, it will also check if the stored content hash matches
	HasEmbedding(ctx context.Context, id string, content string) (bool, error)

	// QuerySimilar finds transaction IDs similar to the given embedding
	// threshold sets the minimum similarity score (0.0-1.0) for results
	// if threshold <= 0, no threshold is applied
	QuerySimilar(ctx context.Context, embedding []float32, limit int, threshold float32) ([]SimilarityResult, error)

	// Close closes the storage
	Close() error
}

// ChromemStorage implements VectorStorage using chromem-go vector database
type ChromemStorage struct {
	db         *chromem.DB
	collection *chromem.Collection
	logger     *log.Logger
	modelName  string
}

// hashContent creates a SHA-256 hash of the content
func hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// NewChromemStorage creates a new ChromemStorage
func NewChromemStorage(dataDir string, provider EmbeddingProvider, logger *log.Logger) (*ChromemStorage, error) {
	dbPath := filepath.Join(dataDir, "chromem-go")

	// Create a wrapper embedding function that uses our EmbeddingProvider
	embeddingFunc := func(ctx context.Context, text string) ([]float32, error) {
		return provider.GenerateEmbedding(ctx, text)
	}

	// Create a new persistent database with compression enabled
	db, err := chromem.NewPersistentDB(dbPath, true)
	if err != nil {
		return nil, fmt.Errorf("failed to create chromem database: %w", err)
	}

	// Create or get the collection
	collection, err := db.GetOrCreateCollection("transactions", nil, embeddingFunc)
	if err != nil {
		db.Reset() // Clean up
		return nil, fmt.Errorf("failed to create collection: %w", err)
	}
	// Create the storage and initialize model info
	storage := &ChromemStorage{
		db:         db,
		collection: collection,
		logger:     logger,
		modelName:  provider.GetEmbeddingModelName(),
	}

	count := collection.Count()
	logger.Info("Opened chromem vector database",
		"path", dbPath,
		"document_count", count,
		"model_name", storage.modelName)

	return storage, nil
}

// StoreEmbedding stores an embedding with the given transaction ID
func (s *ChromemStorage) StoreEmbedding(
	ctx context.Context,
	id string,
	text string,
	embedding []float32,
) error {
	// Create a document with just the transaction ID
	// The content is the search text that was embedded
	startTime := time.Now()

	// Generate content hash to store with the document
	contentHash := hashContent(text)

	// Create metadata with the content hash and model info
	metadata := map[string]string{
		"content_hash": contentHash,
		"model_name":   s.modelName,
	}

	// Create a document to add to the collection
	doc, err := chromem.NewDocument(ctx, id, metadata, embedding, text, nil)
	if err != nil {
		return fmt.Errorf("failed to create document: %w", err)
	}

	// Add document to collection
	err = s.collection.AddDocument(ctx, doc)
	if err != nil {
		return fmt.Errorf("failed to add document to collection: %w", err)
	}

	s.logger.Debug("Stored embedding",
		"id", id,
		"text_length", len(text),
		"vector_size", len(embedding),
		"model_name", s.modelName,
		"content_hash", contentHash,
		"duration", time.Since(startTime))

	return nil
}

// HasEmbedding checks if an embedding exists for the given transaction ID and content
func (s *ChromemStorage) HasEmbedding(ctx context.Context, id string, content string) (bool, error) {
	startTime := time.Now()

	// Check if the document exists by trying to get it
	doc, err := s.collection.GetByID(ctx, id)
	if err != nil {
		return false, nil
	}

	// If we found the document but don't have content to check, it exists
	if content == "" {
		s.logger.Debug("Document exists (no content check)",
			"id", id,
			"duration", time.Since(startTime))
		return true, nil
	}

	// Generate hash of the provided content
	newContentHash := hashContent(content)

	// Check if the document has metadata with content_hash
	metadata := doc.Metadata
	if metadata == nil {
		// No metadata, we need to update the embedding
		s.logger.Debug("Document exists but has no content hash",
			"id", id,
			"duration", time.Since(startTime))
		return false, nil
	}

	// Get the stored content hash
	storedHash, ok := metadata["content_hash"]
	if !ok {
		// No content hash in metadata, we need to update the embedding
		s.logger.Debug("Document exists but has no content hash in metadata",
			"id", id,
			"duration", time.Since(startTime))
		return false, nil
	}

	// Check model name
	storedModelName := metadata["model_name"]

	// Compare the hashes
	matches := storedHash == newContentHash

	// Add model info to log if available
	logFields := []interface{}{
		"id", id,
		"matches", matches,
		"stored_hash", storedHash,
		"new_hash", newContentHash,
		"duration", time.Since(startTime),
	}

	if storedModelName != "" {
		logFields = append(logFields, "model_name", storedModelName)
	}

	s.logger.Debug("Checked document content hash", logFields...)

	return matches, nil
}

// QuerySimilar finds transaction IDs similar to the given embedding
func (s *ChromemStorage) QuerySimilar(
	ctx context.Context,
	embedding []float32,
	limit int,
	threshold float32,
) ([]SimilarityResult, error) {
	startTime := time.Now()

	// Query for similar documents
	results, err := s.collection.QueryEmbedding(ctx, embedding, limit, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar documents: %w", err)
	}

	// Extract IDs and similarity scores, filtering by threshold if applicable
	var similarityResults []SimilarityResult

	for _, result := range results {
		// Apply threshold filtering if requested
		if threshold > 0 && result.Similarity < threshold {
			continue
		}

		similarityResults = append(similarityResults, SimilarityResult{
			ID:         result.ID,
			Similarity: result.Similarity,
		})
	}

	s.logger.Debug("Query similar completed",
		"raw_results", len(results),
		"filtered_results", len(similarityResults),
		"threshold", threshold,
		"model_name", s.modelName,
		"duration", time.Since(startTime))

	return similarityResults, nil
}

// Close closes the database
func (s *ChromemStorage) Close() error {
	// Nothing to do as chromem doesn't have an explicit close method
	// The database is automatically persisted on write operations
	return nil
}
