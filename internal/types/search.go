package types

// SearchScore represents the relevance scores from different search methods
type SearchScore struct {
	TextScore   float64 `json:"text_score,omitempty"`   // BM25 score from full-text search
	VectorScore float32 `json:"vector_score,omitempty"` // Cosine similarity score from vector search
}

// TransactionSearchResult represents a transaction with its search relevance score
type TransactionSearchResult struct {
	TransactionWithDetails
	Scores SearchScore `json:"scores"`
}
