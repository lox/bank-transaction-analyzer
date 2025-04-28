package analyzer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/avast/retry-go/v4"
	"github.com/charmbracelet/log"
	"github.com/lox/ing-transaction-parser/internal/qif"
	"github.com/sashabaranov/go-openai"
	"golang.org/x/sync/errgroup"
)

// TransactionGroup represents a group of similar transactions
type TransactionGroup struct {
	Transactions []qif.Transaction
	Similarity   float64
}

type AnalysisConfig struct {
	EmbeddingModel string
	AnalysisModel  string
	Concurrency    int
	LLMClient      *openai.Client
}

// AnalyzeTransactions uses embeddings to find similar transactions and then analyzes them with GPT-4
func AnalyzeTransactions(ctx context.Context, client *openai.Client, transactions []qif.Transaction, progress Progress, logger *log.Logger, store *EmbeddingStore, config AnalysisConfig) (string, error) {
	// Ensure the LLMClient is set
	if config.LLMClient == nil {
		config.LLMClient = client
	}

	// Check which transactions need embeddings
	status, err := store.GetEmbeddingStatus(ctx, transactions)
	if err != nil {
		return "", fmt.Errorf("error checking embedding status: %v", err)
	}

	// Count how many we need to generate
	var toGenerate []int
	for i, exists := range status {
		if !exists {
			toGenerate = append(toGenerate, i)
		}
	}

	if len(toGenerate) == 0 {
		logger.Info("All embeddings already exist")
		return "", nil
	}

	logger.Info("Starting embedding generation", "count", len(toGenerate), "model", config.EmbeddingModel, "concurrency", config.Concurrency)

	// Generate and store embeddings
	if err := generateEmbeddings(ctx, transactions, config, logger, store, progress); err != nil {
		return "", fmt.Errorf("error generating embeddings: %v", err)
	}

	// Check if context was canceled during embedding generation
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("operation interrupted during embedding generation")
	}

	logger.Info("Embedding generation complete", "count", len(toGenerate))

	// Group similar transactions
	groups, err := groupSimilarTransactions(transactions, store)
	if err != nil {
		return "", fmt.Errorf("error grouping similar transactions: %v", err)
	}

	// Analyze each group
	var analysis strings.Builder
	for i, group := range groups {
		if len(group.Transactions) < 2 {
			continue // Skip single transactions
		}

		groupAnalysis, err := analyzeGroup(ctx, client, group, config.AnalysisModel)
		if err != nil {
			return "", fmt.Errorf("error analyzing group %d: %v", i, err)
		}

		analysis.WriteString(fmt.Sprintf("\nGroup %d (Similarity: %.2f):\n", i+1, group.Similarity))
		analysis.WriteString(groupAnalysis)
		analysis.WriteString("\n---\n")
	}

	return analysis.String(), nil
}

