package commands

// EmbeddingConfig contains common flag definitions for embedding configuration
type EmbeddingConfig struct {
	// Provider is the embedding provider to use
	Provider string `help:"Embedding provider to use" default:"ollama" enum:"llamacpp,gemini,openai,lmstudio,ollama" env:"EMBEDDING_PROVIDER"`
	// LlamaCppModel is the specific LLaMA.cpp embedding model name
	LlamaCppModel string `help:"LLaMA.cpp embedding model name" env:"LLAMACPP_EMBEDDING_MODEL"`
	// LlamaCppURL is the URL for LLaMA.cpp or LMStudio
	LlamaCppURL string `help:"LLaMA.cpp or LMStudio server URL" env:"LLAMACPP_EMBEDDING_URL"`
	// GeminiAPIKey is the API key for Gemini
	GeminiAPIKey string `help:"Google Gemini API key" env:"GEMINI_API_KEY"`
	// GeminiModel is the Google Gemini model name
	GeminiModel string `help:"Google Gemini model name" env:"GEMINI_EMBEDDING_MODEL"`
	// OpenAIAPIKey is the API key for OpenAI
	OpenAIAPIKey string `help:"OpenAI API key" env:"OPENAI_API_KEY"`
	// OpenAIModel is the OpenAI model name
	OpenAIModel string `help:"OpenAI model name" env:"OPENAI_EMBEDDING_MODEL"`
	// OpenAIEndpoint is the OpenAI API endpoint
	OpenAIEndpoint string `help:"OpenAI API endpoint" env:"OPENAI_EMBEDDING_ENDPOINT"`
	// LMStudioModel is the LMStudio model name
	LMStudioModel string `help:"LMStudio model name" env:"LMSTUDIO_EMBEDDING_MODEL"`
	// LMStudioEndpoint is the LMStudio API endpoint
	LMStudioEndpoint string `help:"LMStudio API endpoint" env:"LMSTUDIO_EMBEDDING_ENDPOINT" default:"http://localhost:1234/v1"`
	// OllamaModel is the Ollama model name
	OllamaModel string `help:"Ollama model name" env:"OLLAMA_EMBEDDING_MODEL"`
	// OllamaEndpoint is the Ollama API endpoint
	OllamaEndpoint string `help:"Ollama API endpoint" env:"OLLAMA_EMBEDDING_ENDPOINT" default:"http://localhost:11434/v1"`
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
