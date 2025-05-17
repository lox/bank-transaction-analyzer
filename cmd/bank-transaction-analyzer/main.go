package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/agent"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/bank/amex"
	"github.com/lox/bank-transaction-analyzer/internal/bank/ing"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

type CLI struct {
	commands.CommonConfig
	commands.EmbeddingConfig

	OpenRouterKey   string `help:"OpenRouter API key" env:"OPENROUTER_API_KEY" required:""`
	OpenRouterModel string `help:"OpenRouter model to use for analysis" default:"google/gemini-2.5-flash-preview" env:"OPENROUTER_MODEL"`
	Concurrency     int    `help:"Number of concurrent operations to process" default:"10"`
	NoProgress      bool   `help:"Disable progress bar" default:"false"`
	Bank            string `help:"Bank to use for processing" default:"ing-australia" enum:"ing-australia,amex"`
	QIFFile         string `help:"Path to QIF file to process" required:""`
	DryRun          bool   `help:"Print parsed transactions and exit (no analysis)" default:"false"`
	Limit           int    `help:"Limit the number of transactions to process (0 = no limit)" default:"0"`
	Print           bool   `help:"Print classified transactions after processing (does not skip analysis/storage)" default:"false"`
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

	// Initialize OpenRouter agent for transaction analysis
	agentInst := agent.NewOpenRouterAgent(logger, c.OpenRouterKey, c.OpenRouterModel, 3)

	// Initialize bank registry
	registry := bank.NewRegistry()
	registry.Register(ing.New())
	registry.Register(amex.New())

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
	an, err := initAnalyzer(processCtx, c, agentInst, database, logger)
	if err != nil {
		return err
	}

	// Process transactions
	analyzedTransactions, err := an.AnalyzeTransactions(processCtx, transactions, analyzer.Config{
		OpenRouterModel: c.OpenRouterModel,
		Concurrency:     c.Concurrency,
		Progress:        !c.NoProgress,
		DryRun:          c.DryRun,
		Limit:           c.Limit,
	}, bankImpl)
	if err != nil {
		logger.Fatal("Failed to process transactions", "error", err)
	}

	if c.DryRun {
		logger.Info("Dry run: displaying analyzed transactions", "count", len(analyzedTransactions))
		printTransactions(analyzedTransactions, c.Limit)
		return nil
	}

	if c.Print {
		logger.Info("Printing classified transactions", "count", len(analyzedTransactions))
		printTransactions(analyzedTransactions, c.Limit)
	}

	logger.Info("Transactions processed successfully", "count", len(analyzedTransactions))

	return nil
}

// Initialize the analyzer with the embedding provider and vector storage
func initAnalyzer(ctx context.Context, config *CLI, agentInst *agent.Agent, database *db.DB, logger *log.Logger) (*analyzer.Analyzer, error) {
	// Initialize embedding provider using the common setup
	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, config.EmbeddingConfig, logger)
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
	return analyzer.NewAnalyzer(agentInst, logger, database, embeddingProvider, vectorStorage), nil
}

// printTransactions prints the analyzed transactions up to the limit (if set)
func printTransactions(transactions []types.TransactionWithDetails, limit int) {
	count := len(transactions)
	if limit > 0 && count > limit {
		count = limit
	}
	for i := 0; i < count; i++ {
		b, err := json.MarshalIndent(transactions[i], "", "  ")
		if err != nil {
			fmt.Printf("Error marshaling transaction: %v\n", err)
			continue
		}
		fmt.Println(string(b))
	}
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
