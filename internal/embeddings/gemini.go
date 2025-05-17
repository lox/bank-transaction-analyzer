package embeddings

import (
	"context"
	"fmt"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/charmbracelet/log"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// GeminiConfig holds configuration for the Gemini embedding service
type GeminiConfig struct {
	APIKey        string
	ModelName     string
	RetryAttempts uint
	Logger        *log.Logger
}

func NewGeminiConfig() GeminiConfig {
	return GeminiConfig{
		RetryAttempts: 3,
	}
}

func (c GeminiConfig) WithAPIKey(apiKey string) GeminiConfig {
	c.APIKey = apiKey
	return c
}
func (c GeminiConfig) WithModelName(modelName string) GeminiConfig {
	c.ModelName = modelName
	return c
}
func (c GeminiConfig) WithRetryAttempts(attempts uint) GeminiConfig {
	c.RetryAttempts = attempts
	return c
}
func (c GeminiConfig) WithLogger(logger *log.Logger) GeminiConfig {
	c.Logger = logger
	return c
}

func (c GeminiConfig) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("gemini api key is required")
	}
	if c.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	if c.RetryAttempts == 0 {
		return fmt.Errorf("retry attempts must be greater than 0")
	}
	if c.Logger == nil {
		return fmt.Errorf("logger is required")
	}
	return nil
}

type GeminiEmbeddingProvider struct {
	config GeminiConfig
	client *genai.Client
	model  *genai.EmbeddingModel
	logger *log.Logger
}

func NewGeminiEmbeddingProvider(ctx context.Context, config GeminiConfig) (*GeminiEmbeddingProvider, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	client, err := genai.NewClient(ctx, option.WithAPIKey(config.APIKey))
	if err != nil {
		return nil, fmt.Errorf("failed to create Gemini client: %w", err)
	}
	return &GeminiEmbeddingProvider{
		config: config,
		client: client,
		model:  client.EmbeddingModel(config.ModelName),
		logger: config.Logger,
	}, nil
}

func (p *GeminiEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	var embedding []float32
	var err error
	start := time.Now()
	err = retry.Do(
		func() error {
			result, err := p.model.EmbedContent(ctx, genai.Text(text))
			if err != nil {
				return fmt.Errorf("failed to generate embedding: %w", err)
			}
			if result == nil || result.Embedding == nil {
				return fmt.Errorf("no embedding returned from Gemini API")
			}
			embedding = result.Embedding.Values
			return nil
		},
		retry.Context(ctx),
		retry.Attempts(p.config.RetryAttempts),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			p.logger.Warn("Retrying Gemini embedding request", "attempt", n+1, "max_attempts", p.config.RetryAttempts, "error", err)
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get Gemini embedding: %w", err)
	}
	p.logger.Debug("Generated Gemini embedding", "text_length", len(text), "embedding_length", len(embedding), "model", p.config.ModelName, "duration", time.Since(start))
	return embedding, nil
}

func (p *GeminiEmbeddingProvider) Close() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

func (p *GeminiEmbeddingProvider) GetEmbeddingModelName() string {
	return p.config.ModelName
}
