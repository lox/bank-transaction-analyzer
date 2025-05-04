package commands

// EmbeddingConfig contains common flag definitions for embedding configuration
type EmbeddingConfig struct {
	// Provider is the embedding provider to use
	Provider string `help:"Embedding provider to use" default:"llamacpp" enum:"llamacpp,gemini" env:"EMBEDDING_PROVIDER"`
	// LlamaCppModel is the specific LLaMA.cpp embedding model name
	LlamaCppModel string `help:"Specific LLaMA.cpp embedding model name" env:"LLAMACPP_EMBEDDING_MODEL"`
	// GeminiAPIKey is the API key for Gemini
	GeminiAPIKey string `help:"Google Gemini API key" env:"GEMINI_API_KEY"`
}

// CommonConfig contains configuration common to all commands
type CommonConfig struct {
	// DataDir is the path to the data directory
	DataDir string `help:"Path to data directory" default:"./data"`
	// Timezone is the timezone to use for transaction dates
	Timezone string `help:"Timezone to use for transaction dates" required:"" default:"Australia/Melbourne"`
	// LogLevel is the logging level to use
	LogLevel string `help:"Log level (debug, info, warn, error)" default:"warn" enum:"debug,info,warn,error"`
}
