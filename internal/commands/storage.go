package commands

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
)

// SetupVectorStorage initializes and returns a vector storage based on the config
func SetupVectorStorage(
	ctx context.Context,
	dataDir string,
	provider embeddings.EmbeddingProvider,
	logger *log.Logger,
) (embeddings.VectorStorage, error) {
	vectorStorage, err := embeddings.NewChromemStorage(dataDir, provider, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create vector storage: %w", err)
	}

	return vectorStorage, nil
}
