package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/charmbracelet/log"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// EmbeddingProvider is an interface for generating embeddings from text
type EmbeddingProvider interface {
	// GenerateEmbedding generates a vector embedding for the given text
	GenerateEmbedding(ctx context.Context, text string) ([]float32, error)

	// GetEmbeddingModelName returns the name of the embedding model (e.g. "gemini-embedding-exp-03-07")
	GetEmbeddingModelName() string
}

// LlamaCppConfig holds configuration for the llama.cpp embedding server
type LlamaCppConfig struct {
	// URL is the endpoint for the embedding service
	URL string
	// Timeout is the maximum time to wait for a response
	Timeout time.Duration
	// RetryAttempts is the number of times to retry failed requests
	RetryAttempts uint
	// ModelName is the name of the specific model used by llama.cpp (e.g., "snowflake-arctic-embed-l-v2.0-f16")
	ModelName string
	// Logger is used for logging embedding operations
	Logger *log.Logger
}

// NewLlamaCppConfig creates a new configuration with default values
func NewLlamaCppConfig() LlamaCppConfig {
	return LlamaCppConfig{
		URL:           "http://localhost:8080",
		Timeout:       10 * time.Second,
		RetryAttempts: 3,
	}
}

// WithURL sets the URL for the embedding service
func (c LlamaCppConfig) WithURL(url string) LlamaCppConfig {
	c.URL = url
	return c
}

// WithTimeout sets the timeout for embedding requests
func (c LlamaCppConfig) WithTimeout(timeout time.Duration) LlamaCppConfig {
	c.Timeout = timeout
	return c
}

// WithRetryAttempts sets the number of retry attempts
func (c LlamaCppConfig) WithRetryAttempts(attempts uint) LlamaCppConfig {
	c.RetryAttempts = attempts
	return c
}

// WithModelName sets the model name for the llama.cpp server
func (c LlamaCppConfig) WithModelName(modelName string) LlamaCppConfig {
	c.ModelName = modelName
	return c
}

// WithLogger sets the logger for embedding operations
func (c LlamaCppConfig) WithLogger(logger *log.Logger) LlamaCppConfig {
	c.Logger = logger
	return c
}

// Validate checks if the configuration is valid
func (c LlamaCppConfig) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("embedding service URL is required")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("timeout must be greater than 0")
	}
	if c.RetryAttempts == 0 {
		return fmt.Errorf("retry attempts must be greater than 0")
	}
	if c.ModelName == "" {
		return fmt.Errorf("model name is required")
	}
	if c.Logger == nil {
		return fmt.Errorf("logger is required")
	}
	return nil
}

// LlamaCppEmbeddingProvider implements EmbeddingProvider for llama.cpp server
type LlamaCppEmbeddingProvider struct {
	config     LlamaCppConfig
	httpClient *http.Client
	logger     *log.Logger
}

type llamaCppEmbeddingRequest struct {
	Content string `json:"content"`
}

type llamaCppEmbeddingResponse []struct {
	Index     int         `json:"index"`
	Embedding [][]float32 `json:"embedding"`
}

// NewLlamaCppEmbeddingProvider creates a new llama.cpp server embedding provider
func NewLlamaCppEmbeddingProvider(config LlamaCppConfig) (*LlamaCppEmbeddingProvider, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	return &LlamaCppEmbeddingProvider{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		logger: config.Logger,
	}, nil
}

