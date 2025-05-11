package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type Server struct {
	db       *db.DB
	analyzer *analyzer.Analyzer
	logger   *log.Logger
	banks    []string // List of available banks
}

func New(db *db.DB, analyzer *analyzer.Analyzer, logger *log.Logger, banks []string) *Server {
	return &Server{
		db:       db,
		analyzer: analyzer,
		logger:   logger,
		banks:    banks,
	}
}

func (s *Server) Run() error {
	// Create MCP server
	mcpServer := server.NewMCPServer(
		"Bank Transaction Analyzer",
		"1.1.0",
	)

	mcpServer.AddTool(mcp.NewTool("search_transactions",
		mcp.WithDescription("Search for transactions in your history"),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query - what you're looking for"),
		),
		mcp.WithString("days",
			mcp.Required(),
			mcp.Description("Number of days to look back"),
		),
		mcp.WithString("limit",
			mcp.Description("Maximum number of results to return (default: 10)"),
		),
		mcp.WithString("bank",
			mcp.Description("Filter by bank/source (e.g. 'amex', 'ing-australia')"),
		),
	), s.searchTransactionsHandler)

	mcpServer.AddTool(mcp.NewTool("list_transactions",
		mcp.WithDescription("List transactions chronologically with optional filters"),
		mcp.WithString("days",
			mcp.Required(),
			mcp.Description("Number of days to look back"),
		),
		mcp.WithString("limit",
			mcp.Description("Maximum number of results to return (default: 50)"),
		),
		mcp.WithString("type",
			mcp.Description("Filter by transaction type (purchase, transfer, fee, deposit, withdrawal, refund, interest)"),
		),
		mcp.WithString("category",
			mcp.Description("Filter by transaction category. Use list_categories tool to see available categories."),
		),
		mcp.WithString("bank",
			mcp.Description("Filter by bank/source (e.g. 'amex', 'ing-australia')"),
		),
	), s.listTransactionsHandler)

	mcpServer.AddTool(mcp.NewTool("list_categories",
		mcp.WithDescription("List all unique transaction categories with their transaction counts"),
		mcp.WithString("days",
			mcp.Required(),
			mcp.Description("Number of days to look back"),
		),
		mcp.WithString("bank",
			mcp.Description("Filter by bank/source (e.g. 'amex', 'ing-australia')"),
		),
	), s.listCategoriesHandler)

	mcpServer.AddTool(mcp.NewTool("list_banks",
		mcp.WithDescription("List all available banks/sources for transactions"),
	), s.listBanksHandler)

	// Start the stdio server
	if err := server.ServeStdio(mcpServer); err != nil {
		return err
	}

	return nil
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

	limit := 10 // default limit
	if limitVal, ok := request.Params.Arguments["limit"]; ok {
		switch v := limitVal.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		case string:
			var err error
			limit, err = strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("limit must be a valid integer: %w", err)
			}
		default:
			return nil, errors.New("limit must be a number or string")
		}
	}

	searchResults, err := s.analyzer.HybridSearch(ctx, query, days, limit, 0.4)
	if err != nil {
		return nil, fmt.Errorf("failed to search transactions: %w", err)
	}

	// Format transactions as text
	var result string
	if len(searchResults.Results) == 0 {
		result += "No transactions found matching your search.\n"
	} else {
		// Show result count information
		if searchResults.TotalCount > searchResults.Limit {
			result += fmt.Sprintf("Found %d transactions (showing %d):\n\n",
				searchResults.TotalCount, len(searchResults.Results))
		} else {
			result += fmt.Sprintf("Found %d transactions:\n\n", len(searchResults.Results))
		}

		for _, searchResult := range searchResults.Results {
			t := searchResult.TransactionWithDetails

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
	}

	return mcp.NewToolResultText(result), nil
}

