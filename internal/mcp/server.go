package mcp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-analyzer/internal/analyzer"
	"github.com/lox/ing-transaction-analyzer/internal/db"
	"github.com/lox/ing-transaction-analyzer/internal/types"
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
	), s.listTransactionsHandler)

	mcpServer.AddTool(mcp.NewTool("list_categories",
		mcp.WithDescription("List all unique transaction categories with their transaction counts"),
		mcp.WithString("days",
			mcp.Required(),
			mcp.Description("Number of days to look back"),
		),
	), s.listCategoriesHandler)

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

	// Generate embedding for the query
	config := analyzer.NewLlamaCppConfig().WithLogger(s.logger)
	embeddings, err := analyzer.NewLlamaCppEmbeddingProvider(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding provider: %w", err)
	}

	embedding, err := embeddings.GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Serialize the embedding for sqlite-vec
	serializedEmbedding, err := s.db.SerializeEmbedding(embedding)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize embedding: %w", err)
	}

	s.logger.Debug("Searching transactions", "query", query, "days", days, "limit", limit)
	transactions, err := s.db.SearchTransactionsHybrid(ctx, query, serializedEmbedding, days, limit)
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
		// Add search scores
		result += "  Search Scores:\n"
		if t.Scores.VectorScore != 0 {
			result += fmt.Sprintf("    Vector Score: %.4f\n", t.Scores.VectorScore)
		}
		if t.Scores.TextScore != 0 {
			result += fmt.Sprintf("    Text Score: %.4f\n", t.Scores.TextScore)
		}
		result += fmt.Sprintf("    RRF Score: %.4f\n", t.Scores.RRFScore)
		result += "\n"
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

	transactions, err := s.db.GetTransactions(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("failed to list transactions: %w", err)
	}

	// Filter transactions by type and category if specified
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
		filtered = append(filtered, t)
	}

	// Apply limit after filtering
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	// Format transactions as text
	var result string
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

	// Get categories from database
	categories, err := s.db.GetCategories(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("failed to get categories: %w", err)
	}

	// Format results
	var result string
	result += fmt.Sprintf("Transaction Categories (Last %d days)\n\n", days)

	var totalTransactions int
	for _, cat := range categories {
		result += fmt.Sprintf("%-30s %d transactions\n", cat.Category, cat.Count)
		totalTransactions += cat.Count
	}

	result += fmt.Sprintf("\nTotal Categorized Transactions: %d\n", totalTransactions)

	return mcp.NewToolResultText(result), nil
}
