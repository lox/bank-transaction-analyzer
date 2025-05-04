package types

// SearchScore represents the relevance scores from different search methods
type SearchScore struct {
	TextScore   float64 `json:"text_score,omitempty"`   // BM25 score from full-text search
	VectorScore float32 `json:"vector_score,omitempty"` // Cosine similarity score from vector search
	RRFScore    float64 `json:"rrf_score,omitempty"`    // Reciprocal Rank Fusion score (combined ranking)
}

// TransactionSearchResult represents a transaction with its search relevance score
type TransactionSearchResult struct {
	TransactionWithDetails
	Scores SearchScore `json:"scores"`
}

// SearchResults wraps search results with metadata about the total count
type SearchResults struct {
	Results    []TransactionSearchResult `json:"results"`     // Limited set of search results
	TotalCount int                       `json:"total_count"` // Total count of all matching results
	Limit      int                       `json:"limit"`       // Limit that was applied to the search
}
