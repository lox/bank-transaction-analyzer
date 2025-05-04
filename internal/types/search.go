package types

// SearchScore represents the relevance scores from text search
type SearchScore struct {
	TextScore float64 `json:"text_score,omitempty"` // BM25 score from full-text search
}

// TransactionSearchResult represents a transaction with its search relevance score
type TransactionSearchResult struct {
	TransactionWithDetails
	Scores SearchScore `json:"scores"`
}
