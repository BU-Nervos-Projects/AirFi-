package tests

import (
	"fmt"
	"math/big"
	"testing"
	"time"
)

// ratePerMin is the rate in shannons per minute (~8.33 CKB per minute, 500 CKB = 1 hour).
var testRatePerMin = big.NewInt(833333333)

// calculateDurationFromShannons calculates session duration from funding amount in shannons.
func calculateDurationFromShannons(fundingShannons *big.Int, ratePerMin *big.Int) time.Duration {
	minutes := new(big.Int).Div(fundingShannons, ratePerMin).Int64()
	return time.Duration(minutes) * time.Minute
}

// calculateDurationFromCKB calculates session duration from funding amount in CKB.
func calculateDurationFromCKB(fundingCKB int64) time.Duration {
	shannons := fundingCKB * 100000000
	minutes := shannons / 833333333
	return time.Duration(minutes) * time.Minute
}

// formatDuration formats a duration as H:MM:SS or M:SS.
func formatDuration(d time.Duration) string {
	if d < 0 {
		return "0:00"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func TestDurationCalculation_500CKB_1Hour(t *testing.T) {
	fundingCKB := int64(500)
	expected := 60 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("500 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_250CKB_30Min(t *testing.T) {
	fundingCKB := int64(250)
	expected := 30 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("250 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_1000CKB_2Hours(t *testing.T) {
	fundingCKB := int64(1000)
	expected := 120 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("1000 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_85CKB_10Min(t *testing.T) {
	fundingCKB := int64(85)
	expected := 10 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("85 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_ZeroCKB(t *testing.T) {
	fundingCKB := int64(0)
	expected := 0 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("0 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_MinimumCKB(t *testing.T) {
	fundingCKB := int64(9)
	expected := 1 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("9 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_LargeFunding(t *testing.T) {
	fundingCKB := int64(5000)
	expected := 600 * time.Minute

	result := calculateDurationFromCKB(fundingCKB)

	if result != expected {
		t.Errorf("5000 CKB: expected %v, got %v", expected, result)
	}
}

func TestDurationCalculation_FromShannons(t *testing.T) {
	tests := []struct {
		name     string
		shannons *big.Int
		expected time.Duration
	}{
		{
			name:     "500 CKB in shannons",
			shannons: big.NewInt(50000000000),
			expected: 60 * time.Minute,
		},
		{
			name:     "250 CKB in shannons",
			shannons: big.NewInt(25000000000),
			expected: 30 * time.Minute,
		},
		{
			name:     "1000 CKB in shannons",
			shannons: big.NewInt(100000000000),
			expected: 120 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateDurationFromShannons(tt.shannons, testRatePerMin)
			if result != tt.expected {
				t.Errorf("%s: expected %v, got %v", tt.name, tt.expected, result)
			}
		})
	}
}

func TestRatePerMinute(t *testing.T) {
	ckbFor60Min := int64(500)
	shannonsFor60Min := ckbFor60Min * 100000000

	calculatedRate := shannonsFor60Min / 60
	expectedRate := int64(833333333)

	if calculatedRate != expectedRate {
		t.Errorf("Rate calculation: expected %d, got %d", expectedRate, calculatedRate)
	}
}

func TestExtendSessionAmounts(t *testing.T) {
	tests := []struct {
		name        string
		ckb         int64
		expectedMin int64
	}{
		{"10 min button (85 CKB)", 85, 10},
		{"30 min button (250 CKB)", 250, 30},
		{"1 hour button (500 CKB)", 500, 60},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration := calculateDurationFromCKB(tt.ckb)
			actualMin := int64(duration.Minutes())

			if actualMin != tt.expectedMin {
				t.Errorf("%s: expected %d minutes, got %d minutes", tt.name, tt.expectedMin, actualMin)
			}
		})
	}
}

func TestFormatDuration_Minutes(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{0, "0:00"},
		{30 * time.Second, "0:30"},
		{1 * time.Minute, "1:00"},
		{5*time.Minute + 30*time.Second, "5:30"},
		{59*time.Minute + 59*time.Second, "59:59"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v): expected %s, got %s", tt.duration, tt.expected, result)
			}
		})
	}
}

func TestFormatDuration_Hours(t *testing.T) {
	tests := []struct {
		duration time.Duration
		expected string
	}{
		{1 * time.Hour, "1:00:00"},
		{1*time.Hour + 30*time.Minute, "1:30:00"},
		{2*time.Hour + 15*time.Minute + 45*time.Second, "2:15:45"},
		{10 * time.Hour, "10:00:00"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := formatDuration(tt.duration)
			if result != tt.expected {
				t.Errorf("formatDuration(%v): expected %s, got %s", tt.duration, tt.expected, result)
			}
		})
	}
}

func TestFormatDuration_Negative(t *testing.T) {
	result := formatDuration(-5 * time.Minute)
	expected := "0:00"

	if result != expected {
		t.Errorf("formatDuration(-5m): expected %s, got %s", expected, result)
	}
}
