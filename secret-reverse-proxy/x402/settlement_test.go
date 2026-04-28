package x402

import (
	"testing"
	"time"
)

func newTestSettlement() (*SettlementEngineImpl, *LedgerImpl) {
	logger := testLogger()
	ledger := NewSpendableLedger(5*time.Minute, logger)
	pricing := &PricingConfig{
		Default: ModelPricing{
			InputCostPer1kTokens:  10,
			OutputCostPer1kTokens: 30,
		},
		Models: make(map[string]ModelPricing),
	}
	quoteEngine := NewQuoteEngine(pricing, "uscrt", logger)
	settlement := NewSettlementEngine(ledger, quoteEngine, logger)
	return settlement, ledger
}

func TestSettlement_NormalSettle(t *testing.T) {
	s, l := newTestSettlement()

	l.Credit("agent1", 10000)
	resID, _ := l.Reserve("agent1", 500)

	result, err := s.Settle(resID, 100, 200, "test-model")
	if err != nil {
		t.Fatalf("Settle failed: %v", err)
	}

	if result.EstimatedCost != 500 {
		t.Fatalf("expected estimated cost 500, got %d", result.EstimatedCost)
	}
	// Actual: (100*10+999)/1000 + (200*30+999)/1000 = 1 + 6 = 7
	if result.ActualCost != 7 {
		t.Fatalf("expected actual cost 7, got %d", result.ActualCost)
	}
	if result.Refunded != 493 {
		t.Fatalf("expected refunded 493, got %d", result.Refunded)
	}

	entry, _ := l.GetBalance("agent1")
	// Started 10000, reserved 500 (bal=9500), committed 7 (refund 493), bal=9993
	if entry.Balance != 9993 {
		t.Fatalf("expected balance 9993, got %d", entry.Balance)
	}
}

func TestSettlement_Cancel(t *testing.T) {
	s, l := newTestSettlement()

	l.Credit("agent1", 10000)
	resID, _ := l.Reserve("agent1", 500)

	if err := s.Cancel(resID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	entry, _ := l.GetBalance("agent1")
	if entry.Balance != 10000 {
		t.Fatalf("expected balance 10000 after cancel, got %d", entry.Balance)
	}
}

func TestSettlement_NotFound(t *testing.T) {
	s, _ := newTestSettlement()

	_, err := s.Settle("nonexistent", 100, 200, "model")
	if err != ErrReservationNotFound {
		t.Fatalf("expected ErrReservationNotFound, got %v", err)
	}
}
