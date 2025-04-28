package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-parser/internal/analyzer"
	"github.com/lox/ing-transaction-parser/internal/qif"
	"github.com/sashabaranov/go-openai"
)

type CLI struct {
	File           string `arg:"" help:"QIF file to analyze"`
	APIKey         string `env:"OPENAI_API_KEY" help:"OpenAI API key"`
	EmbeddingModel string `env:"OPENAI_EMBEDDING_MODEL" default:"text-embedding-3-small" help:"OpenAI embedding model to use"`
	AnalysisModel  string `env:"OPENAI_ANALYSIS_MODEL" default:"gpt-4.1-nano" help:"OpenAI model to use for analysis"`
	DataDir        string `env:"DATA_DIR" default:"data" help:"Directory to store data"`
	Verbose        bool   `short:"v" help:"Enable verbose output"`
	Concurrency    int    `env:"CONCURRENCY" default:"5" help:"Number of concurrent embedding requests"`
}

func main() {
	var cli CLI
	kong.Parse(&cli)

	// Create logger
	logger := log.New(os.Stderr)
	if cli.Verbose {
		logger.SetLevel(log.DebugLevel)
	}

	if cli.APIKey == "" {
		logger.Fatal("OPENAI_API_KEY environment variable is required")
	}

	// Parse QIF file
	transactions, err := qif.ParseFile(cli.File)
	if err != nil {
		logger.Fatal("Error parsing QIF file", "error", err)
	}

	if cli.Verbose {
		logger.Debug("Found transactions", "count", len(transactions))
		logger.Debug("Using embedding model", "model", cli.EmbeddingModel)
		logger.Debug("Using analysis model", "model", cli.AnalysisModel)
	}

	// Initialize OpenAI client
	client := openai.NewClient(cli.APIKey)

	// Create a context that can be cancelled
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		logger.Info("Received signal, shutting down gracefully", "signal", sig)
		cancel()
	}()

	// Initialize embedding store
	store, err := analyzer.NewEmbeddingStore(cli.DataDir, logger)
	if err != nil {
		logger.Fatal("Failed to initialize embedding store", "error", err)
	}
	defer store.Close()

	// Count how many embeddings we need to generate
	missing, err := store.CountMissingEmbeddings(ctx, transactions)
	if err != nil {
		logger.Fatal("Failed to count missing embeddings", "error", err)
	}

	// Create appropriate progress tracker
	var progress analyzer.Progress
	if cli.Verbose {
		progress = analyzer.NewNoopProgress()
	} else {
		progress = analyzer.NewBarProgress(missing)
	}

	// Analyze transactions
	analysis, err := analyzer.AnalyzeTransactions(ctx, client, transactions, progress, logger, store, analyzer.AnalysisConfig{
		EmbeddingModel: cli.EmbeddingModel,
		AnalysisModel:  cli.AnalysisModel,
		Concurrency:    cli.Concurrency,
	})
	if err != nil {
		if err == context.Canceled {
			logger.Info("Analysis cancelled by user")
			return
		}
		logger.Fatalf("Error analyzing transactions: %v", err)
	}

	logger.Info("\nAnalysis Results:")
	logger.Info(analysis)
}
