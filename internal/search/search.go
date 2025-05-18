package search

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/log"
	"github.com/lox/bank-transaction-analyzer/internal/db"
	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

type SearchOrder string

const (
	searchOrderRelevance SearchOrder = "relevance"
	searchOrderDate      SearchOrder = "date"

	defaultVectorThreshold = 0.4
)

// SearchOptions defines options for all search types
type searchOptions struct {
	limit           int
	days            int
	orderBy         SearchOrder
	vectorThreshold float32
	dateCutoff      *time.Time
}

// SearchOption is a function that modifies SearchOptions
type SearchOption func(*searchOptions)

// WithLimit sets the limit for search
func WithLimit(limit int) SearchOption {
	return func(opts *searchOptions) {
		opts.limit = limit
	}
}

// WithDays sets the days filter for search
func WithDays(days int) SearchOption {
	return func(opts *searchOptions) {
		opts.days = days
	}
}

// WithVectorThreshold sets the threshold for vector search
func WithVectorThreshold(threshold float32) SearchOption {
	return func(opts *searchOptions) {
		opts.vectorThreshold = threshold
	}
}

// OrderByRelevance sets the order by relevance
func OrderByRelevance() SearchOption {
	return func(opts *searchOptions) {
		opts.orderBy = db.OrderByRelevance
	}
}

// OrderByDate sets the order by date
func OrderByDate() SearchOption {
	return func(opts *searchOptions) {
		opts.orderBy = db.OrderByDate
	}
}

// WithDateCutoff sets an explicit cutoff date for search (overrides days)
func WithDateCutoff(cutoff time.Time) SearchOption {
	return func(opts *searchOptions) {
		opts.dateCutoff = &cutoff
	}
}

// TextSearch performs a full-text search on transactions using a query and SearchOptions
func TextSearch(ctx context.Context, dbConn *db.DB, query string, opts ...SearchOption) ([]types.TransactionSearchResult, int, error) {
	var searchOpts searchOptions
	for _, opt := range opts {
		opt(&searchOpts)
	}

	// Map SearchOptions to db.TransactionQueryOptions
	dbOpts := []db.TransactionQueryOption{}
	if searchOpts.days > 0 {
		dbOpts = append(dbOpts, db.FilterByDays(searchOpts.days))
	}
	if searchOpts.limit > 0 {
		dbOpts = append(dbOpts, db.WithLimit(searchOpts.limit))
	}

	orderBy := db.OrderByDate
	if searchOpts.orderBy == searchOrderRelevance {
		orderBy = db.OrderByRelevance
	}

	return dbConn.SearchTransactionsByText(ctx, query, orderBy, dbOpts...)
}

// VectorSearch finds transactions similar to the given query using vector embeddings
func VectorSearch(
	ctx context.Context,
	logger *log.Logger,
	dbConn *db.DB,
	embeddingsProvider embeddings.EmbeddingProvider,
	vectors embeddings.VectorStorage,
	query string,
	opts ...SearchOption,
) (types.SearchResults, error) {
	options := searchOptions{
		orderBy:         db.OrderByDate,
		vectorThreshold: defaultVectorThreshold,
	}
	for _, opt := range opts {
		opt(&options)
	}

	logger.Info("Performing vector search", "query", query, "options", options)
	startTime := time.Now()

	// Generate embedding for the query
	embedding, err := embeddingsProvider.GenerateEmbedding(ctx, query)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("failed to generate embedding for query: %w", err)
	}

	// Query similar transaction IDs from vector storage with threshold applied
	vectorResults, err := vectors.Query(ctx, embedding, options.vectorThreshold)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("failed to query similar transactions: %w", err)
	}

	logger.Debug("Vector search raw results",
		"query", query,
		"raw_results", len(vectorResults),
		"threshold", options.vectorThreshold)

	// If no results from vector search, return empty slice
	if len(vectorResults) == 0 {
		logger.Info("No similar transactions found", "duration", time.Since(startTime))
		return types.SearchResults{
			Results:    []types.TransactionSearchResult{},
			TotalCount: 0,
			Limit:      options.limit,
		}, nil
	}

	// Total count of vector results before applying date filter and limit
	totalVectorResults := len(vectorResults)

	// Fetch each transaction by ID and build result set
	var results []types.TransactionSearchResult
	var fetchErrors int
	var filteredOutByDate int

	// Calculate the cutoff date
	var cutoffDate *time.Time
	if options.dateCutoff != nil {
		cutoffDate = options.dateCutoff
	} else if options.days > 0 {
		t := time.Now().AddDate(0, 0, -options.days)
		cutoffDate = &t
	} else {
		cutoffDate = nil
	}

	for _, result := range vectorResults {
		// Fetch transaction by ID
		tx, err := dbConn.GetTransactionByID(ctx, result.ID)
		if err != nil {
			// Currently no way to purge these from the vector storage proactively, so delete if we encounter
			err = vectors.RemoveEmbedding(ctx, result.ID)
			if err != nil {
				logger.Warn("Failed to remove embedding",
					"id", result.ID,
					"error", err)
			}
			continue
		}

		// Parse transaction date
		txDate, err := time.Parse("02/01/2006", tx.Date)
		if err != nil {
			logger.Warn("Failed to parse transaction date",
				"date", tx.Date,
				"error", err)
			fetchErrors++
			continue
		}

		// If a cutoff is set, always filter; otherwise, only filter if days > 0
		if cutoffDate != nil && txDate.Before(*cutoffDate) {
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
		if options.limit > 0 && len(results) >= options.limit {
			break
		}
	}

	// Sort results by orderBy
	switch options.orderBy {
	case searchOrderDate:
		sort.Slice(results, func(i, j int) bool {
			di, _ := time.Parse("02/01/2006", results[i].Date)
			dj, _ := time.Parse("02/01/2006", results[j].Date)
			return di.After(dj)
		})
	default: // searchOrderRelevance
		sort.Slice(results, func(i, j int) bool {
			return results[i].Scores.VectorScore > results[j].Scores.VectorScore
		})
	}

	// Calculate total count as the sum of results, fetch errors, and date-filtered items
	totalCount := len(results)
	if (options.limit > 0 && len(results) >= options.limit) || fetchErrors > 0 || filteredOutByDate > 0 {
		totalCount = totalVectorResults - fetchErrors
	}

	logger.Info("Vector search completed",
		"query", query,
		"results", len(results),
		"total_count", totalCount,
		"fetch_errors", fetchErrors,
		"filtered_by_date", filteredOutByDate,
		"threshold", options.vectorThreshold,
		"orderBy", options.orderBy,
		"duration", time.Since(startTime))

	return types.SearchResults{
		Results:    results,
		TotalCount: totalCount,
		Limit:      options.limit,
	}, nil
}

