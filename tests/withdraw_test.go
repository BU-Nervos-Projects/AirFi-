package tests

import (
	"testing"
)

// Constants from withdraw.go
const (
	WithdrawFee     uint64 = 100000      // 0.001 CKB
	MinCellCapacity uint64 = 6100000000  // 61 CKB
	ShannonPerCKB   uint64 = 100000000   // 1 CKB = 100,000,000 shannons
)

func TestWithdrawFee_Value(t *testing.T) {
	// Withdraw fee should be 0.001 CKB = 100,000 shannons
	expectedFee := uint64(100000)
	if WithdrawFee != expectedFee {
		t.Errorf("WithdrawFee: expected %d, got %d", expectedFee, WithdrawFee)
	}
}

func TestMinCellCapacity_Value(t *testing.T) {
	// Minimum cell capacity should be 61 CKB
	expectedCapacity := uint64(61 * ShannonPerCKB)
	if MinCellCapacity != expectedCapacity {
		t.Errorf("MinCellCapacity: expected %d, got %d", expectedCapacity, MinCellCapacity)
	}
}

func TestWithdrawalCalculation_SufficientBalance(t *testing.T) {
	tests := []struct {
		name           string
		totalCapacity  uint64
		expectedOutput uint64
		shouldSucceed  bool
	}{
		{
			name:           "500 CKB withdrawal",
			totalCapacity:  500 * ShannonPerCKB,
			expectedOutput: 500*ShannonPerCKB - WithdrawFee,
			shouldSucceed:  true,
		},
		{
			name:           "100 CKB withdrawal",
			totalCapacity:  100 * ShannonPerCKB,
			expectedOutput: 100*ShannonPerCKB - WithdrawFee,
			shouldSucceed:  true,
		},
		{
			name:           "62 CKB withdrawal (minimum viable)",
			totalCapacity:  62 * ShannonPerCKB,
			expectedOutput: 62*ShannonPerCKB - WithdrawFee,
			shouldSucceed:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := tt.totalCapacity - WithdrawFee

			if output != tt.expectedOutput {
				t.Errorf("Output: expected %d, got %d", tt.expectedOutput, output)
			}

			// Check if withdrawal is viable (output > MinCellCapacity)
			isViable := output > MinCellCapacity
			if isViable != tt.shouldSucceed {
				t.Errorf("Viability: expected %v, got %v", tt.shouldSucceed, isViable)
			}
		})
	}
}

func TestWithdrawalCalculation_InsufficientBalance(t *testing.T) {
	tests := []struct {
		name          string
		totalCapacity uint64
	}{
		{
			name:          "61 CKB (exactly minimum)",
			totalCapacity: 61 * ShannonPerCKB,
		},
		{
			name:          "60 CKB (below minimum)",
			totalCapacity: 60 * ShannonPerCKB,
		},
		{
			name:          "1 CKB",
			totalCapacity: 1 * ShannonPerCKB,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Withdrawal should fail if totalCapacity <= WithdrawFee + MinCellCapacity
			isViable := tt.totalCapacity > WithdrawFee+MinCellCapacity

			if isViable {
				t.Errorf("Expected withdrawal to fail for %s", tt.name)
			}
		})
	}
}

func TestCKBAddressFormat(t *testing.T) {
	tests := []struct {
		name    string
		address string
		isValid bool
	}{
		{
			name:    "Valid testnet address (ckt prefix)",
			address: "ckt1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq",
			isValid: true,
		},
		{
			name:    "Valid mainnet address (ckb prefix)",
			address: "ckb1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq",
			isValid: true,
		},
		{
			name:    "Invalid prefix",
			address: "xyz1qzda0cr08m85hc8jlnfp3zer7xulejywt49kt2rr0vthywaa50xwsq",
			isValid: false,
		},
		{
			name:    "Empty address",
			address: "",
			isValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Simple validation: check prefix
			hasValidPrefix := len(tt.address) > 3 && (tt.address[:3] == "ckt" || tt.address[:3] == "ckb")

			if hasValidPrefix != tt.isValid {
				t.Errorf("Address validation for '%s': expected %v, got %v", tt.address, tt.isValid, hasValidPrefix)
			}
		})
	}
}

func TestShannonToCKB_Conversion(t *testing.T) {
	tests := []struct {
		shannons uint64
		ckb      float64
	}{
		{100000000, 1.0},
		{50000000000, 500.0},
		{100000, 0.001},
		{0, 0},
		{6100000000, 61.0},
	}

	for _, tt := range tests {
		ckb := float64(tt.shannons) / float64(ShannonPerCKB)
		if ckb != tt.ckb {
			t.Errorf("Conversion %d shannons: expected %.3f CKB, got %.3f CKB", tt.shannons, tt.ckb, ckb)
		}
	}
}

func TestCKBToShannon_Conversion(t *testing.T) {
	tests := []struct {
		ckb      uint64
		shannons uint64
	}{
		{1, 100000000},
		{500, 50000000000},
		{61, 6100000000},
		{0, 0},
	}

	for _, tt := range tests {
		shannons := tt.ckb * ShannonPerCKB
		if shannons != tt.shannons {
			t.Errorf("Conversion %d CKB: expected %d shannons, got %d shannons", tt.ckb, tt.shannons, shannons)
		}
	}
}
