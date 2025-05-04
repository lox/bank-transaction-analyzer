package commands

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
)

// EmbeddingOptions contains runtime configuration for embedding providers
type EmbeddingOptions struct {
	// Provider is the name of the embedding provider to use (llamacpp or gemini)
	Provider string
	// LlamaCppModel is the name of the specific LLaMA.cpp model to use
	LlamaCppModel string
	// GeminiAPIKey is the API key for Gemini
	GeminiAPIKey string
	// GeminiModel is the name of the Gemini model to use
	GeminiModel string
	// Logger is used for logging embedding operations
	Logger *log.Logger
}

// SetupEmbeddingProvider initializes and returns an embedding provider based on the config
func SetupEmbeddingProvider(ctx context.Context, options EmbeddingOptions) (analyzer.EmbeddingProvider, error) {
	var embeddingProvider analyzer.EmbeddingProvider
	var err error

	switch options.Provider {
	case "gemini":
		if options.GeminiAPIKey == "" {
			return nil, fmt.Errorf("gemini api key is required when using Gemini embeddings")
		}

		// Create Gemini embedding provider config
		geminiConfig := analyzer.NewGeminiConfig().
			WithAPIKey(options.GeminiAPIKey).
			WithLogger(options.Logger)

		// Set custom model name if provided
		if options.GeminiModel != "" {
			geminiConfig = geminiConfig.WithModelName(options.GeminiModel)
		}

		embeddingProvider, err = analyzer.NewGeminiEmbeddingProvider(ctx, geminiConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Gemini embedding provider: %w", err)
		}

		options.Logger.Info("Using Gemini API for embeddings", "model", geminiConfig.ModelName)

	case "llamacpp":
		if options.LlamaCppModel == "" {
			return nil, fmt.Errorf("llamacpp model name is required when using LlamaCpp embeddings")
		}

		// Create LlamaCpp embedding provider
		llamaCppConfig := analyzer.NewLlamaCppConfig().
			WithLogger(options.Logger).
			WithModelName(options.LlamaCppModel)

		embeddingProvider, err = analyzer.NewLlamaCppEmbeddingProvider(llamaCppConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create LlamaCpp embedding provider: %w", err)
		}

		options.Logger.Info("Using LlamaCpp for embeddings", "model", llamaCppConfig.ModelName)

	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", options.Provider)
	}

	return embeddingProvider, nil
}

// CloseEmbeddingProvider attempts to close the embedding provider if it implements Close
func CloseEmbeddingProvider(provider analyzer.EmbeddingProvider, logger *log.Logger) {
	if closer, ok := provider.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			logger.Warn("Failed to close embedding provider", "error", err)
		}
	}
}
