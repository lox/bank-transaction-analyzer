package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/db"
)

type GlobalFlags struct {
	DataDir  string `help:"Path to data directory" default:"./data"`
	Verbose  bool   `help:"Enable verbose logging" default:"false" short:"v"`
	Timezone string `help:"Timezone to use for transaction dates" required:"" default:"Australia/Sydney"`
}

type CLI struct {
	GlobalFlags
	Query string `help:"Search query - what you're looking for" required:""`
	Days  int    `help:"Number of days to look back" default:"30"`
	Limit int    `help:"Maximum number of results to return" default:"10"`
}

func (c *CLI) Run() error {
	// Setup logger
	logger := log.New(os.Stderr)
	if c.Verbose {
		logger.SetLevel(log.DebugLevel)
	}

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

	// Search transactions using full-text search
	transactions, err := database.SearchTransactionsByText(context.Background(), c.Query, c.Days, c.Limit)
	if err != nil {
		logger.Fatal("Failed to search transactions", "error", err)
	}

	// Print results
	if len(transactions) == 0 {
		fmt.Println("No transactions found")
		return nil
	}

	fmt.Printf("Found %d transactions:\n\n", len(transactions))
	for _, t := range transactions {
		fmt.Printf("%s: %s - %s\n", t.Date, t.Amount, t.Payee)
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

	return nil
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
