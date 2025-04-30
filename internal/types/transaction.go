package types

import "github.com/shopspring/decimal"

// TransactionType represents the type of transaction
type TransactionType string

const (
	TransactionTypePurchase   TransactionType = "purchase"
	TransactionTypeTransfer   TransactionType = "transfer"
	TransactionTypeFee        TransactionType = "fee"
	TransactionTypeDeposit    TransactionType = "deposit"
	TransactionTypeWithdrawal TransactionType = "withdrawal"
	TransactionTypeRefund     TransactionType = "refund"
	TransactionTypeInterest   TransactionType = "interest"
)

// Transaction represents a generic transaction, independent of the input format
type Transaction struct {
	Date   string
	Amount string
	Payee  string
}

// TransactionDetails contains structured information extracted from a transaction
type TransactionDetails struct {
	Type          TransactionType `json:"type"`
	Merchant      string          `json:"merchant"`
	Location      string          `json:"location,omitempty"`
	Category      string          `json:"category,omitempty"`
	Description   string          `json:"description,omitempty"`
	CardNumber    string          `json:"card_number,omitempty"`
	SearchBody    string          `json:"search_body,omitempty"`
	ForeignAmount *struct {
		Amount   decimal.Decimal `json:"amount"`
		Currency string          `json:"currency"`
	} `json:"foreign_amount,omitempty"`
	TransferDetails *struct {
		ToAccount   string `json:"to_account,omitempty"`
		FromAccount string `json:"from_account,omitempty"`
		Reference   string `json:"reference,omitempty"`
	} `json:"transfer_details,omitempty"`
}

type TransactionWithDetails struct {
	Transaction
	Details TransactionDetails
}
