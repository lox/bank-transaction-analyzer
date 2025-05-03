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
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/sashabaranov/go-openai"
)

type GlobalFlags struct {
	DataDir     string `help:"Path to data directory" default:"./data"`
	OpenAIKey   string `help:"OpenAI API key" env:"OPENAI_API_KEY" required:""`
	OpenAIModel string `help:"OpenAI model to use for analysis" default:"gpt-4.1" env:"OPENAI_MODEL"`
	Concurrency int    `help:"Number of concurrent transactions to process" default:"5"`
	Verbose     bool   `help:"Enable verbose logging" default:"false" short:"v"`
	Timezone    string `help:"Timezone to use for transaction dates" required:"" default:"Australia/Melbourne"`
	Bank        string `help:"Bank to use for processing" default:"ing-australia" enum:"ing-australia"`
}

type CLI struct {
	GlobalFlags
	QIFFile string `help:"Path to QIF file to process" required:""`
}

func (c *CLI) Run() error {
	logger := log.New(os.Stderr)
	if c.Verbose {
		logger.SetLevel(log.DebugLevel)
	}

	// Initialize OpenAI client
	client := openai.NewClient(c.OpenAIKey)

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

	// Process transactions
	processCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Create analyzer
	an, err := analyzer.NewAnalyzer(client, logger, database)
	if err != nil {
		logger.Fatal("Failed to create analyzer", "error", err)
	}

	// Process transactions
	_, err = bankImpl.ProcessTransactions(processCtx, transactions, an, analyzer.Config{
		Model:       c.OpenAIModel,
		Concurrency: c.Concurrency,
		Progress:    !c.Verbose,
	})
	if err != nil {
		logger.Fatal("Failed to process transactions", "error", err)
	}

	return nil
}

func main() {
	// Normal Kong CLI handling
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("bank-transaction-analyzer"),
		kong.Description("A tool to analyze bank transactions"),
		kong.UsageOnError(),
	)

	err := ctx.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
