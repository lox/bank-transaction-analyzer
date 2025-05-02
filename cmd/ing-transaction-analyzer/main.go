package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-analyzer/internal/analyzer"
	"github.com/lox/ing-transaction-analyzer/internal/db"
	"github.com/lox/ing-transaction-analyzer/internal/qif"
	"github.com/lox/ing-transaction-analyzer/internal/types"
	"github.com/sashabaranov/go-openai"
)

type GlobalFlags struct {
	DataDir     string `help:"Path to data directory" default:"./data"`
	OpenAIKey   string `help:"OpenAI API key" env:"OPENAI_API_KEY" required:""`
	OpenAIModel string `help:"OpenAI model to use for analysis" default:"gpt-4.1" env:"OPENAI_MODEL"`
	Concurrency int    `help:"Number of concurrent transactions to process" default:"3"`
	Verbose     bool   `help:"Enable verbose logging" default:"false" short:"v"`
	Timezone    string `help:"Timezone to use for transaction dates" required:"" default:"Australia/Melbourne"`
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

	// Parse QIF file
	qifTransactions, err := qif.ParseFile(c.QIFFile)
	if err != nil {
		logger.Fatal("Failed to parse QIF file", "error", err)
	}

	// Convert QIF transactions to generic transactions
	transactions := make([]types.Transaction, len(qifTransactions))
	for i, t := range qifTransactions {
		transactions[i] = types.Transaction{
			Date:   t.Date,
			Amount: t.Amount,
			Payee:  t.Payee,
		}
	}

	// Process transactions
	processCtx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Analyze transactions
	an, err := analyzer.NewAnalyzer(client, logger, database)
	if err != nil {
		logger.Fatal("Failed to create analyzer", "error", err)
	}

	_, err = an.AnalyzeTransactions(processCtx, transactions, analyzer.Config{
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
		kong.Name("ing-transaction-analyzer"),
		kong.Description("A tool to analyze ING bank transactions from QIF files using OpenAI"),
		kong.UsageOnError(),
	)

	err := ctx.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
