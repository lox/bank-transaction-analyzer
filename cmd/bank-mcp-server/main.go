package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/bank/amex"
	"github.com/lox/bank-transaction-analyzer/internal/bank/ing"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/mcp"
)

func getEnv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func main() {
	// Setup logger
	logger := log.New(io.Discard)
	logger.SetLevel(log.DebugLevel)

	// Create a file for logging
	logFile, err := os.OpenFile("bank-mcp-server.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		panic(fmt.Sprintf("Failed to open log file: %v", err))
	}
	defer logFile.Close()

	// Update logger to use the multi-writer
	logger = log.New(logFile)
	logger.SetLevel(log.InfoLevel)

	// Defaults
	var tz = getEnv("TZ", "Australia/Melbourne")
	var dataDir = getEnv("DATA_DIR", "./data")

	// Load timezone
	loc, err := time.LoadLocation(tz)
	if err != nil {
		logger.Fatal("Failed to load timezone", "error", err)
	}

	logger.Info("Loading database", "data_dir", dataDir, "timezone", tz)

	// Initialize database
	database, err := db.New(dataDir, logger, loc)
	if err != nil {
		logger.Fatal("Failed to initialize database", "error", err)
	}

	// Initialize embedding provider
	embeddingProvider, err := commands.SetupEmbeddingProvider(context.Background(), commands.EmbeddingOptions{
		Provider:      getEnv("EMBEDDING_PROVIDER", "llamacpp"),
		LlamaCppModel: getEnv("LLAMACPP_EMBEDDING_MODEL", "snowflake-arctic-embed-l-v2.0-f16"),
		Logger:        logger,
	})
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
	}

	// Initialize vector storage
	vectorStorage, err := commands.SetupVectorStorage(context.Background(), dataDir, embeddingProvider, logger)
	if err != nil {
		logger.Fatal("Failed to initialize vector storage", "error", err)
	}

	// Initialize analyzer
	txAnalyzer := analyzer.NewAnalyzer(nil, logger, database, embeddingProvider, vectorStorage)

	// Initialize bank registry and register banks
	bankRegistry := bank.NewRegistry()
	bankRegistry.Register(ing.New())
	bankRegistry.Register(amex.New())

	logger.Info("Starting MCP server")
	s := mcp.New(database, txAnalyzer, logger, bankRegistry.List())
	if err := s.Run(); err != nil {
		panic(err)
	}
}
