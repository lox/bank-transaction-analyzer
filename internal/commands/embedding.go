package commands

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
)

// SetupEmbeddingProvider initializes and returns an embedding provider based on the config
func SetupEmbeddingProvider(ctx context.Context, config EmbeddingConfig, logger *log.Logger) (embeddings.EmbeddingProvider, error) {
	var embeddingProvider embeddings.EmbeddingProvider
	var err error

	switch config.Provider {
	case "gemini":
		if config.GeminiAPIKey == "" {
			return nil, fmt.Errorf("gemini api key is required when using Gemini embeddings")
		}

		// Create Gemini embedding provider config
		geminiConfig := embeddings.NewGeminiConfig().
			WithAPIKey(config.GeminiAPIKey).
			WithLogger(logger)

		// Set custom model name if provided
		if config.GeminiModel != "" {
			geminiConfig = geminiConfig.WithModelName(config.GeminiModel)
		}

		embeddingProvider, err = embeddings.NewGeminiEmbeddingProvider(ctx, geminiConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create Gemini embedding provider: %w", err)
		}

		logger.Info("Using Gemini API for embeddings", "model", geminiConfig.ModelName)

	case "llamacpp":
		if config.LlamaCppModel == "" {
			return nil, fmt.Errorf("llamacpp model name is required when using LlamaCpp embeddings")
		}

		llamaCppConfig := embeddings.NewLlamaCppConfig().
			WithLogger(logger).
			WithModelName(config.LlamaCppModel)
		if config.LlamaCppURL != "" {
			llamaCppConfig = llamaCppConfig.WithURL(config.LlamaCppURL)
		}

		embeddingProvider, err = embeddings.NewLlamaCppEmbeddingProvider(llamaCppConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create LlamaCpp embedding provider: %w", err)
		}

		logger.Info("Using LlamaCpp for embeddings", "model", llamaCppConfig.ModelName, "url", llamaCppConfig.URL)

	case "lmstudio":
		// LMStudio exposes an OpenAI-compatible API, so use the OpenAI provider with the LMStudio endpoint
		embeddingProvider, err = embeddings.NewOpenAIEmbeddingProvider(embeddings.NewOpenAIConfig().
			WithAPIKey("dummy").
			WithModelName(config.LMStudioModel).
			WithLogger(logger).
			WithEndpoint(config.LMStudioEndpoint))
		if err != nil {
			return nil, fmt.Errorf("failed to create LMStudio (OpenAI-compatible) embedding provider: %w", err)
		}
		logger.Info("Using LMStudio (OpenAI-compatible) for embeddings", "model", config.LMStudioModel, "endpoint", config.LMStudioEndpoint)

	case "ollama":
		embeddingProvider, err = embeddings.NewOpenAIEmbeddingProvider(embeddings.NewOpenAIConfig().
			WithAPIKey("dummy").
			WithModelName(config.OllamaModel).
			WithLogger(logger).
			WithEndpoint(config.OllamaEndpoint))
		if err != nil {
			return nil, fmt.Errorf("failed to create Ollama embedding provider: %w", err)
		}
		logger.Info("Using Ollama for embeddings", "model", config.OllamaModel, "endpoint", config.OllamaEndpoint)

	case "openai":
		if config.OpenAIAPIKey == "" {
			return nil, fmt.Errorf("openai api key is required when using OpenAI embeddings")
		}
		openaiConfig := embeddings.NewOpenAIConfig().
			WithAPIKey(config.OpenAIAPIKey).
			WithModelName(config.OpenAIModel).
			WithLogger(logger)
		if config.OpenAIEndpoint != "" {
			openaiConfig = openaiConfig.WithEndpoint(config.OpenAIEndpoint)
		}
		embeddingProvider, err = embeddings.NewOpenAIEmbeddingProvider(openaiConfig)
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenAI embedding provider: %w", err)
		}
		logger.Info("Using OpenAI-compatible API for embeddings", "model", openaiConfig.ModelName, "endpoint", openaiConfig.Endpoint)

	default:
		return nil, fmt.Errorf("unknown embedding provider: %s", config.Provider)
	}

	return embeddingProvider, nil
}

// CloseEmbeddingProvider attempts to close the embedding provider if it implements Close
func CloseEmbeddingProvider(provider embeddings.EmbeddingProvider, logger *log.Logger) {
	if closer, ok := provider.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			logger.Warn("Failed to close embedding provider", "error", err)
		}
	}
}
