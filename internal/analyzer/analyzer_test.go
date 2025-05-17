package analyzer

import (
	"context"
	"testing"

	"github.com/lox/bank-transaction-analyzer/internal/embeddings"
	"github.com/lox/bank-transaction-analyzer/internal/types"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockEmbeddingProvider is a mock implementation of EmbeddingProvider
type MockEmbeddingProvider struct{}

func (m *MockEmbeddingProvider) GenerateEmbedding(ctx context.Context, text string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

// MockVectorStorage is a mock implementation of VectorStorage
type MockVectorStorage struct{}

func (m *MockVectorStorage) StoreEmbedding(ctx context.Context, id string, content string, embedding []float32) error {
	return nil
}

func (m *MockVectorStorage) HasEmbedding(ctx context.Context, id string, content string) (bool, error) {
	return false, nil
}

func (m *MockVectorStorage) Query(ctx context.Context, embedding []float32, threshold float32) ([]embeddings.VectorResult, error) {
	return []embeddings.VectorResult{}, nil
}

func TestValidateTransactionDetails(t *testing.T) {
	tests := []struct {
		name        string
		details     types.TransactionDetails
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid_basic",
			details: types.TransactionDetails{
				Type:        "purchase",
				Merchant:    "Test Store",
				Category:    "Shopping",
				Description: "Test purchase",
				SearchBody:  "Test purchase at Test Store",
			},
			expectError: false,
		},
		{
			name: "valid_with_foreign_amount",
			details: types.TransactionDetails{
				Type:        "purchase",
				Merchant:    "Test International",
				Category:    "Shopping",
				Description: "Test international purchase",
				SearchBody:  "Test international purchase at Test International",
				ForeignAmount: &types.ForeignAmountDetails{
					Amount:   decimal.NewFromFloat(10.00),
					Currency: "USD",
				},
			},
			expectError: false,
		},
		{
			name: "valid_transfer_with_details",
			details: types.TransactionDetails{
				Type:        "transfer",
				Merchant:    "John Doe",
				Category:    "Transfers",
				Description: "Rent payment",
				SearchBody:  "Transfer to John Doe for rent",
				TransferDetails: &types.TransferDetails{
					ToAccount: "123456789",
					Reference: "Rent payment May",
				},
			},
			expectError: false,
		},
		{
			name: "valid_bpay_payment",
			details: types.TransactionDetails{
				Type:        "transfer",
				Merchant:    "BPAY",
				Category:    "Services",
				Description: "BPAY bill payment",
				SearchBody:  "BPAY bill payment utility",
				TransferDetails: &types.TransferDetails{
					ToAccount: "376000797121007",
					Reference: "0000775466",
				},
			},
			expectError: false,
		},
		{
			name: "valid_bpay_payment_without_transfer_details",
			details: types.TransactionDetails{
				Type:        "transfer",
				Merchant:    "BPAY",
				Category:    "Services",
				Description: "BPAY bill payment",
				SearchBody:  "BPAY bill payment",
			},
			expectError: false,
		},
		{
			name: "invalid_type",
			details: types.TransactionDetails{
				Type:        "invalid_type",
				Merchant:    "Test Store",
				Category:    "Shopping",
				Description: "Test purchase",
				SearchBody:  "Test purchase at Test Store",
			},
			expectError: true,
			errorMsg:    "type='invalid_type'",
		},
		{
			name: "invalid_category",
			details: types.TransactionDetails{
				Type:        "purchase",
				Merchant:    "Test Store",
				Category:    "InvalidCategory",
				Description: "Test purchase",
				SearchBody:  "Test purchase at Test Store",
			},
			expectError: true,
			errorMsg:    "category='InvalidCategory'",
		},
		{
			name: "invalid_currency_code",
			details: types.TransactionDetails{
				Type:        "purchase",
				Merchant:    "Test International",
				Category:    "Shopping",
				Description: "Test international purchase",
				SearchBody:  "Test international purchase at Test International",
				ForeignAmount: &types.ForeignAmountDetails{
					Amount:   decimal.NewFromFloat(10.00),
					Currency: "INVALID",
				},
			},
			expectError: true,
			errorMsg:    "foreign_amount.currency='INVALID'",
		},
		{
			name: "transfer_without_details",
			details: types.TransactionDetails{
				Type:        "transfer",
				Merchant:    "John Doe",
				Category:    "Transfers",
				Description: "Rent payment",
				SearchBody:  "Transfer to John Doe for rent",
			},
			expectError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateTransactionDetails(&tc.details)
			if tc.expectError {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errorMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
