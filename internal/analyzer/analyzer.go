package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	err = a.UpdateEmbedding(ctx, &tx)
	if err != nil {
		a.logger.Warn("Failed to update embedding during transaction storage", "error", err)
		// Continue anyway, as storing the transaction in the DB was successful
	}

	a.logger.Debug("Transaction storage completed",
		"payee", t.Payee,
		"total_duration", time.Since(startTime))

	return nil
}

// UpdateEmbedding updates the embedding for a single transaction
func (a *Analyzer) UpdateEmbedding(ctx context.Context, tx *types.TransactionWithDetails) error {
	// Generate transaction ID
	txID := db.GenerateTransactionID(tx.Transaction)

	// Check if embedding exists in vector storage with content hash
	exists, metadata, err := a.vectors.HasEmbedding(ctx, txID)
	if err != nil {
		return fmt.Errorf("failed to check embedding existence: %w", err)
	}

	// If embedding exists, check if it's up to date
	if exists {
		if metadata.MatchContent(tx.Details.SearchBody) {
			a.logger.Debug("Embedding already exists and is up to date",
				"id", txID,
				"payee", tx.Payee,
				"merchant", tx.Details.Merchant)
			return nil
		} else {
			a.logger.Warn("Embedding exists but content does not match",
				"id", txID,
				"payee", tx.Payee,
				"merchant", tx.Details.Merchant,
				"content", tx.Details.SearchBody,
				"metadata", metadata)
		}

		// Remove the embedding
		err = a.vectors.RemoveEmbedding(ctx, txID)
		if err != nil {
			return fmt.Errorf("failed to remove embedding: %w", err)
		}
	}

	// Generate embedding
	embedding, err := a.embeddings.GenerateEmbedding(ctx, tx.Details.SearchBody)
	if err != nil {
		return fmt.Errorf("failed to generate embedding: %w", err)
	}

	// Store embedding
	err = a.vectors.StoreEmbedding(ctx, txID, tx.Details.SearchBody, embedding, embeddings.EmbeddingMetadata{
		ContentHash: embeddings.Hash(tx.Details.SearchBody),
		ModelName:   a.embeddings.GetEmbeddingModelName(),
		Length:      len(embedding),
		LastUpdated: time.Now(),
	})
	if err != nil {
		return fmt.Errorf("failed to store embedding: %w", err)
	}

	a.logger.Debug("Updated transaction embedding",
		"id", txID,
		"payee", tx.Payee,
		"merchant", tx.Details.Merchant)

	return nil
}

// UpdateMissingEmbeddings updates embeddings for all transactions in the database
func (a *Analyzer) UpdateMissingEmbeddings(ctx context.Context, config Config) error {
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

		err := a.UpdateEmbedding(ctx, tx)
		if err != nil {
			a.logger.Warn("Failed to update embedding",
				"error", err,
				"payee", tx.Payee)
			// Continue with other transactions
		} else {
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
