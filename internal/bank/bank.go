package bank

import (
	"context"
	"io"

	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

// Bank represents a bank implementation that can parse and process transactions
type Bank interface {
	// Name returns the name of the bank
	Name() string

	// ParseTransactions parses transactions from a QIF file
	ParseTransactions(ctx context.Context, r io.Reader) ([]types.Transaction, error)

	// ProcessTransactions processes transactions using the analyzer
	ProcessTransactions(ctx context.Context, transactions []types.Transaction, an *analyzer.Analyzer, config analyzer.Config) ([]types.TransactionWithDetails, error)
}

// Registry maintains a list of available bank implementations
type Registry struct {
	banks map[string]Bank
}

// NewRegistry creates a new bank registry
func NewRegistry() *Registry {
	return &Registry{
		banks: make(map[string]Bank),
	}
}

// Register adds a bank implementation to the registry
func (r *Registry) Register(b Bank) {
	r.banks[b.Name()] = b
}

// Get returns a bank implementation by name
func (r *Registry) Get(name string) (Bank, bool) {
	b, ok := r.banks[name]
	return b, ok
}

// List returns a list of all registered bank names
func (r *Registry) List() []string {
	names := make([]string, 0, len(r.banks))
	for name := range r.banks {
		names = append(names, name)
	}
	return names
}
