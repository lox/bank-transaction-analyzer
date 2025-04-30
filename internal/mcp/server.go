package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-analyzer/internal/db"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	dataDir  = "./data"
	timezone = "Australia/Sydney"
)

type Server struct {
	logger *log.Logger
	db     *db.DB
}

func New() *Server {
	// Create a null logger that discards all output
	logger := log.New(os.Stderr)

	// Load timezone
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		logger.Fatal("Failed to load timezone", "error", err)
	}

	// Initialize database
	database, err := db.New(dataDir, logger, loc)
	if err != nil {
		logger.Fatal("Failed to initialize database", "error", err)
	}

	return &Server{
		logger: logger,
		db:     database,
	}
}

func (s *Server) Run() error {
	// Create MCP server
	mcpServer := server.NewMCPServer(
		"ING Transaction Analyzer",
		"1.0.0",
	)

	mcpServer.AddTool(mcp.NewTool("get_transactions",
		mcp.WithDescription("Get transactions from the database"),
		mcp.WithNumber("days",
			mcp.Required(),
			mcp.Description("Number of days to look back (must be a positive integer greater than 0)"),
		),
	), s.getTransactionsHandler)

	mcpServer.AddTool(mcp.NewTool("search_transactions",
		mcp.WithDescription("Search transactions using full-text search"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query (supports SQLite FTS5 syntax)"),
		),
		mcp.WithNumber("days",
			mcp.Required(),
			mcp.Description("Number of days to look back (integer)"),
		),
	), s.searchTransactionsHandler)

	// Start the stdio server
	if err := server.ServeStdio(mcpServer); err != nil {
		return err
	}

	return nil
}

func (s *Server) getTransactionsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	var days int
	switch v := request.Params.Arguments["days"].(type) {
	case float64:
		days = int(v)
		if float64(days) != v {
			return nil, fmt.Errorf("days must be a whole number, got %v", v)
		}
	case int:
		days = v
	case string:
		var err error
		days, err = strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("days must be a valid number, got %q: %w", v, err)
		}
	default:
		return nil, fmt.Errorf("days must be a number, got %T", v)
	}

	// Validate days is positive
	if days <= 0 {
		return nil, fmt.Errorf("days must be greater than 0, got %d", days)
	}

	s.logger.Debug("Getting transactions", "days", days)
	transactions, err := s.db.GetTransactions(ctx, days)
	if err != nil {
		s.logger.Error("Failed to get transactions", "error", err)
		return nil, fmt.Errorf("failed to get transactions: %w", err)
	}

	// Format transactions as text
	var result string
	for _, t := range transactions {
		result += fmt.Sprintf("%s: %s - %s\n", t.Date, t.Amount, t.Payee)
		result += fmt.Sprintf("  Type: %s\n", t.Details.Type)
		if t.Details.Merchant != "" {
			result += fmt.Sprintf("  Merchant: %s\n", t.Details.Merchant)
		}
		if t.Details.Location != "" {
			result += fmt.Sprintf("  Location: %s\n", t.Details.Location)
		}
		if t.Details.Category != "" {
			result += fmt.Sprintf("  Category: %s\n", t.Details.Category)
		}
		if t.Details.Description != "" {
			result += fmt.Sprintf("  Description: %s\n", t.Details.Description)
		}
		if t.Details.CardNumber != "" {
			result += fmt.Sprintf("  Card Number: %s\n", t.Details.CardNumber)
		}
		if t.Details.ForeignAmount != nil {
			result += fmt.Sprintf("  Foreign Amount: %s %s\n", t.Details.ForeignAmount.Amount, t.Details.ForeignAmount.Currency)
		}
		if t.Details.TransferDetails != nil {
			if t.Details.TransferDetails.ToAccount != "" {
				result += fmt.Sprintf("  To Account: %s\n", t.Details.TransferDetails.ToAccount)
			}
			if t.Details.TransferDetails.FromAccount != "" {
				result += fmt.Sprintf("  From Account: %s\n", t.Details.TransferDetails.FromAccount)
			}
			if t.Details.TransferDetails.Reference != "" {
				result += fmt.Sprintf("  Reference: %s\n", t.Details.TransferDetails.Reference)
			}
		}
		result += "\n"
	}

	return mcp.NewToolResultText(result), nil
}

func (s *Server) searchTransactionsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, ok := request.Params.Arguments["query"].(string)
	if !ok {
		return nil, errors.New("query must be a string")
	}

	var days int
	switch v := request.Params.Arguments["days"].(type) {
	case int:
		days = v
	case float64:
		days = int(v)
	case string:
		var err error
		days, err = strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("days must be a valid integer: %w", err)
		}
	default:
		return nil, errors.New("days must be a number or string")
	}

	s.logger.Debug("Searching transactions", "query", query, "days", days)
	transactions, err := s.db.SearchTransactions(ctx, query, days)
	if err != nil {
		s.logger.Error("Failed to search transactions", "error", err)
		return nil, fmt.Errorf("failed to search transactions: %w", err)
	}

	// Format transactions as text
	var result string
	for _, t := range transactions {
		result += fmt.Sprintf("%s: %s - %s\n", t.Date, t.Amount, t.Payee)
		result += fmt.Sprintf("  Type: %s\n", t.Details.Type)
		if t.Details.Merchant != "" {
			result += fmt.Sprintf("  Merchant: %s\n", t.Details.Merchant)
		}
		if t.Details.Location != "" {
			result += fmt.Sprintf("  Location: %s\n", t.Details.Location)
		}
		if t.Details.Category != "" {
			result += fmt.Sprintf("  Category: %s\n", t.Details.Category)
		}
		if t.Details.Description != "" {
			result += fmt.Sprintf("  Description: %s\n", t.Details.Description)
		}
		if t.Details.CardNumber != "" {
			result += fmt.Sprintf("  Card Number: %s\n", t.Details.CardNumber)
		}
		if t.Details.ForeignAmount != nil {
			result += fmt.Sprintf("  Foreign Amount: %s %s\n", t.Details.ForeignAmount.Amount, t.Details.ForeignAmount.Currency)
		}
		if t.Details.TransferDetails != nil {
			if t.Details.TransferDetails.ToAccount != "" {
				result += fmt.Sprintf("  To Account: %s\n", t.Details.TransferDetails.ToAccount)
			}
			if t.Details.TransferDetails.FromAccount != "" {
				result += fmt.Sprintf("  From Account: %s\n", t.Details.TransferDetails.FromAccount)
			}
			if t.Details.TransferDetails.Reference != "" {
				result += fmt.Sprintf("  Reference: %s\n", t.Details.TransferDetails.Reference)
			}
		}
		result += "\n"
	}

	return mcp.NewToolResultText(result), nil
}
