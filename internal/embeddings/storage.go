package embeddings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/philippgille/chromem-go"
)

// VectorResult represents a single result from a vector search
type VectorResult struct {
	// ID is the transaction ID
	ID string
	// Similarity is the cosine similarity score (0.0-1.0)
	Similarity float32
	// Content is the content of the document
	Content string
}

type EmbeddingMetadata struct {
	ContentHash string    `json:"content_hash"`
	ModelName   string    `json:"model_name"`
	Length      int       `json:"length"`
	LastUpdated time.Time `json:"last_updated"`
}

func (m *EmbeddingMetadata) ToMap() map[string]string {
	return map[string]string{
		"content_hash": m.ContentHash,
		"model_name":   m.ModelName,
		"length":       strconv.Itoa(m.Length),
		"last_updated": m.LastUpdated.Format(time.RFC3339),
	}
}

func EmbeddingFromMap(metadata map[string]string) (EmbeddingMetadata, error) {
	length, err := strconv.Atoi(metadata["length"])
	if err != nil {
		return EmbeddingMetadata{}, fmt.Errorf("failed to parse length: %w", err)
	}
	lastUpdated, err := time.Parse(time.RFC3339, metadata["last_updated"])
	if err != nil {
		return EmbeddingMetadata{}, fmt.Errorf("failed to parse last updated: %w", err)
	}
	return EmbeddingMetadata{
		ContentHash: metadata["content_hash"],
		ModelName:   metadata["model_name"],
		Length:      length,
		LastUpdated: lastUpdated,
	}, nil
}

func (m *EmbeddingMetadata) MatchContent(content string) bool {
	return m.ContentHash == Hash(content)
}

// VectorStorage is an interface for storing and retrieving vector embeddings
type VectorStorage interface {
	// StoreEmbedding stores an embedding with the given transaction ID
	StoreEmbedding(ctx context.Context, id string, text string, embedding []float32, metadata EmbeddingMetadata) error

	// HasEmbedding checks if an embedding exists for the given transaction ID
	// and returns the metadata if it does
	HasEmbedding(ctx context.Context, id string) (bool, EmbeddingMetadata, error)

	// Query finds transaction IDs similar to the given embedding
	// threshold sets the minimum similarity score (0.0-1.0) for results
	// if threshold <= 0, no threshold is applied
	Query(ctx context.Context, embedding []float32, threshold float32) ([]VectorResult, error)

	// Close closes the storage
	Close() error

	// RemoveEmbedding removes an embedding/document by ID from the collection
	RemoveEmbedding(ctx context.Context, id string) error
}

// ChromemStorage implements VectorStorage using chromem-go vector database
type ChromemStorage struct {
	db         *chromem.DB
	collection *chromem.Collection
	logger     *log.Logger
	modelName  string
}

// Hash creates a SHA-256 hash of the content
func Hash(content string) string {
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
	metadata EmbeddingMetadata,
) error {
	// Create a document to add to the collection
	doc, err := chromem.NewDocument(ctx, id, metadata.ToMap(), embedding, text, nil)
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
		"metadata", metadata)

	return nil
}

// HasEmbedding checks if an embedding exists for the given transaction ID and content
func (s *ChromemStorage) HasEmbedding(ctx context.Context, id string) (bool, EmbeddingMetadata, error) {
	// Check if the document exists by trying to get it
	doc, err := s.collection.GetByID(ctx, id)
	if err != nil {
		return false, EmbeddingMetadata{}, nil
	}

	metadata, err := EmbeddingFromMap(doc.Metadata)
	if err != nil {
		return false, EmbeddingMetadata{}, fmt.Errorf("failed to parse metadata for id %s: %w", id, err)
	}

	return true, metadata, nil
}

// QuerySimilar finds transaction IDs similar to the given embedding
func (s *ChromemStorage) Query(ctx context.Context, embedding []float32, threshold float32) ([]VectorResult, error) {
	// Query for similar documents
	results, err := s.collection.QueryEmbedding(ctx, embedding, s.collection.Count(), nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to query embeddings: %w", err)
	}

	// Extract IDs and similarity scores, filtering by threshold if applicable
	var vectorResults []VectorResult

	for _, result := range results {
		if result.Similarity < threshold {
			continue
		}
		vectorResults = append(vectorResults, VectorResult{
			ID:         result.ID,
			Similarity: result.Similarity,
			Content:    result.Content,
		})
	}

	// Sort results by similarity (highest first)
	sort.Slice(vectorResults, func(i, j int) bool {
		return vectorResults[i].Similarity > vectorResults[j].Similarity
	})

	return vectorResults, nil
}

// Close closes the database
func (s *ChromemStorage) Close() error {
	// Nothing to do as chromem doesn't have an explicit close method
	// The database is automatically persisted on write operations
	return nil
}

// RemoveEmbedding removes an embedding/document by ID from the collection
func (s *ChromemStorage) RemoveEmbedding(ctx context.Context, id string) error {
	return s.collection.Delete(ctx, nil, nil, id)
}
