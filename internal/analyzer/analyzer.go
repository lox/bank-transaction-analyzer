package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync/atomic"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	openrouter "github.com/revrost/go-openrouter"
	"github.com/revrost/go-openrouter/jsonschema"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	OpenRouterModel string
	Concurrency     int
	Progress        bool
}

type Analyzer struct {
	client     *openrouter.Client
	logger     *log.Logger
	db         *db.DB
	embeddings EmbeddingProvider
	vectors    VectorStorage
}

// NewAnalyzer creates a new transaction analyzer with explicit dependencies
func NewAnalyzer(
	client *openrouter.Client,
	logger *log.Logger,
	db *db.DB,
	embeddingProvider EmbeddingProvider,
	vectorStorage VectorStorage,
) *Analyzer {
	return &Analyzer{
		client:     client,
		logger:     logger,
		db:         db,
		embeddings: embeddingProvider,
		vectors:    vectorStorage,
	}
}

// DB returns the database connection
func (a *Analyzer) DB() *db.DB {
	return a.db
}

// AnalyzeTransactions processes transactions in parallel and stores their details
func (a *Analyzer) AnalyzeTransactions(ctx context.Context, transactions []types.Transaction, config Config) ([]types.TransactionWithDetails, error) {
	startTime := time.Now()
	a.logger.Info("Starting transaction analysis", "total_transactions", len(transactions))

	// Filter out transactions that already exist in the database
	filterStart := time.Now()
	filteredTransactions, err := a.db.FilterExistingTransactions(ctx, transactions)
	if err != nil {
		return nil, fmt.Errorf("error filtering existing transactions: %w", err)
	}
	a.logger.Info("Filtered existing transactions",
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

	// Initialize result slice with capacity for all transactions
	analyzedTransactions := make([]types.TransactionWithDetails, 0, len(transactions))

	// First, get all existing transactions
	for _, t := range transactions {
		exists, err := a.db.Has(ctx, t)
		if err != nil {
			return nil, fmt.Errorf("error checking if transaction exists: %w", err)
		}
		if exists {
			analyzed, err := a.db.Get(ctx, t)
			if err != nil {
				return nil, fmt.Errorf("error getting transaction details: %w", err)
			}
			analyzedTransactions = append(analyzedTransactions, types.TransactionWithDetails{
				Transaction: t,
				Details:     *analyzed,
			})
		}
	}

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
			details, err := a.analyzeTransaction(gCtx, t, config.OpenRouterModel)
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
			a.logger.Debug("Transaction storage completed",
				"payee", t.Payee,
				"duration", time.Since(storeStart))

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

// analyzeTransaction uses an LLM to extract structured information from a transaction
func (a *Analyzer) analyzeTransaction(ctx context.Context, t types.Transaction, model string) (*types.TransactionDetails, error) {
	startTime := time.Now()
	a.logger.Info("Analyzing transaction",
		"payee", t.Payee,
		"amount", t.Amount,
		"date", t.Date,
		"model", model)

	prompt := fmt.Sprintf(`Extract structured information from this transaction description.
Classify the transaction type and extract relevant details.

IMPORTANT RULES:
1. Remove payment processor prefixes and suffixes:
   - Remove "SQ *" (Square)
   - Remove "Visa Purchase", "EFTPOS Purchase"
   - Remove "Receipt" and receipt numbers
   - Remove "Date", "Time" and timestamps
   - Remove "Card" and card numbers
2. Extract location even if it's at the end of the merchant name
3. Use consistent categories from this list:
   - Food & Dining (restaurants, cafes, food delivery)
   - Shopping (retail stores, online shopping)
   - Transportation (Uber, taxis, public transport)
   - Entertainment (movies, events, festivals)
   - Services (utilities, subscriptions, professional services)
   - Personal Care (health, beauty, fitness)
   - Travel (flights, accommodation, travel services)
   - Education (courses, books, educational services)
   - Home (furniture, appliances, home improvement)
   - Groceries (supermarkets, food stores)
   - Bank Fees (fees, charges, interest)
   - Transfers (personal transfers, payments)
   - Other (anything that doesn't fit above categories)
4. For foreign amounts:
   - Carefully extract all foreign amount and currency information
   - If you see "Foreign Currency Amount: USD 11.99", extract amount=11.99 and currency=USD
   - Amount MUST be a valid number (integer or decimal), not a string or object
   - The amount should be just the number value without currency symbols or formatting
   - Currency must be the 3-letter currency code (USD, EUR, GBP, etc.)
   - Example: For "Foreign amount USD 29.99", use {"amount": 29.99, "currency": "USD"}
   - NEVER return an empty object {} for foreign_amount
5. Special handling for food delivery services:
   - For Uber Eats, use "Uber Eats" as merchant name
   - For DoorDash, use "DoorDash" as merchant name
   - For Menulog, use "Menulog" as merchant name
   - Category should be "Food & Dining" for all food delivery services
6. Clean merchant names:
   - Remove any domain names (e.g., .com, .co)
   - Remove any help/support text
   - Use the main brand name only
   - Use proper case formatting (e.g., "Richie's Cafe" not "RICHIES CAFE")
   - For chains, use their official capitalization (e.g., "McDonald's" not "MCDONALDS")
   - For local businesses, use title case with appropriate apostrophes
7. Description field rules:
   - NEVER include the amount in the description
   - Provide a brief description of what was purchased
   - For restaurants/cafes, describe the type of food or service
   - For retail, describe the type of items purchased
   - For services, describe the service provided
   - Keep descriptions concise but informative
8. Card number extraction:
   - If a card number is present in the format "Card XXXX...XXXX", extract it
   - Store the full card number in the card_number field
   - Do not mask or truncate the card number
9. Transfer details processing:
   - For transfer transactions, carefully extract account numbers, removing any non-essential characters
   - The to_account field should only contain the account number, not dates, amounts, or transaction details
   - For reference fields, keep only the actual reference text, not dates or amounts
   - Never include the full transaction details in the transfer_details fields
   - Ignore any narrative text that describes the transaction
   - Example: For "Transfer to BSB 123-456 Account 12345678 Reference: RENT JUN", use {"to_account": "12345678", "reference": "RENT JUN"}
10. Search body generation:
   - Generate a search_body field that contains all relevant keywords from the transaction
   - Include the merchant name, location, description, and any other relevant details
   - Use proper case formatting for the search_body

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

Transaction: %s
Amount: %s
Date: %s`,
		t.Payee, t.Amount, t.Date)

	var details *types.TransactionDetails

	// Create a custom schema that properly handles the decimal.Decimal type
	customSchema := jsonschema.Definition{
		Type: jsonschema.Object,
		Properties: map[string]jsonschema.Definition{
			"type": {
				Type: jsonschema.String,
				Enum: []string{"purchase", "transfer", "fee", "deposit", "withdrawal", "refund", "interest"},
			},
			"merchant": {
				Type: jsonschema.String,
			},
			"location": {
				Type: jsonschema.String,
			},
			"category": {
				Type: jsonschema.String,
			},
			"description": {
				Type: jsonschema.String,
			},
			"card_number": {
				Type: jsonschema.String,
			},
			"search_body": {
				Type: jsonschema.String,
			},
			"foreign_amount": {
				Type: jsonschema.Object, // Can be null through omitempty
				Properties: map[string]jsonschema.Definition{
					"amount": {
						Type:        jsonschema.Number,
						Description: "The amount in the foreign currency as a numerical value",
					},
					"currency": {
						Type:        jsonschema.String,
						Description: "The 3-letter currency code (e.g., USD, EUR, GBP)",
					},
				},
				Required: []string{"amount", "currency"},
			},
			"transfer_details": {
				Type: jsonschema.Object, // Can be null through omitempty
				Properties: map[string]jsonschema.Definition{
					"to_account": {
						Type: jsonschema.String,
					},
					"from_account": {
						Type: jsonschema.String,
					},
					"reference": {
						Type: jsonschema.String,
					},
				},
			},
		},
		Required: []string{"type", "merchant", "category", "description", "search_body"},
	}

	// Retry the API call with exponential backoff
	err := retry.Do(
		func() error {
			resp, err := a.client.CreateChatCompletion(
				ctx,
				openrouter.ChatCompletionRequest{
					Model: model,
					Messages: []openrouter.ChatCompletionMessage{
						{
							Role:    openrouter.ChatMessageRoleSystem,
							Content: openrouter.Content{Text: "You are a financial transaction parser. Extract structured data from transaction descriptions into clean JSON as specified by the schema provided. Follow these critical rules: 1) For foreign_amount, always return a number for amount, never a string or empty object. 2) For transfer_details, extract only essential account numbers and references, not dates or narrative text. 3) Make merchant names clean and consistent. 4) Generate an appropriate search_body field containing relevant keywords from the transaction. 5) Never hallucinate or add information not present in the transaction."},
						},
						{
							Role:    openrouter.ChatMessageRoleUser,
							Content: openrouter.Content{Text: prompt},
						},
					},
					MaxCompletionTokens: 500,
					Temperature:         0.1,
					ResponseFormat: &openrouter.ChatCompletionResponseFormat{
						Type: openrouter.ChatCompletionResponseFormatTypeJSONSchema,
						JSONSchema: &openrouter.ChatCompletionResponseFormatJSONSchema{
							Name:   "transaction_details",
							Schema: &customSchema,
							Strict: true,
						},
					},
				},
			)
			if err != nil {
				return retry.Unrecoverable(err)
			}

			// Validate response
			if len(resp.Choices) == 0 {
				return retry.Unrecoverable(fmt.Errorf("no choices in response"))
			}

			// Parse the details
			details = &types.TransactionDetails{}
			responseText := resp.Choices[0].Message.Content.Text
			if err := json.Unmarshal([]byte(responseText), details); err != nil {
				a.logger.Warn("Invalid JSON response, will retry",
					"error", err,
					"response", responseText)
				return fmt.Errorf("invalid JSON response: %w", err)
			}

			return nil
		},
		retry.Context(ctx),
		retry.Attempts(3),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			a.logger.Warn("Retrying API call",
				"attempt", n+1,
				"max_attempts", 3,
				"error", err,
				"payee", t.Payee)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("error getting completion: %w", err)
	}

	a.logger.Info("Successfully parsed transaction details",
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

	a.logger.Info("Transaction storage completed",
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
	// If the search body is empty, nothing to do
	if tx.Details.SearchBody == "" {
		return false, nil
	}

	// Generate transaction ID
	txID := db.GenerateTransactionID(tx.Transaction)

	// // Check if embedding exists in vector storage with content hash
	// exists, err := a.vectors.HasEmbedding(ctx, txID, tx.Details.SearchBody)
	// if err != nil {
	// 	return false, fmt.Errorf("failed to check embedding existence: %w", err)
	// }

	// // If embedding already exists and is up to date, nothing to do
	// if exists {
	// 	a.logger.Debug("Embedding already exists and is up to date",
	// 		"id", txID,
	// 		"payee", tx.Payee,
	// 		"merchant", tx.Details.Merchant)
	// 	return false, nil
	// }

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

// UpdateEmbeddings updates embeddings for the provided transactions
func (a *Analyzer) UpdateEmbeddings(ctx context.Context, transactions []types.TransactionWithDetails, config Config) error {
	if len(transactions) == 0 {
		return nil
	}

	a.logger.Info("Updating embeddings for provided transactions", "count", len(transactions))
	startTime := time.Now()

	// Set up progress tracking
	var progress Progress
	if !config.Progress {
		progress = NewNoopProgress()
	} else {
		progress = NewBarProgress(len(transactions))
	}

	// Track the number of updated transactions
	var updateCount int32

	// Process each transaction one by one
	for _, tx := range transactions {
		// Check if context is canceled
		if err := ctx.Err(); err != nil {
			return err
		}

		// Update embedding for this transaction
		updated, err := a.UpdateEmbedding(ctx, &tx)
		if err != nil {
			a.logger.Warn("Failed to update embedding",
				"error", err,
				"payee", tx.Payee)
			// Continue with other transactions
		} else if updated {
			// Increment update counter if embedding was updated
			atomic.AddInt32(&updateCount, 1)
		}

		// Update progress
		if err := progress.Add(1); err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			a.logger.Warn("Failed to update progress", "error", err)
		}
	}

	a.logger.Info("Completed embedding update for provided transactions",
		"total_processed", len(transactions),
		"total_updated", updateCount,
		"duration", time.Since(startTime))

	return nil
}

// VectorSearch finds transactions similar to the given query using vector embeddings
func (a *Analyzer) VectorSearch(
	ctx context.Context,
	query string,
	limit int,
	threshold float32,
) ([]types.TransactionSearchResult, error) {
	a.logger.Info("Performing vector search",
		"query", query,
		"limit", limit,
		"threshold", threshold)
	startTime := time.Now()

	// Generate embedding for the query
	embedding, err := a.embeddings.GenerateEmbedding(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to generate embedding for query: %w", err)
	}

	// Query similar transaction IDs from vector storage with threshold applied
	similarResults, err := a.vectors.QuerySimilar(ctx, embedding, limit, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to query similar transactions: %w", err)
	}

	a.logger.Debug("Vector search raw results",
		"query", query,
		"raw_results", len(similarResults),
		"threshold", threshold)

	// If no results from vector search, return empty slice
	if len(similarResults) == 0 {
		a.logger.Info("No similar transactions found", "duration", time.Since(startTime))
		return []types.TransactionSearchResult{}, nil
	}

	// Fetch each transaction by ID and build result set
	var results []types.TransactionSearchResult
	var fetchErrors int

	for _, result := range similarResults {
		// Fetch transaction by ID
		tx, err := a.db.GetTransactionByID(ctx, result.ID)
		if err != nil {
			a.logger.Warn("Failed to fetch transaction",
				"id", result.ID,
				"error", err)
			fetchErrors++
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
	}

	// Sort by vector score (highest first)
	sortSearchResultsByVectorScore(results)

	// Limit results if needed (shouldn't be necessary as we already limited at the database level,
	// but keeping as a safety measure)
	if len(results) > limit {
		results = results[:limit]
	}

	a.logger.Info("Vector search completed",
		"query", query,
		"results", len(results),
		"fetch_errors", fetchErrors,
		"threshold", threshold,
		"duration", time.Since(startTime))

	return results, nil
}

// sortSearchResultsByVectorScore sorts search results by their vector similarity score (highest first)
func sortSearchResultsByVectorScore(results []types.TransactionSearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Scores.VectorScore > results[j].Scores.VectorScore
	})
}
