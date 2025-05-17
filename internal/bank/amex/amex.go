package amex

import (
	"context"
	"io"

	"github.com/lox/bank-transaction-analyzer/internal/bank"
	"github.com/lox/bank-transaction-analyzer/internal/qif"
	"github.com/lox/bank-transaction-analyzer/internal/types"
)

// Amex represents the American Express bank implementation
type Amex struct{}

// New creates a new Amex bank implementation
func New() *Amex {
	return &Amex{}
}

// Name returns the name of the bank
func (a *Amex) Name() string {
	return "amex"
}

// ParseTransactions parses transactions from a QIF file
func (a *Amex) ParseTransactions(ctx context.Context, r io.Reader) ([]types.Transaction, error) {
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
			Bank:   a.Name(),
		}
	}

	return transactions, nil
}

// AdditionalPromptRules returns Amex-specific rules for prompt injection (none for now)
func (a *Amex) AdditionalPromptRules() string {
	return `
- For positive amounts like "DIRECT DEBIT RECEIVED", categorize as type="payment" and category="Credit Card Payment" instead of deposit/Other.
- When merchant contains "DIRECT DEBIT", set description to "Credit card payment from bank account".
- These transactions represent payments received from a bank account to pay down credit card balance.
- Ensure the relationship with the corresponding bank payment is clear.
`
}

// Ensure Amex implements the Bank interface
var _ bank.Bank = (*Amex)(nil)
