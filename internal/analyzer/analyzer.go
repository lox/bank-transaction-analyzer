package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/agent"
	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	openai "github.com/sashabaranov/go-openai"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	OpenRouterModel string
	Concurrency     int
	Progress        bool
	DryRun          bool
	Limit           int
}

type Analyzer struct {
	agent      *agent.Agent
	logger     *log.Logger
	db         *db.DB
	embeddings embeddings.EmbeddingProvider
	vectors    embeddings.VectorStorage
}

// NewAnalyzer creates a new transaction analyzer with explicit dependencies
func NewAnalyzer(
	agent *agent.Agent,
	logger *log.Logger,
	db *db.DB,
	embeddingProvider embeddings.EmbeddingProvider,
	vectorStorage embeddings.VectorStorage,
) *Analyzer {
	return &Analyzer{
		agent:      agent,
		logger:     logger,
		db:         db,
		embeddings: embeddingProvider,
		vectors:    vectorStorage,
	}
}

// AnalyzeTransactions processes and returns only newly analyzed transactions (not already in the database)
func (a *Analyzer) AnalyzeTransactions(ctx context.Context, transactions []types.Transaction, config Config, bank bank.Bank) ([]types.TransactionWithDetails, error) {
	startTime := time.Now()
	a.logger.Info("Starting transaction analysis", "total_transactions", len(transactions))

	// Filter out transactions that already exist in the database
	filterStart := time.Now()
	filteredTransactions, err := a.db.FilterExistingTransactions(ctx, transactions)
	if err != nil {
		return nil, fmt.Errorf("error filtering existing transactions: %w", err)
	}
	// Apply limit after filtering
	if config.Limit > 0 && len(filteredTransactions) > config.Limit {
		filteredTransactions = filteredTransactions[:config.Limit]
	}
	a.logger.Debug("Filtered existing transactions",
		"duration", time.Since(filterStart),
		"total", len(filteredTransactions),
		"skipped", len(transactions)-len(filteredTransactions))

	// Create progress bar
	var progress Progress
	if !config.Progress {
		progress = NewNoopProgress()
	} else {
		progress = NewBarProgress(len(filteredTransactions))
	}

	// Initialize result slice with capacity for all newly processed transactions
	analyzedTransactions := make([]types.TransactionWithDetails, 0, len(filteredTransactions))

	// Process new transactions in parallel
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(config.Concurrency)

	for _, t := range filteredTransactions {
		t := t // Create new variable for the goroutine
		g.Go(func() error {
			// Check if context is canceled before starting
			if err := gCtx.Err(); err != nil {
				return err
			}

			// Parse transaction details
			analysisStart := time.Now()
			details, err := a.analyzeTransaction(gCtx, t, config.OpenRouterModel, bank)
			if err != nil {
				// If context was canceled, return immediately
				if errors.Is(err, context.Canceled) {
					return err
				}
				a.logger.Error("Failed to analyze transaction",
					"error", err,
					"payee", t.Payee,
					"duration", time.Since(analysisStart))
				return fmt.Errorf("error analyzing transaction: %w", err)
			}
			a.logger.Debug("Transaction analysis completed",
				"payee", t.Payee,
				"duration", time.Since(analysisStart))

			// In dry run mode, skip storing transaction and embedding
			if !config.DryRun {
				// Store transaction details
				storeStart := time.Now()
				if err := a.storeTransaction(gCtx, t, details); err != nil {
					// If context was canceled, return immediately
					if errors.Is(err, context.Canceled) {
						return err
					}
					a.logger.Error("Failed to store transaction",
						"error", err,
						"payee", t.Payee,
						"duration", time.Since(storeStart))
					return fmt.Errorf("error storing transaction: %w", err)
				}
			}

			// Add to results
			analyzedTransactions = append(analyzedTransactions, types.TransactionWithDetails{
				Transaction: t,
				Details:     *details,
			})

			// Update progress
			if err := progress.Add(1); err != nil {
				// If context was canceled, return immediately
				if errors.Is(err, context.Canceled) {
					return err
				}
				return fmt.Errorf("error updating progress: %w", err)
			}

			return nil
		})
	}

	// Wait for all goroutines to complete
	if err := g.Wait(); err != nil {
		if errors.Is(err, context.Canceled) {
			a.logger.Info("Transaction analysis interrupted by user")
			return nil, err
		}
		return nil, fmt.Errorf("error analyzing transactions: %w", err)
	}

	a.logger.Info("Successfully analyzed transactions",
		"total_duration", time.Since(startTime),
		"total", len(filteredTransactions),
		"skipped", len(transactions)-len(filteredTransactions))

	return analyzedTransactions, nil
}

