package types

import "github.com/shopspring/decimal"

const (
	TransactionTypeOther     = "other"
	TransactionCategoryOther = "other"
)

// TransactionType represents the type of transaction
type TransactionType struct {
	Name      string
	Guideline string
}

// AllowedTypes is a list of all allowed transaction types
var AllowedTypes = []TransactionType{
	{Name: "purchase", Guideline: "For retail transactions, subscriptions, and general spending"},
	{Name: "transfer", Guideline: "For bank transfers, payments to individuals"},
	{Name: "fee", Guideline: "For bank fees, account fees, interest charges"},
	{Name: "deposit", Guideline: "For money received or added to account"},
	{Name: "withdrawal", Guideline: "For ATM withdrawals or manual withdrawals"},
	{Name: "refund", Guideline: "For refunded purchases or returns"},
	{Name: "interest", Guideline: "For interest earned on accounts"},
	{Name: "credit", Guideline: "For positive adjustments to your account excluding refunds or interest"},
	{Name: "other", Guideline: "For anything that doesn't fit other categories"},
}

// AllowedTypesMap is a map of all allowed transaction types
var AllowedTypesMap = map[string]struct{}{
	"purchase":   {},
	"transfer":   {},
	"fee":        {},
	"deposit":    {},
	"withdrawal": {},
	"refund":     {},
	"interest":   {},
	"credit":     {},
	"other":      {},
}

// TransactionCategory represents the spending category of a transaction
type TransactionCategory struct {
	Name      string
	Guideline string
}

// AllowedCategories is a list of all allowed transaction categories
var AllowedCategories = []TransactionCategory{
	{Name: "Food & Dining", Guideline: "restaurants, cafes, food delivery"},
	{Name: "Shopping", Guideline: "retail stores, online shopping"},
	{Name: "Transportation", Guideline: "Uber, taxis, public transport"},
	{Name: "Entertainment", Guideline: "movies, events, festivals"},
	{Name: "Services", Guideline: "utilities, subscriptions, professional services"},
	{Name: "Personal Care", Guideline: "health, beauty, fitness"},
	{Name: "Travel", Guideline: "flights, accommodation, travel services"},
	{Name: "Education", Guideline: "courses, books, educational services"},
	{Name: "Home", Guideline: "furniture, appliances, home improvement"},
	{Name: "Groceries", Guideline: "supermarkets, food stores"},
	{Name: "Bank Fees", Guideline: "fees, charges, interest"},
	{Name: "Transfers", Guideline: "personal transfers, payments"},
	{Name: "Other", Guideline: "anything that doesn't fit other categories"},
}

// AllowedCategoriesMap is a map of all allowed transaction categories
var AllowedCategoriesMap = map[string]struct{}{
	"Food & Dining":  {},
	"Shopping":       {},
	"Transportation": {},
	"Entertainment":  {},
	"Services":       {},
	"Personal Care":  {},
	"Travel":         {},
	"Education":      {},
	"Home":           {},
	"Groceries":      {},
	"Bank Fees":      {},
	"Transfers":      {},
	"Other":          {},
}

// Transaction represents a bank transaction
type Transaction struct {
	Date   string `json:"date"`
	Amount string `json:"amount"`
	Payee  string `json:"payee"`
	Bank   string `json:"bank"`
}

// ForeignAmountDetails contains details about a foreign currency amount
type ForeignAmountDetails struct {
	Amount   decimal.Decimal `json:"amount" jsonschema:"required"`
	Currency string          `json:"currency" jsonschema:"required"`
}

// TransferDetails contains details about a bank transfer
type TransferDetails struct {
	ToAccount   string `json:"to_account,omitempty"`
	FromAccount string `json:"from_account,omitempty"`
	Reference   string `json:"reference,omitempty"`
}

// TransactionDetails contains structured information extracted from a transaction
type TransactionDetails struct {
	Type        string `json:"type"`
	Merchant    string `json:"merchant"`
	Location    string `json:"location,omitempty"`
	Category    string `json:"category"`
	Description string `json:"description"`
	CardNumber  string `json:"card_number,omitempty"`
	SearchBody  string `json:"search_body"`

	// Optional fields
	ForeignAmount   *ForeignAmountDetails `json:"foreign_amount,omitempty"`
	TransferDetails *TransferDetails      `json:"transfer_details,omitempty"`
	Tags            string                `json:"tags,omitempty"`
}

type TransactionWithDetails struct {
	Transaction
	Details TransactionDetails `json:"details"`
}
