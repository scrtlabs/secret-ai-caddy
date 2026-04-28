package x402

import (
	"testing"
)

func TestQuoteEngine_Estimate(t *testing.T) {
	pricing := &PricingConfig{
		Default: ModelPricing{
			InputCostPer1kTokens:  10,
			OutputCostPer1kTokens: 30,
		},
		Models: map[string]ModelPricing{
			"llama3.3:70b": {
				InputCostPer1kTokens:  15,
				OutputCostPer1kTokens: 45,
			},
		},
	}

	q := NewQuoteEngine(pricing, "uscrt", testLogger())

	// Test with known model
	quote, err := q.Estimate("llama3.3:70b", 1000, 2000)
	if err != nil {
		t.Fatalf("Estimate failed: %v", err)
	}
	if quote.InputCost != 15 { // (1000 * 15 + 999) / 1000 = 15
		t.Fatalf("expected input cost 15, got %d", quote.InputCost)
	}
	if quote.OutputCost != 90 { // (2000 * 45 + 999) / 1000 = 90
		t.Fatalf("expected output cost 90, got %d", quote.OutputCost)
	}
	if quote.TotalCost != 105 {
		t.Fatalf("expected total cost 105, got %d", quote.TotalCost)
	}

	// Test with unknown model (uses default)
	quote, err = q.Estimate("unknown-model", 500, 1000)
	if err != nil {
		t.Fatalf("Estimate failed: %v", err)
	}
	if quote.InputCost != 5 { // (500 * 10 + 999) / 1000 = 5
		t.Fatalf("expected input cost 5, got %d", quote.InputCost)
	}
	if quote.OutputCost != 30 { // (1000 * 30 + 999) / 1000 = 30
		t.Fatalf("expected output cost 30, got %d", quote.OutputCost)
	}
	if quote.Currency != "uscrt" {
		t.Fatalf("expected currency uscrt, got %s", quote.Currency)
	}
}

func TestQuoteEngine_ZeroTokens(t *testing.T) {
	pricing := &PricingConfig{
		Default: ModelPricing{
			InputCostPer1kTokens:  10,
			OutputCostPer1kTokens: 30,
		},
		Models: make(map[string]ModelPricing),
	}
	q := NewQuoteEngine(pricing, "uscrt", testLogger())

	quote, err := q.Estimate("any", 0, 0)
	if err != nil {
		t.Fatalf("Estimate failed: %v", err)
	}
	if quote.TotalCost != 0 {
		t.Fatalf("expected total cost 0 for zero tokens, got %d", quote.TotalCost)
	}
}

func TestQuoteEngine_CeilingDivision(t *testing.T) {
	// Test that small token counts round up
	pricing := &PricingConfig{
		Default: ModelPricing{
			InputCostPer1kTokens:  10,
			OutputCostPer1kTokens: 30,
		},
		Models: make(map[string]ModelPricing),
	}
	q := NewQuoteEngine(pricing, "uscrt", testLogger())

	quote, _ := q.Estimate("any", 1, 0)
	// (1 * 10 + 999) / 1000 = 1 (ceiling division)
	if quote.InputCost != 1 {
		t.Fatalf("expected input cost 1 for 1 token, got %d", quote.InputCost)
	}
}

func TestLoadPricing_Default(t *testing.T) {
	pricing, err := LoadPricing("")
	if err != nil {
		t.Fatalf("LoadPricing failed: %v", err)
	}
	if pricing.Default.InputCostPer1kTokens != 10 {
		t.Fatalf("expected default input cost 10, got %d", pricing.Default.InputCostPer1kTokens)
	}
}