func validateTransactionDetails(details *types.TransactionDetails) error {
	var invalids []string
	if _, ok := types.AllowedTypesMap[details.Type]; !ok {
		invalids = append(invalids, fmt.Sprintf("type='%s'", details.Type))
	}
	if _, ok := types.AllowedCategoriesMap[details.Category]; !ok {
		invalids = append(invalids, fmt.Sprintf("category='%s'", details.Category))
	}

	// Check if foreign amount has a valid currency code (3 letters)
	if details.ForeignAmount != nil {
		currency := details.ForeignAmount.Currency
		if len(currency) != 3 || !isValidCurrencyCode(currency) {
			invalids = append(invalids, fmt.Sprintf("foreign_amount.currency='%s'", currency))
		}
	}

	if len(invalids) > 0 {
		return fmt.Errorf("invalid %s. Please use only allowed values", strings.Join(invalids, ", "))
	}
	return nil
}

// isValidCurrencyCode checks if a string is a valid ISO 4217 currency code
// This is a simple validation that checks if the string is 3 uppercase letters
func isValidCurrencyCode(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, c := range code {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// Helper to extract names from TransactionType slice
func getTypeNames(types []types.TransactionType) []string {
	names := make([]string, len(types))
	for i, t := range types {
		names[i] = t.Name
	}
	return names
}

// Helper to extract names from TransactionCategory slice
func getCategoryNames(categories []types.TransactionCategory) []string {
	names := make([]string, len(categories))
	for i, c := range categories {
		names[i] = c.Name
	}
	return names
}

// Helper to build transaction type guidelines from AllowedTypes
func buildTypeGuidelines() string {
	var sb strings.Builder
	for _, t := range types.AllowedTypes {
		sb.WriteString(fmt.Sprintf("- \"%s\" - %s\n", t.Name, t.Guideline))
	}
	return sb.String()
}

// Helper to build category guidelines from AllowedCategories
func buildCategoryGuidelines() string {
	var sb strings.Builder
	sb.WriteString("Use one of these specific categories:\n")
	for _, c := range types.AllowedCategories {
		sb.WriteString(fmt.Sprintf("- %s (%s)\n", c.Name, c.Guideline))
	}
	return sb.String()
}

// analyzeTransaction uses an LLM to extract structured information from a transaction
func (a *Analyzer) analyzeTransaction(ctx context.Context, t types.Transaction, model string, bank bank.Bank) (*types.TransactionDetails, error) {
	startTime := time.Now()
	a.logger.Debug("Analyzing transaction",
		"payee", t.Payee,
		"amount", t.Amount,
		"date", t.Date,
		"model", model)

	promptBase := fmt.Sprintf(`Extract and classify transaction details from the following bank transaction.

Transaction: %s
Amount: %s
Date: %s

BANK-SPECIFIC RULES:
%s

Your task is to analyze this transaction and call the classify_transaction function with structured data.

When calling the function, follow these guidelines:

IMPORTANT RULES:
1. Remove payment processor prefixes and suffixes:
   - Remove "SQ *" (Square)
   - Remove "Visa Purchase", "EFTPOS Purchase"
   - Remove "Receipt" and receipt numbers
   - Remove "Date", "Time" and timestamps
   - Remove "Card" and card numbers unless storing in card_number field
2. Extract location even if it's at the end of the merchant name
3. Clean merchant names:
   - Remove any domain names (e.g., .com, .co)
   - Remove any help/support text
   - Use the main brand name only
   - Use proper case formatting (e.g., "Richie's Cafe" not "RICHIES CAFE")
   - For chains, use their official capitalization (e.g., "McDonald's" not "MCDONALDS")
   - For local businesses, use title case with appropriate apostrophes
4. For foreign amounts:
   - Carefully extract all foreign amount and currency information
   - If you see "Foreign Currency Amount: USD 11.99", extract amount=11.99 and currency=USD
   - Amount MUST be a valid number (integer or decimal), not a string or object
   - The amount should be just the number value without currency symbols or formatting
   - Currency must be the 3-letter currency code (USD, EUR, GBP, etc.)
   - Example: For "Foreign amount USD 29.99", use {"amount": 29.99, "currency": "USD"}
   - NEVER return an empty object {} for foreign_amount
   - If a foreign currency amount is present but the currency code is missing or invalid, do not return a foreign_amount object.
5. Special handling for food delivery services:
   - For Uber Eats, use "Uber Eats" as merchant name
   - For DoorDash, use "DoorDash" as merchant name
   - For Menulog, use "Menulog" as merchant name
   - Category should be "Food & Dining" for all food delivery services
6. Description field rules:
   - NEVER include the amount in the description
   - Provide a brief description of what was purchased
   - For restaurants/cafes, describe the type of food or service
   - For retail, describe the type of items purchased
   - For services, describe the service provided
   - Keep descriptions concise but informative
7. Card number extraction:
   - If a card number is present in the format "Card XXXX...XXXX", extract it
   - Store the full card number in the card_number field
   - Do not mask or truncate the card number
8. Transfer details processing:
   - For transfer transactions, carefully extract account numbers, removing any non-essential characters
   - The to_account field should only contain the account number, not dates, amounts, or transaction details
   - For reference fields, keep only the actual reference text, not dates or amounts
   - Never include the full transaction details in the transfer_details fields
   - Ignore any narrative text that describes the transaction
9. Search body generation:
   - Generate a search_body field that contains all relevant keywords from the transaction
   - Include the merchant name, location, description, and any other relevant details
   - Use proper case formatting for the search_body
10. Transaction type classification:
%s
11. Transaction category classification:
%s

EXAMPLES:

Example 1 - Purchase:
Transaction: EFTPOS PURCHASE AMAZON.COM.AU SYDNEY AU ON 12 MAR Card 1234...5678
Amount: -49.95
Date: 2023-03-12

Correct response:
{
  "type": "purchase",
  "merchant": "Amazon",
  "location": "Sydney AU",
  "category": "Shopping",
  "description": "Online retail purchase",
  "card_number": "1234...5678",
  "search_body": "Amazon Sydney AU Online retail purchase"
}

Example 2 - Transfer:
Transaction: Transfer to Nicole Smith BSB 062-692 Account 87654321 Reference: BIRTHDAY GIFT
Amount: -100.00
Date: 2023-04-15

Correct response:
{
  "type": "transfer",
  "merchant": "Nicole Smith",
  "category": "Transfers",
  "description": "Personal transfer",
  "search_body": "Nicole Smith Personal transfer Birthday gift",
  "transfer_details": {
    "to_account": "87654321",
    "reference": "BIRTHDAY GIFT"
  }
}

Example 3 - Foreign Purchase:
Transaction: UBER TRIP 1234ABCD SAN FRANCISCO USA Foreign Currency Amount USD 11.99
Amount: -18.52
Date: 2023-05-22

Correct response:
{
  "type": "purchase",
  "merchant": "Uber",
  "location": "San Francisco USA",
  "category": "Transportation",
  "description": "Ride service",
  "search_body": "Uber San Francisco USA Ride service",
  "foreign_amount": {
    "amount": 11.99,
    "currency": "USD"
  }
}

Example 4 - Food Delivery:
Transaction: DOORDASH *MARIOS PIZZA MELBOURNE
Amount: -32.50
Date: 2023-06-10

Correct response:
{
  "type": "purchase",
  "merchant": "DoorDash",
  "location": "Melbourne",
  "category": "Food & Dining",
  "description": "Food delivery from Mario's Pizza",
  "search_body": "DoorDash Melbourne Food delivery Mario's Pizza"
}

Example 5 - BPAY Payment:
Transaction: BPAY PAYMENT-THANK YOU REC # 0000775466
Amount: -7900.77
Date: 2023-06-15

Correct response:
{
  "type": "transfer",
  "merchant": "BPAY",
  "category": "Services",
  "description": "BPAY bill payment",
  "search_body": "BPAY bill payment",
  "transfer_details": {
    "reference": "0000775466"
  }
}

The classify_transaction function requires these fields: type, merchant, category, description, and search_body.`,
		t.Payee, t.Amount, t.Date, bank.AdditionalPromptRules(), buildTypeGuidelines(), buildCategoryGuidelines())

	chatMessages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: "You are a financial transaction classifier. You must call the classify_transaction function to extract and structure transaction data. DO NOT explain your reasoning or add comments. ONLY call the function with properly formatted JSON arguments. Follow the exact structure and guidelines provided in the user's prompt.",
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: promptBase,
		},
	}

	params := map[string]any{
		"type": map[string]any{
			"type":        "string",
			"enum":        getTypeNames(types.AllowedTypes),
			"description": "The type of transaction",
		},
		"merchant": map[string]any{
			"type":        "string",
			"description": "The merchant or counterparty name",
		},
		"location": map[string]any{
			"type":        "string",
			"description": "The location of the transaction, if available",
		},
		"category": map[string]any{
			"type":        "string",
			"description": "The spending category of the transaction",
			"enum":        getCategoryNames(types.AllowedCategories),
		},
		"description": map[string]any{
			"type":        "string",
			"description": "A brief description of what was purchased or the purpose of the transaction",
		},
		"card_number": map[string]any{
			"type":        "string",
			"description": "Card number used for the transaction, if available",
		},
		"search_body": map[string]any{
			"type":        "string",
			"description": "Text to use for search indexing, containing relevant keywords",
		},
		"foreign_amount": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"amount": map[string]any{
					"type":        "number",
					"description": "The amount in foreign currency",
				},
				"currency": map[string]any{
					"type":        "string",
					"description": "The 3-letter currency code",
				},
			},
			"required":    []string{"amount", "currency"},
			"description": "Details of the transaction amount in a foreign currency, if applicable",
		},
		"transfer_details": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"to_account": map[string]any{
					"type":        "string",
					"description": "The recipient account number",
				},
				"from_account": map[string]any{
					"type":        "string",
					"description": "The sender account number",
				},
				"reference": map[string]any{
					"type":        "string",
					"description": "The payment reference or description",
				},
			},
			"description": "Additional details for transfer transactions",
		},
	}
	params["required"] = []string{"type", "merchant", "category", "description", "search_body"}

	f := openai.FunctionDefinition{
		Name:        "classify_transaction",
		Description: "Classify and extract details from bank transaction data",
		Parameters:  params,
		Strict:      true,
	}

	parseTransactionTool := openai.Tool{
		Type:     openai.ToolTypeFunction,
		Function: &f,
	}

	validator := func(toolCall openai.ToolCall) (interface{}, error) {
		if toolCall.Function.Name != "classify_transaction" {
			return nil, fmt.Errorf("unexpected tool call: %s", toolCall.Function.Name)
		}
		var parsedDetails types.TransactionDetails
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &parsedDetails); err != nil {
			a.logger.Warn("Invalid JSON in tool call arguments",
				"error", err,
				"arguments", toolCall.Function.Arguments)
			return nil, fmt.Errorf("invalid JSON in tool call arguments: %w", err)
		}
		// Default type and category to 'other' if empty
		if parsedDetails.Type == "" {
			parsedDetails.Type = types.TransactionTypeOther
		}
		if parsedDetails.Category == "" {
			parsedDetails.Category = types.TransactionCategoryOther
		}
		if err := validateTransactionDetails(&parsedDetails); err != nil {
			return nil, err
		}
		return &parsedDetails, nil
	}

	shouldStop := func(toolCall openai.ToolCall) bool {
		return toolCall.Function.Name == "classify_transaction"
	}

	result, err := a.agent.RunLoop(
		ctx,
		chatMessages,
		[]openai.Tool{parseTransactionTool},
		validator,
		shouldStop,
		3,
	)
	if err != nil {
		return nil, err
	}
	details := result.(*types.TransactionDetails)

	a.logger.Debug("Successfully parsed transaction details",
		"payee", t.Payee,
		"type", details.Type,
		"merchant", details.Merchant,
		"category", details.Category,
		"total_duration", time.Since(startTime))

	return details, nil
}

