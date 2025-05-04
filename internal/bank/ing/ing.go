package ing

import (
	"context"
	"io"

	"github.com/lox/bank-transaction-analyzer/internal/analyzer"
	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/qif"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

// ING represents the ING Australia bank implementation
type ING struct{}

// New creates a new ING Australia bank implementation
func New() *ING {
	return &ING{}
}

// Name returns the name of the bank
func (i *ING) Name() string {
	return "ing-australia"
}

// ParseTransactions parses transactions from a QIF file
func (i *ING) ParseTransactions(ctx context.Context, r io.Reader) ([]types.Transaction, error) {
	// Parse the QIF file
	qifTransactions, err := qif.ParseReader(r)
	if err != nil {
		return nil, err
	}

	// Convert QIF transactions to our internal type
	transactions := make([]types.Transaction, len(qifTransactions))
	for idx, t := range qifTransactions {
		transactions[idx] = types.Transaction{
			Date:   t.Date,
			Amount: t.Amount,
			Payee:  t.Payee,
			Bank:   i.Name(),
		}
	}

	return transactions, nil
}

// ProcessTransactions processes transactions using the analyzer
func (i *ING) ProcessTransactions(ctx context.Context, transactions []types.Transaction, an *analyzer.Analyzer, config analyzer.Config) ([]types.TransactionWithDetails, error) {
	return an.AnalyzeTransactions(ctx, transactions, config)
}

// Ensure ING implements the Bank interface
var _ bank.Bank = (*ING)(nil)
