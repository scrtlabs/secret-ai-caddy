package x402

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestChallengeBuilder_Build402Response(t *testing.T) {
	cb := NewChallengeBuilder("https://portal.example.com/api/agent/add-funds")

	w := httptest.NewRecorder()
	err := cb.Build402Response(w, 0, 0.01)
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
	if challenge.BalanceUSD != "0.000000" {
		t.Errorf("expected balance_usd '0.000000', got %q", challenge.BalanceUSD)
	}
	if challenge.RequiredUSD != "0.010000" {
		t.Errorf("expected required_usd '0.010000', got %q", challenge.RequiredUSD)
	}
	if challenge.TopupAmountUSD != "0.010000" {
		t.Errorf("expected topup_amount_usd '0.010000', got %q", challenge.TopupAmountUSD)
	}
	if challenge.TopupURL != "https://portal.example.com/api/agent/add-funds" {
		t.Errorf("unexpected topup_url: %q", challenge.TopupURL)
	}
}

func TestChallengeBuilder_PartialBalance(t *testing.T) {
	cb := NewChallengeBuilder("https://portal.example.com/api/agent/add-funds")

	w := httptest.NewRecorder()
	_ = cb.Build402Response(w, 0.005, 0.01)

	var challenge Challenge
	json.NewDecoder(w.Body).Decode(&challenge)

	if challenge.TopupAmountUSD != "0.005000" {
		t.Errorf("expected topup_amount_usd '0.005000', got %q", challenge.TopupAmountUSD)
	}
	if challenge.BalanceUSD != "0.005000" {
		t.Errorf("expected balance_usd '0.005000', got %q", challenge.BalanceUSD)
	}
}
