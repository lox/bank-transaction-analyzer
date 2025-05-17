package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/commands"
	"github.com/lox/bank-transaction-analyzer/internal/db"
)

type EmbeddingsCLI struct {
	commands.CommonConfig
	commands.EmbeddingConfig
	Update    UpdateCmd    `cmd:"" help:"Update embeddings for all transactions that are missing or outdated."`
	Test      TestCmd      `cmd:"" help:"Test embedding generation for a given text input."`
	Benchmark BenchmarkCmd `cmd:"" help:"Benchmark embedding generation for a given text input."`
}

type UpdateCmd struct {
	Concurrency int  `help:"Number of concurrent operations to process" default:"10"`
	NoProgress  bool `help:"Disable progress bar" default:"false"`
}

type TestCmd struct {
	Text string `help:"Text to generate embedding for" required:""`
}

type BenchmarkCmd struct {
	Text  string `help:"Text to generate embedding for" required:""`
	Count int    `help:"Number of times to generate the embedding" default:"10"`
}

func (c *UpdateCmd) Run(cli *EmbeddingsCLI) error {
	logger := log.New(os.Stderr)
	level, err := log.ParseLevel(cli.LogLevel)
	if err != nil {
		logger.Fatal("Invalid log level", "error", err)
	}
	logger.SetLevel(level)

	loc, err := time.LoadLocation(cli.Timezone)
	if err != nil {
		logger.Fatal("Failed to load timezone", "error", err)
	}

	database, err := db.New(cli.DataDir, logger, loc)
	if err != nil {
		logger.Fatal("Failed to initialize database", "error", err)
	}
	defer database.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, cli.EmbeddingConfig, logger)
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
		return err
	}
	vectorStorage, err := commands.SetupVectorStorage(ctx, cli.DataDir, embeddingProvider, logger)
	if err != nil {
		logger.Fatal("Failed to create vector storage", "error", err)
		return err
	}
	an := analyzer.NewAnalyzer(nil, logger, database, embeddingProvider, vectorStorage)

	err = an.UpdateAllEmbeddings(ctx, analyzer.Config{
		Concurrency: c.Concurrency,
		Progress:    !c.NoProgress,
	})
	if err != nil {
		logger.Fatal("Failed to update embeddings", "error", err)
		return err
	}
	logger.Info("Embeddings updated successfully")
	return nil
}

func (c *TestCmd) Run(cli *EmbeddingsCLI) error {
	logger := log.New(os.Stderr)
	level, err := log.ParseLevel(cli.LogLevel)
	if err != nil {
		logger.Fatal("Invalid log level", "error", err)
	}
	logger.SetLevel(level)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, cli.EmbeddingConfig, logger)
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
		return err
	}

	embedding, err := embeddingProvider.GenerateEmbedding(ctx, c.Text)
	if err != nil {
		logger.Fatal("Failed to generate embedding", "error", err)
		return err
	}

	fmt.Printf("Embedding for: %q\n", c.Text)
	fmt.Printf("%v\n", embedding)
	return nil
}

func (c *BenchmarkCmd) Run(cli *EmbeddingsCLI) error {
	logger := log.New(os.Stderr)
	level, err := log.ParseLevel(cli.LogLevel)
	if err != nil {
		logger.Fatal("Invalid log level", "error", err)
	}
	logger.SetLevel(level)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(c.Count)*2*time.Minute)
	defer cancel()

	embeddingProvider, err := commands.SetupEmbeddingProvider(ctx, cli.EmbeddingConfig, logger)
	if err != nil {
		logger.Fatal("Failed to initialize embedding provider", "error", err)
		return err
	}

	var totalTime time.Duration
	var embeddingLen int
	for i := 0; i < c.Count; i++ {
		start := time.Now()
		embedding, err := embeddingProvider.GenerateEmbedding(ctx, c.Text)
		elapsed := time.Since(start)
		if err != nil {
			logger.Fatal("Failed to generate embedding", "iteration", i+1, "error", err)
			return err
		}
		if i == 0 {
			embeddingLen = len(embedding)
		}
		totalTime += elapsed
		fmt.Printf("Run %d: %v (embedding length: %d)\n", i+1, elapsed, len(embedding))
	}
	avgTime := totalTime / time.Duration(c.Count)
	fmt.Printf("\nBenchmark complete: %d runs\n", c.Count)
	fmt.Printf("Total time: %v\n", totalTime)
	fmt.Printf("Average time per embedding: %v\n", avgTime)
	fmt.Printf("Embedding length: %d\n", embeddingLen)
	return nil
}

func main() {
	cli := &EmbeddingsCLI{}
	ctx := kong.Parse(cli,
		kong.Name("bank-transaction-embeddings"),
		kong.Description("Manage and update transaction embeddings"),
		kong.UsageOnError(),
	)
	// Dispatch to the selected subcommand
	err := ctx.Run(cli)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
