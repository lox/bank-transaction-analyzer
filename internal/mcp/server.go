package mcp

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
	"github.com/lox/bank-transaction-analyzer/internal/search"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type Server struct {
	db                 *db.DB
	logger             *log.Logger
	banks              []string // List of available banks
	embeddingsProvider embeddings.EmbeddingProvider
	vectorStorage      embeddings.VectorStorage
}

func New(db *db.DB, logger *log.Logger, embeddingsProvider embeddings.EmbeddingProvider, vectorStorage embeddings.VectorStorage, banks []string) *Server {
	return &Server{
		db:                 db,
		logger:             logger,
		banks:              banks,
		embeddingsProvider: embeddingsProvider,
		vectorStorage:      vectorStorage,
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
		mcp.WithString("amount",
			mcp.Description("Filter by exact amount (e.g. '326.02')"),
		),
		mcp.WithString("min_amount",
			mcp.Description("Filter by minimum amount (e.g. '100')"),
		),
		mcp.WithString("max_amount",
			mcp.Description("Filter by maximum amount (e.g. '500')"),
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

	mcpServer.AddTool(mcp.NewTool("update_transaction",
		mcp.WithDescription("Update merchant, type, details_category, or tags for a transaction by ID"),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Transaction ID to update"),
		),
		mcp.WithString("merchant",
			mcp.Description("New merchant name (optional)"),
		),
		mcp.WithString("type",
			mcp.Description("New transaction type (optional)"),
		),
		mcp.WithString("details_category",
			mcp.Description("New transaction category (optional)"),
		),
		mcp.WithString("tags",
			mcp.Description("Comma-separated tags to add (optional)"),
		),
	), s.updateTransactionHandler)

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

	// Perform the search using the decoupled search package
	searchResults, err := search.HybridSearch(
		ctx,
		s.logger,
		s.db,
		s.embeddingsProvider,
		s.vectorStorage,
		query,
		search.WithLimit(limit),
		search.WithDays(days),
		search.OrderByRelevance(),
		search.WithVectorThreshold(0.4),
	)
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
	minAmount, hasMinAmount := request.Params.Arguments["min_amount"].(string)
	maxAmount, hasMaxAmount := request.Params.Arguments["max_amount"].(string)

	// Parse amount parameters
	if !hasMinAmount && !hasMaxAmount {
		// Also check for exact amount
		if exactAmount, hasExactAmount := request.Params.Arguments["amount"].(string); hasExactAmount {
			// Use the exact amount as both min and max for absolute value filtering
			minAmount = exactAmount
			maxAmount = exactAmount
			hasMinAmount = true
			hasMaxAmount = true
		}
	}

	// Use GetTransactions with the limit parameter and 0 offset
	var opts []db.TransactionQueryOption
	opts = append(opts, db.FilterByDays(days))
	opts = append(opts, db.WithLimit(limit))
	if category != "" {
		opts = append(opts, db.FilterByCategory(category))
	}
	if txType != "" {
		opts = append(opts, db.FilterByType(txType))
	}
	if bank != "" {
		opts = append(opts, db.FilterByBank(bank))
	}
	// Add amount filters if provided
	if hasMinAmount && hasMaxAmount {
		// Convert to absolute value filtering
		opts = append(opts, db.FilterByAbsoluteAmount(minAmount, maxAmount))
	} else if hasMinAmount {
		opts = append(opts, db.FilterByAbsoluteAmount(minAmount, ""))
	} else if hasMaxAmount {
		opts = append(opts, db.FilterByAbsoluteAmount("", maxAmount))
	}
	transactions, err := s.db.GetTransactions(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}

	filtered := transactions // DB now handles filtering

	// Format transactions as text
	var result string

	// Add header showing count information
	if len(filtered) == 0 {
		result += "No transactions found matching your criteria.\n\n"
	} else {
		// Determine if we're showing limited results
		if txType != "" || category != "" || bank != "" || hasMinAmount || hasMaxAmount {
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

			// Add message about amount filtering for clarity
			if hasMinAmount && hasMaxAmount && minAmount == maxAmount {
				// Exact amount search
				result += fmt.Sprintf(" with absolute amount %s", minAmount)
			} else if hasMinAmount && hasMaxAmount {
				result += fmt.Sprintf(" with absolute amount between %s and %s", minAmount, maxAmount)
			} else if hasMinAmount {
				result += fmt.Sprintf(" with minimum absolute amount %s", minAmount)
			} else if hasMaxAmount {
				result += fmt.Sprintf(" with maximum absolute amount %s", maxAmount)
			}

			result += ":\n\n"
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

// Handler for update_transaction
func (s *Server) updateTransactionHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, ok := request.Params.Arguments["id"].(string)
	if !ok || id == "" {
		return nil, errors.New("id is required and must be a string")
	}

	var merchant, txType, category, tags *string

	if v, ok := request.Params.Arguments["merchant"].(string); ok && v != "" {
		merchant = &v
	}
	if v, ok := request.Params.Arguments["type"].(string); ok && v != "" {
		// Validate type
		if _, valid := types.AllowedTypesMap[v]; !valid {
			var allowed []string
			for k := range types.AllowedTypesMap {
				allowed = append(allowed, k)
			}
			return nil, fmt.Errorf("invalid type '%s'. Allowed types: %v", v, allowed)
		}
		txType = &v
	}
	if v, ok := request.Params.Arguments["details_category"].(string); ok && v != "" {
		// Validate category
		if _, valid := types.AllowedCategoriesMap[v]; !valid {
			var allowed []string
			for k := range types.AllowedCategoriesMap {
				allowed = append(allowed, k)
			}
			return nil, fmt.Errorf("invalid category '%s'. Allowed categories: %v", v, allowed)
		}
		category = &v
	}
	if v, ok := request.Params.Arguments["tags"].(string); ok && v != "" {
		tags = &v
	}

	err := s.db.UpdateTransaction(ctx, id, merchant, txType, category, tags)
	if err != nil {
		return nil, fmt.Errorf("failed to update transaction: %w", err)
	}

	return mcp.NewToolResultText("Transaction updated successfully."), nil
}
