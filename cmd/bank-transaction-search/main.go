package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

type CLI struct {
	commands.CommonConfig
	commands.EmbeddingConfig

	Query     string  `help:"Search query - what you're looking for" required:""`
	Days      int     `help:"Number of days to look back" default:"30"`
	Limit     int     `help:"Maximum number of results to return" default:"100"`
	Method    string  `help:"Search method to use" default:"hybrid" enum:"text,vector,hybrid"`
	Threshold float32 `help:"Minimum similarity score for search results (0.0-1.0)" default:"0.4"`
}

func (c *CLI) Run() error {
	// Setup basic components
	ctx := context.Background()
	logger, _, database, err := c.setupCommonComponents()
	if err != nil {
		return err
	}
	defer database.Close()

	// Handle search based on method
	switch c.Method {
	case "text":
		return c.performTextSearch(ctx, database, logger)
	case "vector":
		// Initialize vector search components (needed for both vector and hybrid search)
		embeddingProvider, _, txAnalyzer, err := c.setupVectorComponents(ctx, logger, database)
		if err != nil {
			return err
		}
		defer commands.CloseEmbeddingProvider(embeddingProvider, logger)

		return c.performVectorSearch(ctx, txAnalyzer, logger)
	case "hybrid":
		// Initialize vector search components (needed for both vector and hybrid search)
		embeddingProvider, _, txAnalyzer, err := c.setupVectorComponents(ctx, logger, database)
		if err != nil {
			return err
		}
		defer commands.CloseEmbeddingProvider(embeddingProvider, logger)

		return c.performHybridSearch(ctx, txAnalyzer, logger)
	default:
		// This should never happen due to enum validation, but just in case
		return fmt.Errorf("invalid search method: %s", c.Method)
	}
}

// setupCommonComponents initializes logger, timezone, and database
func (c *CLI) setupCommonComponents() (*log.Logger, *time.Location, *db.DB, error) {
	// Setup logger
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

	return logger, loc, database, nil
}

// setupVectorComponents initializes the embedding provider, vector storage, and analyzer
func (c *CLI) setupVectorComponents(ctx context.Context, logger *log.Logger, database *db.DB) (analyzer.EmbeddingProvider, analyzer.VectorStorage, *analyzer.Analyzer, error) {
	// Initialize embedding provider
	embeddingOptions := commands.EmbeddingOptions{
		Provider:      c.Provider,
		LlamaCppModel: c.LlamaCppModel,
		GeminiAPIKey:  c.GeminiAPIKey,
		Logger:        logger,
	}

	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, embeddingOptions)
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
		return nil, nil, nil, err
	}

	// Initialize vector storage
	vectorStorage, err := commands.SetupVectorStorage(ctx, c.DataDir, embeddingProvider, logger)
	if err != nil {
		logger.Fatal("Failed to initialize vector storage", "error", err)
		return embeddingProvider, nil, nil, err
	}

	// Initialize analyzer
	txAnalyzer := analyzer.NewAnalyzer(nil, logger, database, embeddingProvider, vectorStorage)

	return embeddingProvider, vectorStorage, txAnalyzer, nil
}

// performTextSearch performs a full-text search and displays results
func (c *CLI) performTextSearch(ctx context.Context, database *db.DB, logger *log.Logger) error {
	searchResults, err := database.SearchTransactionsByText(ctx, c.Query, c.Days, c.Limit)
	if err != nil {
		logger.Fatal("Failed to search transactions", "error", err)
	}

	// Print results
	if len(searchResults) == 0 {
		fmt.Println("No transactions found")
		return nil
	}

	fmt.Printf("Found %d transactions:\n\n", len(searchResults))
	for _, result := range searchResults {
		t := result.TransactionWithDetails
		fmt.Printf("%s: %s - %s (text score: %.2f)\n", t.Date, t.Amount, t.Payee, result.Scores.TextScore)
		printTransactionDetails(t)
	}

	return nil
}

