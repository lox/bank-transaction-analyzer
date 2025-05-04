package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/bank/ing"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	openrouter "github.com/revrost/go-openrouter"
)

type CLI struct {
	commands.CommonConfig
	commands.EmbeddingConfig

	OpenRouterKey    string `help:"OpenRouter API key" env:"OPENROUTER_API_KEY" required:""`
	OpenRouterModel  string `help:"OpenRouter model to use for analysis" default:"google/gemini-2.5-flash-preview" env:"OPENROUTER_MODEL"`
	Concurrency      int    `help:"Number of concurrent operations to process" default:"10"`
	NoProgress       bool   `help:"Disable progress bar" default:"false"`
	Bank             string `help:"Bank to use for processing" default:"ing-australia" enum:"ing-australia"`
	QIFFile          string `help:"Path to QIF file to process" required:""`
	UpdateEmbeddings bool   `help:"Update embeddings for all transactions after processing" default:"false"`
}

func (c *CLI) Run() error {
	logger := log.New(os.Stderr)

	// Set log level
	level, err := log.ParseLevel(c.LogLevel)
	if err != nil {
		logger.Fatal("Invalid log level", "error", err)
	}
	logger.SetLevel(level)

	// Load timezone
	loc, err := time.LoadLocation(c.Timezone)
	if err != nil {
		logger.Fatal("Failed to load timezone", "error", err)
	}

	// Initialize database
	database, err := db.New(c.DataDir, logger, loc)
	if err != nil {
		logger.Fatal("Failed to initialize database", "error", err)
	}
	defer database.Close()

	// Create context with timeout for operations
	processCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Initialize OpenRouter client for transaction analysis
	client := openrouter.NewClient(
		c.OpenRouterKey,
		openrouter.WithXTitle("Bank Transaction Analyzer"),
		openrouter.WithHTTPReferer("https://github.com/lox/bank-transaction-analyzer"),
	)

	// Initialize bank registry
	registry := bank.NewRegistry()
	registry.Register(ing.New())

	// Get bank implementation
	bankImpl, ok := registry.Get(c.Bank)
	if !ok {
		logger.Fatal("Unknown bank", "bank", c.Bank, "available", registry.List())
	}

	// Open QIF file
	file, err := os.Open(c.QIFFile)
	if err != nil {
		logger.Fatal("Failed to open QIF file", "error", err)
	}
	defer file.Close()

	// Parse transactions
	transactions, err := bankImpl.ParseTransactions(context.Background(), file)
	if err != nil {
		logger.Fatal("Failed to parse transactions", "error", err)
	}

	// Initialize embedding provider and vector storage
	an, err := initAnalyzer(processCtx, c, client, database, logger)
	if err != nil {
		return err
	}

	// Process transactions
	analyzedTransactions, err := bankImpl.ProcessTransactions(processCtx, transactions, an, analyzer.Config{
		OpenRouterModel: c.OpenRouterModel,
		Concurrency:     c.Concurrency,
		Progress:        !c.NoProgress,
	})
	if err != nil {
		logger.Fatal("Failed to process transactions", "error", err)
	}

	logger.Info("Transactions processed successfully", "count", len(analyzedTransactions))

	// Update embeddings if requested
	if c.UpdateEmbeddings {
		transactionsNeedingUpdate := make([]types.TransactionWithDetails, 0)
		for _, t := range analyzedTransactions {
			hasEmbedding, err := an.HasTransactionEmbedding(processCtx, &t)
			if err != nil {
				logger.Fatal("Failed to check if transaction embedding exists", "error", err)
				return err
			}
			if !hasEmbedding {
				transactionsNeedingUpdate = append(transactionsNeedingUpdate, t)
			}
		}
		logger.Info("Updating embeddings for transactions without embeddings", "count", len(transactionsNeedingUpdate))
		err = an.UpdateEmbeddings(processCtx, transactionsNeedingUpdate, analyzer.Config{
			Concurrency: c.Concurrency,
			Progress:    !c.NoProgress,
		})
		if err != nil {
			logger.Fatal("Failed to update embeddings", "error", err)
			return err
		}

		logger.Info("Embeddings updated successfully")
	}

	return nil
}

// Initialize the analyzer with the embedding provider and vector storage
func initAnalyzer(ctx context.Context, config *CLI, client *openrouter.Client, database *db.DB, logger *log.Logger) (*analyzer.Analyzer, error) {
	// Initialize embedding provider using the common setup
	embeddingOptions := commands.EmbeddingOptions{
		Provider:      config.Provider,
		LlamaCppModel: config.LlamaCppModel,
		GeminiAPIKey:  config.GeminiAPIKey,
		// For Gemini, we can use the OpenRouterModel as the GeminiModel if desired
		Logger: logger,
	}

	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, embeddingOptions)
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
		return nil, err
	}

	// Initialize vector storage
	vectorStorage, err := commands.SetupVectorStorage(ctx, config.DataDir, embeddingProvider, logger)
	if err != nil {
		logger.Fatal("Failed to create vector storage", "error", err)
		return nil, err
	}

	// Create analyzer with all the required dependencies
	return analyzer.NewAnalyzer(client, logger, database, embeddingProvider, vectorStorage), nil
}

func main() {
	// Parse CLI commands
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("bank-transaction-analyzer"),
		kong.Description("A tool to analyze bank transactions"),
		kong.UsageOnError(),
	)

	// Run the selected command
	err := ctx.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