// GenerateEmbedding generates a vector embedding for the given text using llama.cpp server
func (p *LlamaCppEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	// Create request body
	reqBody := llamaCppEmbeddingRequest{
		Content: text,
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Parse base URL
	baseURL, err := url.Parse(p.config.URL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}

	// Join with path
	embedURL := baseURL.JoinPath("embedding")

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", embedURL.String(), bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Retry logic for the HTTP request
	var embeddings llamaCppEmbeddingResponse
	err = retry.Do(
		func() error {
			// Make request
			resp, err := p.httpClient.Do(req)
			if err != nil {
				return fmt.Errorf("failed to make request: %w", err)
			}
			defer resp.Body.Close()

			// Read response body
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("failed to read response: %w", err)
			}

			// Check status code
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("embedding server returned status %d: %s", resp.StatusCode, body)
			}

			// Parse response
			if err := json.Unmarshal(body, &embeddings); err != nil {
				p.logger.Debug("Failed to unmarshal embedding response",
					"body", string(body),
					"error", err)
				return fmt.Errorf("failed to unmarshal response: %w", err)
			}

			if len(embeddings) == 0 {
				return fmt.Errorf("no embeddings returned from server")
			}

			if len(embeddings[0].Embedding) == 0 || len(embeddings[0].Embedding[0]) == 0 {
				return fmt.Errorf("empty embedding returned from server")
			}

			return nil
		},
		retry.Context(ctx),
		retry.Attempts(p.config.RetryAttempts),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			p.logger.Warn("Retrying embedding request",
				"attempt", n+1,
				"max_attempts", p.config.RetryAttempts,
				"error", err)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get embedding: %w", err)
	}

	// We only send one text, so we only get one embedding back
	// The embedding is returned as a nested array, so we take the first (and only) inner array
	embedding := embeddings[0].Embedding[0]

	p.logger.Debug("Generated embedding",
		"text_length", len(text),
		"embedding_length", len(embedding),
		"embedding_index", embeddings[0].Index)

	return embedding, nil
}

// GetEmbeddingModelName returns the name of the embedding model
func (p *LlamaCppEmbeddingProvider) GetEmbeddingModelName() string {
	return p.config.ModelName
}

// GeminiConfig holds configuration for the Gemini embedding service
type GeminiConfig struct {
	// APIKey is the Gemini API key
	APIKey string
	// ModelName is the name of the Gemini embedding model to use
	ModelName string
	// RetryAttempts is the number of times to retry failed requests
	RetryAttempts uint
	// Logger is used for logging embedding operations
	Logger *log.Logger
}

// NewGeminiConfig creates a new configuration with default values
func NewGeminiConfig() GeminiConfig {
	return GeminiConfig{
		RetryAttempts: 3,
	}
}

// WithAPIKey sets the Gemini API key
func (c GeminiConfig) WithAPIKey(apiKey string) GeminiConfig {
	c.APIKey = apiKey
	return c
}

// WithModelName sets the Gemini model name
func (c GeminiConfig) WithModelName(modelName string) GeminiConfig {
	c.ModelName = modelName
	return c
}

// WithRetryAttempts sets the number of retry attempts
func (c GeminiConfig) WithRetryAttempts(attempts uint) GeminiConfig {
	c.RetryAttempts = attempts
	return c
}

// WithLogger sets the logger for embedding operations
func (c GeminiConfig) WithLogger(logger *log.Logger) GeminiConfig {
	c.Logger = logger
	return c
}

// Validate checks if the configuration is valid
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

// GeminiEmbeddingProvider implements EmbeddingProvider using Google's Gemini API
type GeminiEmbeddingProvider struct {
	config GeminiConfig
	client *genai.Client
	model  *genai.EmbeddingModel
	logger *log.Logger
}

// NewGeminiEmbeddingProvider creates a new Gemini embedding provider
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

// GenerateEmbedding generates a vector embedding for the given text using Gemini API
func (p *GeminiEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	var embedding []float32
	var err error

	start := time.Now()

	// Retry logic for the embedding request
	err = retry.Do(
		func() error {
			// Request embedding from Gemini API
			result, err := p.model.EmbedContent(ctx, genai.Text(text))
			if err != nil {
				return fmt.Errorf("failed to generate embedding: %w", err)
			}

			if result == nil || result.Embedding == nil {
				return fmt.Errorf("no embedding returned from Gemini API")
			}

			// Get the embedding values
			embedding = result.Embedding.Values

			return nil
		},
		retry.Context(ctx),
		retry.Attempts(p.config.RetryAttempts),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			p.logger.Warn("Retrying Gemini embedding request",
				"attempt", n+1,
				"max_attempts", p.config.RetryAttempts,
				"error", err)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("failed to get Gemini embedding: %w", err)
	}

	p.logger.Debug("Generated Gemini embedding",
		"text_length", len(text),
		"embedding_length", len(embedding),
		"model", p.config.ModelName,
		"duration", time.Since(start))

	return embedding, nil
}

// Close closes the Gemini client
func (p *GeminiEmbeddingProvider) Close() error {
	if p.client != nil {
		return p.client.Close()
	}
	return nil
}

// GetEmbeddingModelName returns the name of the embedding model
func (p *GeminiEmbeddingProvider) GetEmbeddingModelName() string {
	return p.config.ModelName
}