// performVectorSearch performs a vector search and displays results
func (c *CLI) performVectorSearch(ctx context.Context, txAnalyzer *analyzer.Analyzer, logger *log.Logger) error {
	searchResults, err := txAnalyzer.VectorSearch(ctx, c.Query, c.Limit, c.Threshold, c.Days)
	if err != nil {
		logger.Fatal("Failed to perform vector search", "error", err)
	}

	// Print results
	if len(searchResults) == 0 {
		fmt.Println("No transactions found")
		return nil
	}

	fmt.Printf("Found %d transactions:\n\n", len(searchResults))

	for _, result := range searchResults {
		t := result.TransactionWithDetails
		fmt.Printf("%s: %s - %s (similarity: %.2f)\n", t.Date, t.Amount, t.Payee, result.Scores.VectorScore)
		printTransactionDetails(t)
	}

	return nil
}

// performHybridSearch performs a hybrid search and displays results
func (c *CLI) performHybridSearch(ctx context.Context, txAnalyzer *analyzer.Analyzer, logger *log.Logger) error {
	searchResults, err := txAnalyzer.HybridSearch(ctx, c.Query, c.Days, c.Limit, c.Threshold)
	if err != nil {
		logger.Fatal("Failed to perform hybrid search", "error", err)
	}

	// Print results
	if len(searchResults) == 0 {
		fmt.Println("No transactions found")
		return nil
	}

	fmt.Printf("Found %d transactions:\n\n", len(searchResults))

	for _, result := range searchResults {
		t := result.TransactionWithDetails
		fmt.Printf("%s: %s - %s (score: %.4f)\n", t.Date, t.Amount, t.Payee, result.Scores.RRFScore)

		// Show individual scores if they exist
		var scores []string
		if result.Scores.TextScore != 0 {
			scores = append(scores, fmt.Sprintf("text: %.2f", result.Scores.TextScore))
		}
		if result.Scores.VectorScore != 0 {
			scores = append(scores, fmt.Sprintf("vector: %.2f", result.Scores.VectorScore))
		}
		if len(scores) > 0 {
			fmt.Printf("  Scores: %s\n", strings.Join(scores, ", "))
		}

		printTransactionDetails(t)
	}

	return nil
}

// printTransactionDetails prints the details of a transaction
func printTransactionDetails(t types.TransactionWithDetails) {
	fmt.Printf("  Type: %s\n", t.Details.Type)
	if t.Details.Merchant != "" {
		fmt.Printf("  Merchant: %s\n", t.Details.Merchant)
	}
	if t.Details.Location != "" {
		fmt.Printf("  Location: %s\n", t.Details.Location)
	}
	if t.Details.Category != "" {
		fmt.Printf("  Category: %s\n", t.Details.Category)
	}
	if t.Details.Description != "" {
		fmt.Printf("  Description: %s\n", t.Details.Description)
	}
	if t.Details.CardNumber != "" {
		fmt.Printf("  Card Number: %s\n", t.Details.CardNumber)
	}
	if t.Details.ForeignAmount != nil {
		fmt.Printf("  Foreign Amount: %s %s\n", t.Details.ForeignAmount.Amount, t.Details.ForeignAmount.Currency)
	}
	if t.Details.TransferDetails != nil {
		if t.Details.TransferDetails.ToAccount != "" {
			fmt.Printf("  To Account: %s\n", t.Details.TransferDetails.ToAccount)
		}
		if t.Details.TransferDetails.FromAccount != "" {
			fmt.Printf("  From Account: %s\n", t.Details.TransferDetails.FromAccount)
		}
		if t.Details.TransferDetails.Reference != "" {
			fmt.Printf("  Reference: %s\n", t.Details.TransferDetails.Reference)
		}
	}
	fmt.Println()
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("bank-transaction-search"),
		kong.Description("Search for transactions in your bank transaction history"),
		kong.UsageOnError(),
	)

	err := ctx.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