// storeTransaction stores a transaction and its details in the database
func (a *Analyzer) storeTransaction(ctx context.Context, t types.Transaction, details *types.TransactionDetails) error {
	startTime := time.Now()

	// Store in database
	err := a.db.Store(ctx, t, details)
	if err != nil {
		return fmt.Errorf("failed to store transaction: %w", err)
	}

	// Create a TransactionWithDetails and update embedding
	tx := types.TransactionWithDetails{
		Transaction: t,
		Details:     *details,
	}

	_, err = a.UpdateEmbedding(ctx, &tx)
	if err != nil {
		a.logger.Warn("Failed to update embedding during transaction storage", "error", err)
		// Continue anyway, as storing the transaction in the DB was successful
	}

	a.logger.Debug("Transaction storage completed",
		"payee", t.Payee,
		"total_duration", time.Since(startTime))

	return nil
}

// HasTransactionEmbedding checks if a transaction embedding exists in the vector storage
func (a *Analyzer) HasTransactionEmbedding(ctx context.Context, tx *types.TransactionWithDetails) (bool, error) {
	txID := db.GenerateTransactionID(tx.Transaction)
	return a.vectors.HasEmbedding(ctx, txID, tx.Details.SearchBody)
}

// UpdateEmbedding updates the embedding for a single transaction
// Returns true if the embedding was created/updated, false if it was already up to date
func (a *Analyzer) UpdateEmbedding(ctx context.Context, tx *types.TransactionWithDetails) (bool, error) {
	// Generate transaction ID
	txID := db.GenerateTransactionID(tx.Transaction)

	// If the search body is empty, nothing to do
	if tx.Details.SearchBody == "" {
		a.logger.Warn("Search body is empty, skipping embedding update",
			"id", txID,
			"payee", tx.Payee,
			"merchant", tx.Details.Merchant)
		return false, nil
	}

	// Check if embedding exists in vector storage with content hash
	exists, err := a.vectors.HasEmbedding(ctx, txID, tx.Details.SearchBody)
	if err != nil {
		return false, fmt.Errorf("failed to check embedding existence: %w", err)
	}

	// If embedding already exists and is up to date, nothing to do
	if exists {
		a.logger.Debug("Embedding already exists and is up to date",
			"id", txID,
			"payee", tx.Payee,
			"merchant", tx.Details.Merchant)
		return false, nil
	} else {
		a.logger.Debug("Embedding does not exist, generating and storing",
			"id", txID,
			"payee", tx.Payee,
			"merchant", tx.Details.Merchant)
	}

	// Generate embedding
	embedding, err := a.embeddings.GenerateEmbedding(ctx, tx.Details.SearchBody)
	if err != nil {
		return false, fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Store embedding with content hash
	err = a.vectors.StoreEmbedding(ctx, txID, tx.Details.SearchBody, embedding)
	if err != nil {
		return false, fmt.Errorf("failed to store embedding: %w", err)
	}

	a.logger.Debug("Updated transaction embedding",
		"id", txID,
		"payee", tx.Payee,
		"merchant", tx.Details.Merchant)

	return true, nil
}

// UpdateAllEmbeddings updates embeddings for all transactions in the database
func (a *Analyzer) UpdateAllEmbeddings(ctx context.Context, config Config) error {
	a.logger.Info("Updating embeddings for all transactions in the database (iterator mode)")
	startTime := time.Now()

	// Count total transactions for progress bar
	totalCount, err := a.db.Count()
	if err != nil {
		a.logger.Warn("Failed to count transactions for progress bar", "error", err)
		totalCount = 0 // fallback: no progress bar
	}

	var progress Progress
	if !config.Progress || totalCount == 0 {
		progress = NewNoopProgress()
	} else {
		progress = NewBarProgress(totalCount)
	}

	var updateCount int32
	it := a.db.IterateTransactions(ctx)
	for {
		tx, ok := it.Next()
		if !ok {
			break
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		updated, err := a.UpdateEmbedding(ctx, tx)
		if err != nil {
			a.logger.Warn("Failed to update embedding",
				"error", err,
				"payee", tx.Payee)
			// Continue with other transactions
		} else if updated {
			atomic.AddInt32(&updateCount, 1)
		}

		if err := progress.Add(1); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			a.logger.Warn("Failed to update progress", "error", err)
		}
	}

	a.logger.Info("Completed embedding update for all transactions",
		"total_processed", totalCount,
		"total_updated", updateCount,
		"duration", time.Since(startTime))

	return nil
}

// TextSearch finds transactions matching the given query using full-text search
func (a *Analyzer) TextSearch(
	ctx context.Context,
	query string,
	days int,
	limit int,
) (types.SearchResults, error) {
	a.logger.Info("Performing text search",
		"query", query,
		"days", days,
		"limit", limit)
	startTime := time.Now()

	// Perform text search using the database
	searchResults, totalCount, err := a.db.SearchTransactionsByText(ctx, query, days, limit)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("failed to search transactions by text: %w", err)
	}

	a.logger.Info("Text search completed",
		"query", query,
		"results", len(searchResults),
		"total_count", totalCount,
		"duration", time.Since(startTime))

	return types.SearchResults{
		Results:    searchResults,
		TotalCount: totalCount,
		Limit:      limit,
	}, nil
}

