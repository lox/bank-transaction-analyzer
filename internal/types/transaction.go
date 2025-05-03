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

// Transaction represents a bank transaction
type Transaction struct {
	Date   string `json:"date"`
	Amount string `json:"amount"`
	Payee  string `json:"payee"`
	Bank   string `json:"bank"`
}

// TransactionDetails contains structured information extracted from a transaction
type TransactionDetails struct {
	Type        string `json:"type"`
	Merchant    string `json:"merchant"`
	Location    string `json:"location"`
	Category    string `json:"category"`
	Description string `json:"description"`
	CardNumber  string `json:"card_number"`
	SearchBody  string `json:"search_body"`

	// Optional fields
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
	Details TransactionDetails `json:"details"`
}
