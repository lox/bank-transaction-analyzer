package embeddings

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	openai "github.com/sashabaranov/go-openai"
)

// EmbeddingProvider is an interface for generating embeddings from text
// (copied from analyzer/embeddings.go for now, will be moved here in refactor)
type EmbeddingProvider interface {
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)
	GetEmbeddingModelName() string
}

// OpenAIConfig holds configuration for the OpenAI embedding service
type OpenAIConfig struct {
	APIKey        string
	Endpoint      string // e.g. https://api.openai.com/v1
	ModelName     string
	Timeout       time.Duration
	RetryAttempts uint
	Logger        *log.Logger
}

func NewOpenAIConfig() OpenAIConfig {
	return OpenAIConfig{
		Endpoint:      "https://api.openai.com/v1",
		Timeout:       10 * time.Second,
		RetryAttempts: 3,
	}
}

func (c OpenAIConfig) WithAPIKey(apiKey string) OpenAIConfig {
	c.APIKey = apiKey
	return c
}
func (c OpenAIConfig) WithEndpoint(endpoint string) OpenAIConfig {
	c.Endpoint = endpoint
	return c
}
func (c OpenAIConfig) WithModelName(modelName string) OpenAIConfig {
	c.ModelName = modelName
	return c
}
func (c OpenAIConfig) WithTimeout(timeout time.Duration) OpenAIConfig {
	c.Timeout = timeout
	return c
}
func (c OpenAIConfig) WithRetryAttempts(attempts uint) OpenAIConfig {
	c.RetryAttempts = attempts
	return c
}
func (c OpenAIConfig) WithLogger(logger *log.Logger) OpenAIConfig {
	c.Logger = logger
	return c
}

func (c OpenAIConfig) Validate() error {
	if c.APIKey == "" {
		return fmt.Errorf("openai api key is required")
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

// OpenAIEmbeddingProvider implements EmbeddingProvider using OpenAI-compatible API
// (including OpenAI, OpenRouter, LMStudio, etc)
type OpenAIEmbeddingProvider struct {
	config OpenAIConfig
	client *openai.Client
	logger *log.Logger
}

func NewOpenAIEmbeddingProvider(config OpenAIConfig) (*OpenAIEmbeddingProvider, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	cfg := openai.DefaultConfig(config.APIKey)
	cfg.BaseURL = config.Endpoint
	client := openai.NewClientWithConfig(cfg)
	return &OpenAIEmbeddingProvider{
		config: config,
		client: client,
		logger: config.Logger,
	}, nil
}

func (p *OpenAIEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	var embedding []float32
	var err error
	for attempt := uint(0); attempt < p.config.RetryAttempts; attempt++ {
		t := time.Now()
		p.logger.Debug("Generating OpenAI embedding", "text", text, "text_length", len(text), "model", p.config.ModelName)
		resp, err := p.client.CreateEmbeddings(ctx, openai.EmbeddingRequest{
			Model: openai.EmbeddingModel(p.config.ModelName),
			Input: []string{text},
		})
		if err == nil && len(resp.Data) > 0 {
			embedding = resp.Data[0].Embedding
			p.logger.Debug("Generated OpenAI embedding", "text_length", len(text), "embedding_length", len(embedding), "duration", time.Since(t))
			return embedding, nil
		}
		p.logger.Warn("OpenAI embedding request failed", "attempt", attempt+1, "error", err)
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("failed to get OpenAI embedding: %w", err)
}

func (p *OpenAIEmbeddingProvider) GetEmbeddingModelName() string {
	return p.config.ModelName
}