// generateEmbeddings generates embeddings for transactions in parallel and stores them as they are generated
func generateEmbeddings(ctx context.Context, transactions []qif.Transaction, config AnalysisConfig, logger *log.Logger, store *EmbeddingStore, progress Progress) error {
	// Check if context is already canceled
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("context canceled before starting embedding generation")
	}

	// Create an errgroup with the specified concurrency
	g, gCtx := errgroup.WithContext(ctx)
	g.SetLimit(config.Concurrency)

	// Process transactions in parallel
	for i, t := range transactions {
		i, t := i, t // Create new variables for the goroutine
		g.Go(func() error {
			// Check if context is canceled before starting
			if err := gCtx.Err(); err != nil {
				return err
			}

			// Create the embedding text using only the payee
			text := t.Payee
			logger.Debug("Generating embedding", "transaction", i, "text", text)

			var resp openai.EmbeddingResponse
			// Use retry-go to handle retries with exponential backoff
			err := retry.Do(
				func() error {
					// Create a new context with a timeout for each attempt
					requestCtx, cancel := context.WithTimeout(gCtx, 30*time.Second)
					defer cancel()

					// Generate the embedding
					var err error
					resp, err = config.LLMClient.CreateEmbeddings(requestCtx, openai.EmbeddingRequest{
						Model: openai.EmbeddingModel(config.EmbeddingModel),
						Input: []string{text},
					})
					if err != nil {
						// Check if it was the parent context that was canceled
						if gCtx.Err() != nil {
							return retry.Unrecoverable(fmt.Errorf("embedding generation interrupted"))
						}
						return err
					}
					return nil
				},
				retry.Context(gCtx),
				retry.Attempts(3),
				retry.Delay(5*time.Second),
				retry.DelayType(retry.BackOffDelay),
				retry.OnRetry(func(n uint, err error) {
					logger.Warn("Retrying embedding generation", "transaction", i, "attempt", n+1, "error", err)
				}),
				retry.RetryIf(func(err error) bool {
					// Don't retry if the context was canceled
					return !errors.Is(err, context.Canceled)
				}),
			)

			if err != nil {
				if errors.Is(err, context.Canceled) {
					return fmt.Errorf("embedding generation interrupted")
				}
				return fmt.Errorf("error creating embedding for transaction %d after retries: %v", i, err)
			}

			// Store the embedding immediately
			if err := store.StoreEmbedding(gCtx, t, resp.Data[0].Embedding); err != nil {
				return fmt.Errorf("error storing embedding for transaction %d: %v", i, err)
			}

			// Update progress
			if err := progress.Add(1); err != nil {
				return fmt.Errorf("error updating progress: %v", err)
			}

			return nil
		})
	}

	// Wait for all goroutines to complete
	if err := g.Wait(); err != nil {
		// If the error is due to context cancellation, return a more specific error
		if errors.Is(err, context.Canceled) {
			return fmt.Errorf("embedding generation interrupted")
		}
		return fmt.Errorf("error generating embeddings: %v", err)
	}

	return nil
}

func groupSimilarTransactions(transactions []qif.Transaction, store *EmbeddingStore) ([]TransactionGroup, error) {
	groups := make([]TransactionGroup, 0)
	used := make(map[int]bool)

	for i, t := range transactions {
		if used[i] {
			continue
		}

		// Find similar transactions using the embedding store
		similarTransactions, similarities, err := store.FindSimilarTransactions(context.Background(), t, len(transactions))
		if err != nil {
			return nil, fmt.Errorf("error finding similar transactions: %v", err)
		}

		// Create a group with the current transaction and its similar ones
		group := TransactionGroup{
			Transactions: []qif.Transaction{t},
		}

		// Add similar transactions to the group
		for j, similarT := range similarTransactions {
			// Skip the first one as it's the same as t
			if j == 0 {
				continue
			}

			// Find the index of the similar transaction in our original list
			for k, origT := range transactions {
				if origT.Date == similarT.Date && origT.Payee == similarT.Payee && origT.Amount == similarT.Amount {
					if !used[k] {
						group.Transactions = append(group.Transactions, similarT)
						used[k] = true
					}
					break
				}
			}
		}

		// Only add groups with more than one transaction
		if len(group.Transactions) > 1 {
			// Calculate average similarity
			var sum float64
			for _, s := range similarities[1:] { // Skip first similarity as it's 1.0 (self)
				sum += float64(s)
			}
			group.Similarity = sum / float64(len(similarities)-1)
			groups = append(groups, group)
		}

		used[i] = true
	}

	return groups, nil
}

func analyzeGroup(ctx context.Context, client *openai.Client, group TransactionGroup, model string) (string, error) {
	// Convert group to JSON for the prompt
	groupJSON, err := json.Marshal(group.Transactions)
	if err != nil {
		log.Error("Failed to marshal group", "error", err)
		return "", fmt.Errorf("error marshaling group: %v", err)
	}

	log.Debug("Analyzing transaction group", "size", len(group.Transactions), "similarity", group.Similarity)

	prompt := fmt.Sprintf(`Analyze these similar transactions and determine if they are recurring charges.
Consider:
1. The frequency of transactions
2. The consistency of amounts
3. The similarity of payee names

Transactions:
%s

Please provide a clear analysis of whether these are recurring charges and explain your reasoning.`, groupJSON)

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "You are a financial analysis assistant. Analyze groups of similar transactions to identify recurring charges.",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
	})
	if err != nil {
		log.Error("Failed to get completion", "error", err)
		return "", fmt.Errorf("error getting completion: %v", err)
	}

	log.Debug("Analysis completed for group")
	return resp.Choices[0].Message.Content, nil
}
