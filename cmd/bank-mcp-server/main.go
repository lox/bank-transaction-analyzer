package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/bank/amex"
	"github.com/lox/bank-transaction-analyzer/internal/bank/ing"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/mcp"
)

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

	type CLI struct {
		commands.EmbeddingConfig
		commands.CommonConfig
	}

	var cli CLI
	_ = kong.Parse(&cli,
		kong.Name("bank-mcp-server"),
		kong.Description("Run the MCP server for bank transaction analysis"),
		kong.UsageOnError(),
	)

	// Defaults
	var tz = cli.Timezone
	var dataDir = cli.DataDir

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

	// Initialize embedding provider using Kong-parsed CLI values
	embeddingProvider, err := commands.SetupEmbeddingProvider(context.Background(), cli.EmbeddingConfig, logger)
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
