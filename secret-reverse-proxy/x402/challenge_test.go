package x402

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestChallengeBuilder_Build402Response(t *testing.T) {
	cb := NewChallengeBuilder("https://portal.example.com/api/agent/add-funds")

	w := httptest.NewRecorder()
	err := cb.Build402Response(w, 0, 10000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != 402 {
		t.Errorf("expected status 402, got %d", w.Code)
	}

	if w.Header().Get("Payment-Required") != "x402" {
		t.Errorf("expected Payment-Required header 'x402', got %q", w.Header().Get("Payment-Required"))
	}

	var challenge Challenge
	if err := json.NewDecoder(w.Body).Decode(&challenge); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if challenge.Error != "Insufficient balance" {
		t.Errorf("unexpected error: %q", challenge.Error)
	}
	if challenge.BalanceUSDC != "0.000000" {
		t.Errorf("expected balance_usdc '0.000000', got %q", challenge.BalanceUSDC)
	}
	if challenge.RequiredUSDC != "0.010000" {
		t.Errorf("expected required_usdc '0.010000', got %q", challenge.RequiredUSDC)
	}
	if challenge.TopupAmountUSDC != "0.010000" {
		t.Errorf("expected topup_amount_usdc '0.010000', got %q", challenge.TopupAmountUSDC)
	}
	if challenge.TopupURL != "https://portal.example.com/api/agent/add-funds" {
		t.Errorf("unexpected topup_url: %q", challenge.TopupURL)
	}
}

func TestChallengeBuilder_PartialBalance(t *testing.T) {
	cb := NewChallengeBuilder("https://portal.example.com/api/agent/add-funds")

	w := httptest.NewRecorder()
	_ = cb.Build402Response(w, 5000, 10000)

	var challenge Challenge
	json.NewDecoder(w.Body).Decode(&challenge)

	if challenge.TopupAmountUSDC != "0.005000" {
		t.Errorf("expected topup_amount_usdc '0.005000', got %q", challenge.TopupAmountUSDC)
	}
	if challenge.BalanceUSDC != "0.005000" {
		t.Errorf("expected balance_usdc '0.005000', got %q", challenge.BalanceUSDC)
	}
}

func TestMinorToUSDC(t *testing.T) {
	tests := []struct {
		minor    int64
		expected string
	}{
		{0, "0.000000"},
		{1, "0.000001"},
		{10000, "0.010000"},
		{20000, "0.020000"},
		{1000000, "1.000000"},
		{1500000, "1.500000"},
	}

	for _, tc := range tests {
		got := minorToUSDC(tc.minor)
		if got != tc.expected {
			t.Errorf("minorToUSDC(%d) = %q, want %q", tc.minor, got, tc.expected)
		}
	}
}
