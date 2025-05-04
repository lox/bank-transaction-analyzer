package commands

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
)

// SetupVectorStorage initializes and returns a vector storage based on the config
func SetupVectorStorage(
	ctx context.Context,
	dataDir string,
	provider analyzer.EmbeddingProvider,
	logger *log.Logger,
) (analyzer.VectorStorage, error) {
	vectorStorage, err := analyzer.NewChromemStorage(dataDir, provider, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create vector storage: %w", err)
	}

	return vectorStorage, nil
}
