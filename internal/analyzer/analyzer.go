package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	"github.com/sashabaranov/go-openai"
	"golang.org/x/sync/errgroup"
)

type Config struct {
	Model       string
	Concurrency int
	Progress    bool
}

type Analyzer struct {
	client     *openai.Client
	logger     *log.Logger
	db         *db.DB
	embeddings EmbeddingProvider
}

type AnalyzedTransaction struct {
	types.Transaction
	Details *types.TransactionDetails
}

// NewAnalyzer creates a new transaction parser
func NewAnalyzer(client *openai.Client, logger *log.Logger, db *db.DB) (*Analyzer, error) {
	// Create embedding provider
	config := NewLlamaCppConfig().WithLogger(logger)
	embeddings, err := NewLlamaCppEmbeddingProvider(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create embedding provider: %w", err)
	}

	return &Analyzer{
		client:     client,
		logger:     logger,
		db:         db,
		embeddings: embeddings,
	}, nil
}

// DB returns the database connection
func (a *Analyzer) DB() *db.DB {
	return a.db
}

// AnalyzeTransactions processes transactions in parallel and stores their details
func (a *Analyzer) AnalyzeTransactions(ctx context.Context, transactions []types.Transaction, config Config) ([]AnalyzedTransaction, error) {
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
	analyzedTransactions := make([]AnalyzedTransaction, 0, len(transactions))

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
			analyzedTransactions = append(analyzedTransactions, AnalyzedTransaction{
				Transaction: t,
				Details:     analyzed,
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
			details, err := a.analyzeTransaction(gCtx, t, config.Model)
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
			analyzedTransactions = append(analyzedTransactions, AnalyzedTransaction{
				Transaction: t,
				Details:     details,
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
   - amount must be a number (float) without currency symbol
   - currency must be the 3-letter currency code
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

Transaction: %s
Amount: %s
Date: %s

Return the information in JSON format with these fields:
- type: One of: purchase, transfer, fee, deposit, withdrawal, refund, interest
- merchant: The main merchant or business name (clean, without location, receipt info, or payment processor prefixes)
- location: The location where the transaction occurred (if present)
- category: A general category from the list above
- description: Clean description without receipt numbers, card details, or payment processor info
- card_number: The full card number if present in the format "Card XXXX...XXXX"
- foreign_amount: If there's a foreign currency, include amount (as number) and currency code
- transfer_details: For transfers, include to_account, from_account, and reference
- search_body: A concatenated string of all relevant searchable information, including merchant, location, description, and any other relevant details. This should be a single string that can be used for full-text search.`,
		t.Payee, t.Amount, t.Date)

	var details *types.TransactionDetails
	var resp openai.ChatCompletionResponse

	// Retry the OpenAI API call with exponential backoff
	err := retry.Do(
		func() error {
			var err error
			resp, err = a.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model: model,
				Messages: []openai.ChatCompletionMessage{
					{
						Role:    openai.ChatMessageRoleSystem,
						Content: "You are a financial transaction parser. Classify transactions and extract structured information. Follow the rules strictly, especially about removing payment processor prefixes.",
					},
					{
						Role:    openai.ChatMessageRoleUser,
						Content: prompt,
					},
				},
				ResponseFormat: &openai.ChatCompletionResponseFormat{
					Type: openai.ChatCompletionResponseFormatTypeJSONObject,
				},
			})
			if err != nil {
				// Check if this is a retryable error
				if openaiErr, ok := err.(*openai.APIError); ok {
					// Retry on server errors, rate limits, and gateway errors
					if openaiErr.HTTPStatusCode >= 500 ||
						openaiErr.HTTPStatusCode == 429 ||
						openaiErr.HTTPStatusCode == 502 ||
						openaiErr.HTTPStatusCode == 503 ||
						openaiErr.HTTPStatusCode == 504 {
						a.logger.Warn("OpenAI API error, will retry",
							"status_code", openaiErr.HTTPStatusCode,
							"error", openaiErr.Message)
						return err
					}
				}
				// For other errors, don't retry
				return retry.Unrecoverable(err)
			}

			// Validate response
			if len(resp.Choices) == 0 {
				return retry.Unrecoverable(fmt.Errorf("no choices in response"))
			}

			// Try to unmarshal the response to validate it's valid JSON
			var testDetails types.TransactionDetails
			if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), &testDetails); err != nil {
				a.logger.Warn("Invalid JSON response, will retry",
					"error", err,
					"response", resp.Choices[0].Message.Content)
				return fmt.Errorf("invalid JSON response: %w", err)
			}

			return nil
		},
		retry.Context(ctx),
		retry.Attempts(3),
		retry.DelayType(retry.BackOffDelay),
		retry.OnRetry(func(n uint, err error) {
			a.logger.Warn("Retrying OpenAI API call",
				"attempt", n+1,
				"max_attempts", 3,
				"error", err,
				"payee", t.Payee)
		}),
	)

	if err != nil {
		return nil, fmt.Errorf("error getting completion: %w", err)
	}

	details = &types.TransactionDetails{}
	if err := json.Unmarshal([]byte(resp.Choices[0].Message.Content), details); err != nil {
		return nil, fmt.Errorf("error unmarshaling response: %w", err)
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

	// Store in database without embedding
	err := a.db.Store(ctx, t, details)
	if err != nil {
		return fmt.Errorf("failed to store transaction: %w", err)
	}

	a.logger.Info("Transaction storage completed",
		"payee", t.Payee,
		"total_duration", time.Since(startTime))

	return nil
}