// VectorSearch finds transactions similar to the given query using vector embeddings
func (a *Analyzer) VectorSearch(
	ctx context.Context,
	query string,
	limit int,
	threshold float32,
	days int,
) (types.SearchResults, error) {
	a.logger.Info("Performing vector search",
		"query", query,
		"limit", limit,
		"threshold", threshold,
		"days", days)
	startTime := time.Now()

	// Generate embedding for the query
	embedding, err := a.embeddings.GenerateEmbedding(ctx, query)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("failed to generate embedding for query: %w", err)
	}

	// Query similar transaction IDs from vector storage with threshold applied
	vectorResults, err := a.vectors.Query(ctx, embedding, threshold)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("failed to query similar transactions: %w", err)
	}

	a.logger.Debug("Vector search raw results",
		"query", query,
		"raw_results", len(vectorResults),
		"threshold", threshold)

	// If no results from vector search, return empty slice
	if len(vectorResults) == 0 {
		a.logger.Info("No similar transactions found", "duration", time.Since(startTime))
		return types.SearchResults{
			Results:    []types.TransactionSearchResult{},
			TotalCount: 0,
			Limit:      limit,
		}, nil
	}

	// Total count of vector results before applying date filter and limit
	totalVectorResults := len(vectorResults)

	// Fetch each transaction by ID and build result set
	var results []types.TransactionSearchResult
	var fetchErrors int
	var filteredOutByDate int

	// Calculate the cutoff date
	cutoffDate := time.Now().AddDate(0, 0, -days)

	for _, result := range vectorResults {
		// Fetch transaction by ID
		tx, err := a.db.GetTransactionByID(ctx, result.ID)
		if err != nil {
			a.logger.Warn("Failed to fetch transaction",
				"id", result.ID,
				"error", err)
			fetchErrors++
			continue
		}

		// Parse transaction date
		txDate, err := time.Parse("02/01/2006", tx.Date)
		if err != nil {
			a.logger.Warn("Failed to parse transaction date",
				"date", tx.Date,
				"error", err)
			fetchErrors++
			continue
		}

		// Filter by date if days parameter is provided
		if days > 0 && txDate.Before(cutoffDate) {
			filteredOutByDate++
			continue
		}

		// Create search result with vector score
		searchResult := types.TransactionSearchResult{
			TransactionWithDetails: *tx,
			Scores: types.SearchScore{
				VectorScore: result.Similarity,
			},
		}
		results = append(results, searchResult)

		// Stop once we have enough results
		if len(results) >= limit {
			break
		}
	}

	// Calculate total count as the sum of results, fetch errors, and date-filtered items
	totalCount := len(results)
	if len(results) >= limit || fetchErrors > 0 || filteredOutByDate > 0 {
		// If we hit the limit or had errors/filtering, use the pre-filtered count
		totalCount = totalVectorResults - fetchErrors
	}

	a.logger.Info("Vector search completed",
		"query", query,
		"results", len(results),
		"total_count", totalCount,
		"fetch_errors", fetchErrors,
		"filtered_by_date", filteredOutByDate,
		"threshold", threshold,
		"duration", time.Since(startTime))

	return types.SearchResults{
		Results:    results,
		TotalCount: totalCount,
		Limit:      limit,
	}, nil
}

