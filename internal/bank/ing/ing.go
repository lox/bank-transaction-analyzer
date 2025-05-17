package ing

import (
	"context"
	"io"

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

// AdditionalPromptRules returns ING-specific rules for prompt injection
func (i *ING) AdditionalPromptRules() string {
	return `
- For ING transfer transactions, the full BSB and account number after 'To' (e.g., 'To 033134 452177') should be extracted as the to_account field, formatted as '033134 452177'.
- Never use the receipt number as a reference unless it appears after 'Ref' or 'Reference'.
- If both BSB and account are present, combine them as 'BSB ACCOUNT' (e.g., '033134 452177').
- Ignore repeated or duplicated transaction text in the payee field.
- Do not use the receipt number as the to_account or reference unless explicitly indicated.
`
}

// Ensure ING implements the Bank interface
var _ bank.Bank = (*ING)(nil)
