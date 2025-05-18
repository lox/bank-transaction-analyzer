package embeddings

import (
	"context"
	"testing"
	"time"

	"io"

	"github.com/charmbracelet/log"
	"github.com/stretchr/testify/assert"
)

func TestEmbeddingMetadataToMapAndFromMap(t *testing.T) {
	meta := EmbeddingMetadata{
		ContentHash: "abc123",
		ModelName:   "test-model",
		Length:      42,
		LastUpdated: time.Now().UTC().Truncate(time.Second),
	}
	m := meta.ToMap()
	parsed, err := EmbeddingFromMap(m)
	assert.NoError(t, err)
	assert.Equal(t, meta.ContentHash, parsed.ContentHash)
	assert.Equal(t, meta.ModelName, parsed.ModelName)
	assert.Equal(t, meta.Length, parsed.Length)
	// Allow a small delta for time parsing
	assert.WithinDuration(t, meta.LastUpdated, parsed.LastUpdated, time.Second)
}

func TestEmbeddingMetadataMatchContent(t *testing.T) {
	content := "hello world"
	hash := Hash(content)
	meta := EmbeddingMetadata{ContentHash: hash}
	assert.True(t, meta.MatchContent(content))
	assert.False(t, meta.MatchContent("other content"))
}

func TestHashDeterministic(t *testing.T) {
	content := "foo bar"
	h1 := Hash(content)
	h2 := Hash(content)
	assert.Equal(t, h1, h2)
	assert.NotEmpty(t, h1)
}

// MockVectorStorage is a simple in-memory implementation for testing interface compliance
// (does not persist or search, just for compile-time checks)
type MockVectorStorage struct{}

func (m *MockVectorStorage) StoreEmbedding(ctx context.Context, id string, text string, embedding []float32, metadata EmbeddingMetadata) error {
	return nil
}
func (m *MockVectorStorage) HasEmbedding(ctx context.Context, id string) (bool, EmbeddingMetadata, error) {
	return false, EmbeddingMetadata{}, nil
}
func (m *MockVectorStorage) Query(ctx context.Context, embedding []float32, threshold float32) ([]VectorResult, error) {
	return nil, nil
}
func (m *MockVectorStorage) Close() error                                         { return nil }
func (m *MockVectorStorage) RemoveEmbedding(ctx context.Context, id string) error { return nil }

func TestMockVectorStorageImplementsInterface(t *testing.T) {
	var _ VectorStorage = &MockVectorStorage{}
}

// mockEmbeddingProvider implements EmbeddingProvider for testing
type mockEmbeddingProvider struct{}

func (m *mockEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{1.0, 2.0, 3.0}, nil
}
func (m *mockEmbeddingProvider) GetEmbeddingModelName() string {
	return "mock-model"
}

func TestChromemStorageIntegration(t *testing.T) {
	logger := log.New(io.Discard)
	tempDir := t.TempDir()
	provider := &mockEmbeddingProvider{}

	store, err := NewChromemStorage(tempDir, provider, logger)
	assert.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	id := "test-id"
	text := "test content"
	embedding, _ := provider.GenerateEmbedding(ctx, text)
	meta := EmbeddingMetadata{
		ContentHash: Hash(text),
		ModelName:   provider.GetEmbeddingModelName(),
		Length:      len(embedding),
		LastUpdated: time.Now().UTC().Truncate(time.Second),
	}

	// Store embedding
	err = store.StoreEmbedding(ctx, id, text, embedding, meta)
	assert.NoError(t, err)

	// Retrieve embedding
	exists, gotMeta, err := store.HasEmbedding(ctx, id)
	assert.NoError(t, err)
	assert.True(t, exists)
	assert.Equal(t, meta.ContentHash, gotMeta.ContentHash)
	assert.Equal(t, meta.ModelName, gotMeta.ModelName)
	assert.Equal(t, meta.Length, gotMeta.Length)

	// Remove embedding
	err = store.RemoveEmbedding(ctx, id)
	assert.NoError(t, err)

	exists, _, err = store.HasEmbedding(ctx, id)
	assert.NoError(t, err)
	assert.False(t, exists)
}
