package types

// SearchScore represents the relevance scores from different search methods
type SearchScore struct {
	VectorScore float64 `json:"vector_score,omitempty"` // Cosine similarity score from vector search
	TextScore   float64 `json:"text_score,omitempty"`   // BM25 score from full-text search
	RRFScore    float64 `json:"rrf_score,omitempty"`    // Combined Reciprocal Rank Fusion score
}

// TransactionSearchResult represents a transaction with its search relevance scores
type TransactionSearchResult struct {
	TransactionWithDetails
	Scores SearchScore `json:"scores"`
}