func (s *Server) listTransactionsHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse days parameter
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

	// Parse optional limit parameter
	limit := 50 // default limit
	if limitVal, ok := request.Params.Arguments["limit"]; ok {
		switch v := limitVal.(type) {
		case int:
			limit = v
		case float64:
			limit = int(v)
		case string:
			var err error
			limit, err = strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("limit must be a valid integer: %w", err)
			}
		}
	}

	// Get optional filters
	txType, _ := request.Params.Arguments["type"].(string)
	category, _ := request.Params.Arguments["category"].(string)
	bank, _ := request.Params.Arguments["bank"].(string)

	// First, count total transactions for the period
	totalCount, err := s.db.CountTransactions(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("failed to count transactions: %w", err)
	}

	// Use GetTransactions with the limit parameter and 0 offset
	transactions, err := s.db.GetTransactions(ctx, days, limit, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}

	// Filter transactions by type, category, and bank if specified
	var filtered []types.TransactionWithDetails
	for _, t := range transactions {
		// Skip if type filter is set and doesn't match
		if txType != "" && string(t.Details.Type) != txType {
			continue
		}
		// Skip if category filter is set and doesn't match
		if category != "" && t.Details.Category != category {
			continue
		}
		// Skip if bank filter is set and doesn't match
		if bank != "" && t.Bank != bank {
			continue
		}
		filtered = append(filtered, t)
	}

	// Format transactions as text
	var result string

	// Add header showing count information
	if len(filtered) == 0 {
		result += "No transactions found matching your criteria.\n\n"
	} else {
		// Determine if we're showing limited results
		if txType != "" || category != "" || bank != "" {
			// When filtering by type, category, or bank, just show the filtered count
			result += fmt.Sprintf("Found %d transactions", len(filtered))
			if txType != "" {
				result += fmt.Sprintf(" of type '%s'", txType)
			}
			if category != "" {
				result += fmt.Sprintf(" in category '%s'", category)
			}
			if bank != "" {
				result += fmt.Sprintf(" from bank '%s'", bank)
			}
			result += ":\n\n"
		} else if len(filtered) < totalCount {
			// When showing fewer than the total due to limit
			result += fmt.Sprintf("Showing %d of %d transactions (most recent first):\n\n",
				len(filtered), totalCount)
		} else {
			result += fmt.Sprintf("Found %d transactions:\n\n", len(filtered))
		}

		for _, t := range filtered {
			result += fmt.Sprintf("%s: %s - %s\n", t.Date, t.Amount, t.Payee)
			result += fmt.Sprintf("  Type: %s\n", t.Details.Type)
			if t.Details.Merchant != "" {
				result += fmt.Sprintf("  Merchant: %s\n", t.Details.Merchant)
			}
			if t.Details.Category != "" {
				result += fmt.Sprintf("  Category: %s\n", t.Details.Category)
			}
			if t.Details.Description != "" {
				result += fmt.Sprintf("  Description: %s\n", t.Details.Description)
			}
			if t.Details.Location != "" {
				result += fmt.Sprintf("  Location: %s\n", t.Details.Location)
			}
			if t.Details.CardNumber != "" {
				result += fmt.Sprintf("  Card Number: %s\n", t.Details.CardNumber)
			}
			if t.Details.ForeignAmount != nil {
				result += fmt.Sprintf("  Foreign Amount: %s %s\n",
					t.Details.ForeignAmount.Amount,
					t.Details.ForeignAmount.Currency)
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
	}

	return mcp.NewToolResultText(result), nil
}

func (s *Server) listCategoriesHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Parse days parameter
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

	bank, _ := request.Params.Arguments["bank"].(string)

	// Get categories from database, filtered by bank if provided
	categories, err := s.db.GetCategoriesWithBank(ctx, days, bank)
	if err != nil {
		return nil, fmt.Errorf("failed to get categories: %w", err)
	}

	// Format results
	var result string
	result += fmt.Sprintf("Transaction Categories (Last %d days)", days)
	if bank != "" {
		result += fmt.Sprintf(" for bank '%s'", bank)
	}
	result += "\n\n"

	var totalTransactions int
	for _, cat := range categories {
		result += fmt.Sprintf("%-30s %d transactions\n", cat.Category, cat.Count)
		totalTransactions += cat.Count
	}

	result += fmt.Sprintf("\nTotal Categorized Transactions: %d\n", totalTransactions)

	return mcp.NewToolResultText(result), nil
}

func (s *Server) listBanksHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if len(s.banks) == 0 {
		return mcp.NewToolResultText("No banks are registered."), nil
	}
	result := "Available Banks:\n\n"
	for _, bank := range s.banks {
		result += "- " + bank + "\n"
	}
	return mcp.NewToolResultText(result), nil
}