// HybridSearch performs both text and vector searches and combines results using Reciprocal Rank Fusion (RRF)
func HybridSearch(
	ctx context.Context,
	logger *log.Logger,
	dbConn *db.DB,
	embeddingsProvider embeddings.EmbeddingProvider,
	vectors embeddings.VectorStorage,
	query string,
	opts ...SearchOption,
) (types.SearchResults, error) {
	options := searchOptions{
		orderBy:         searchOrderRelevance,
		vectorThreshold: defaultVectorThreshold,
	}
	for _, opt := range opts {
		opt(&options)
	}

	logger.Info("Performing hybrid search with Reciprocal Rank Fusion", "query", query, "options", options)
	startTime := time.Now()

	// Perform text search
	textResults, textTotalCount, err := dbConn.SearchTransactionsByText(ctx,
		query, db.OrderByRelevance, db.FilterByDays(options.days), db.WithLimit(options.limit*2))
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("text search failed: %w", err)
	}

	// Pass dateCutoff to VectorSearch if set
	vOpts := opts
	if options.dateCutoff != nil {
		vOpts = append(vOpts, WithDateCutoff(*options.dateCutoff))
	}

	// Perform vector search
	vectorResults, err := VectorSearch(ctx, logger, dbConn, embeddingsProvider, vectors, query, vOpts...)
	if err != nil {
		return types.SearchResults{}, fmt.Errorf("vector search failed: %w", err)
	}

	logger.Debug("Hybrid search raw results",
		"text_results", len(textResults),
		"vector_results", len(vectorResults.Results),
		"text_total_count", textTotalCount)

	// No results from either search
	if len(textResults) == 0 && len(vectorResults.Results) == 0 {
		logger.Info("No results found in hybrid search", "duration", time.Since(startTime))
		return types.SearchResults{
			Results:    []types.TransactionSearchResult{},
			TotalCount: 0,
			Limit:      options.limit,
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

	// Sort results by orderBy
	switch options.orderBy {
	case searchOrderDate:
		sort.Slice(finalResults, func(i, j int) bool {
			di, _ := time.Parse("02/01/2006", finalResults[i].Date)
			dj, _ := time.Parse("02/01/2006", finalResults[j].Date)
			return di.After(dj)
		})
	default: // searchOrderRelevance
		sortSearchResultsByRRFScore(finalResults)
	}

	// Return full results set before limiting for total count
	allResultsCount := len(finalResults)

	// Limit results if needed
	if options.limit > 0 && len(finalResults) > options.limit {
		finalResults = finalResults[:options.limit]
	}

	logger.Info("Hybrid search completed",
		"query", query,
		"results", len(finalResults),
		"total_count", allResultsCount,
		"duration", time.Since(startTime))

	return types.SearchResults{
		Results:    finalResults,
		TotalCount: allResultsCount,
		Limit:      options.limit,
	}, nil
}

// sortSearchResultsByRRFScore sorts search results by their RRF score (highest first)
func sortSearchResultsByRRFScore(results []types.TransactionSearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Scores.RRFScore > results[j].Scores.RRFScore
	})
}