// HybridSearch performs both text and vector searches and combines results using Reciprocal Rank Fusion (RRF)
func (a *Analyzer) HybridSearch(
	ctx context.Context,
	query string,
	days int,
	limit int,
	vectorThreshold float32,
) (types.SearchResults, error) {
	a.logger.Info("Performing hybrid search with Reciprocal Rank Fusion",
		"query", query,
		"days", days,
		"limit", limit,
		"vector_threshold", vectorThreshold)
	startTime := time.Now()

	// Perform text search
	textResults, textTotalCount, err := a.db.SearchTransactionsByText(ctx, query, days, limit*2) // Get more results for better fusion
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("text search failed: %w", err)
	}

	// Perform vector search
	vectorResults, err := a.VectorSearch(ctx, query, limit, vectorThreshold, days)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("vector search failed: %w", err)
	}

	a.logger.Debug("Hybrid search raw results",
		"text_results", len(textResults),
		"vector_results", len(vectorResults.Results),
		"text_total_count", textTotalCount)

	// No results from either search
	if len(textResults) == 0 && len(vectorResults.Results) == 0 {
		a.logger.Info("No results found in hybrid search", "duration", time.Since(startTime))
		return types.SearchResults{
			Results:    []types.TransactionSearchResult{},
			TotalCount: 0,
			Limit:      limit,
		}, nil
	}

	// Map of transaction IDs to their search results and rankings
	type resultInfo struct {
		result     types.TransactionSearchResult
		textRank   int // 1-based position in text results (0 if not found)
		vectorRank int // 1-based position in vector results (0 if not found)
	}

	// Constant k for RRF formula
	const k = 60 // Standard value often used in RRF

	// Build combined results using transaction ID as the key
	combinedResults := make(map[string]resultInfo)

	// Process text search results
	for i, result := range textResults {
		// Generate transaction ID
		txID := db.GenerateTransactionID(result.Transaction)

		// Store or update the result in the combined map
		if info, exists := combinedResults[txID]; exists {
			info.textRank = i + 1 // 1-based ranking
			combinedResults[txID] = info
		} else {
			combinedResults[txID] = resultInfo{
				result:     result,
				textRank:   i + 1, // 1-based ranking
				vectorRank: 0,     // Not found in vector results yet
			}
		}
	}

	// Process vector search results
	for i, result := range vectorResults.Results {
		// Generate transaction ID
		txID := db.GenerateTransactionID(result.Transaction)

		// Store or update the result in the combined map
		if info, exists := combinedResults[txID]; exists {
			info.vectorRank = i + 1 // 1-based ranking
			info.result.Scores.VectorScore = result.Scores.VectorScore
			combinedResults[txID] = info
		} else {
			combinedResults[txID] = resultInfo{
				result:     result,
				textRank:   0,     // Not found in text results
				vectorRank: i + 1, // 1-based ranking
			}
		}
	}

	// Calculate RRF scores and prepare final results
	var finalResults []types.TransactionSearchResult
	for _, info := range combinedResults {
		// Calculate RRF score using the formula: 1/(k + r) where r is the rank
		var rrfScore float64

		// Add text contribution if it exists
		if info.textRank > 0 {
			rrfScore += 1.0 / float64(k+info.textRank)
		}

		// Add vector contribution if it exists
		if info.vectorRank > 0 {
			rrfScore += 1.0 / float64(k+info.vectorRank)
		}

		// Create a copy of the result with RRF score
		result := info.result
		result.Scores.RRFScore = rrfScore

		finalResults = append(finalResults, result)
	}

	// Sort results by RRF score (highest first)
	sortSearchResultsByRRFScore(finalResults)

	// Return full results set before limiting for total count
	allResultsCount := len(finalResults)

	// Limit results if needed
	if len(finalResults) > limit {
		finalResults = finalResults[:limit]
	}

	a.logger.Info("Hybrid search completed",
		"query", query,
		"results", len(finalResults),
		"total_count", allResultsCount,
		"duration", time.Since(startTime))

	return types.SearchResults{
		Results:    finalResults,
		TotalCount: allResultsCount,
		Limit:      limit,
	}, nil
}

// sortSearchResultsByRRFScore sorts search results by their RRF score (highest first)
func sortSearchResultsByRRFScore(results []types.TransactionSearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Scores.RRFScore > results[j].Scores.RRFScore
	})
}
