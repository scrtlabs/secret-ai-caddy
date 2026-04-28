package x402

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestChallengeBuilder_Build402Response(t *testing.T) {
	cb := NewChallengeBuilder("https://portal.example.com/fund", "uscrt")
	w := httptest.NewRecorder()

	err := cb.Build402Response(w, "agent-123", 5000)
	if err != nil {
		t.Fatalf("Build402Response failed: %v", err)
	}

	// Check status code
	if w.Code != 402 {
		t.Fatalf("expected status 402, got %d", w.Code)
	}

	// Check headers
	if w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}
	if w.Header().Get("X-Payment-Required") != "true" {
		t.Fatal("expected X-Payment-Required header")
	}

	// Check body
	var challenge Challenge
	if err := json.Unmarshal(w.Body.Bytes(), &challenge); err != nil {
		t.Fatalf("failed to unmarshal response: %v", err)
	}
	if challenge.AgentAddress != "agent-123" {
		t.Fatalf("expected agent-123, got %s", challenge.AgentAddress)
	}
	if challenge.RequiredAmount != 5000 {
		t.Fatalf("expected required amount 5000, got %d", challenge.RequiredAmount)
	}
	if challenge.Currency != "uscrt" {
		t.Fatalf("expected currency uscrt, got %s", challenge.Currency)
	}
	if challenge.PaymentURL != "https://portal.example.com/fund" {
		t.Fatalf("expected payment URL, got %s", challenge.PaymentURL)
	}
	if challenge.ChallengeRef == "" {
		t.Fatal("expected non-empty challenge ref")
	}
}
