package x402

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func testLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}

func TestLedger_CreditAndReserve(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	// Credit an agent
	if err := l.Credit("agent1", 1000); err != nil {
		t.Fatalf("Credit failed: %v", err)
	}

	// Check balance
	entry, err := l.GetBalance("agent1")
	if err != nil {
		t.Fatalf("GetBalance failed: %v", err)
	}
	if entry.Balance != 1000 {
		t.Fatalf("expected balance 1000, got %d", entry.Balance)
	}

	// Reserve
	resID, err := l.Reserve("agent1", 400)
	if err != nil {
		t.Fatalf("Reserve failed: %v", err)
	}
	if resID == "" {
		t.Fatal("expected non-empty reservation ID")
	}

	// Balance should be reduced
	entry, _ = l.GetBalance("agent1")
	if entry.Balance != 600 {
		t.Fatalf("expected balance 600 after reservation, got %d", entry.Balance)
	}
	if entry.Reserved != 400 {
		t.Fatalf("expected reserved 400, got %d", entry.Reserved)
	}
}

func TestLedger_InsufficientBalance(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	l.Credit("agent1", 100)

	_, err := l.Reserve("agent1", 200)
	if err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance, got %v", err)
	}
}

func TestLedger_UnknownAgent(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	_, err := l.Reserve("unknown", 100)
	if err != ErrInsufficientBalance {
		t.Fatalf("expected ErrInsufficientBalance for unknown agent, got %v", err)
	}

	entry, _ := l.GetBalance("unknown")
	if entry != nil {
		t.Fatal("expected nil entry for unknown agent")
	}
}

func TestLedger_Commit(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	l.Credit("agent1", 1000)
	resID, _ := l.Reserve("agent1", 500)

	// Commit with less than reserved — refund the difference
	if err := l.Commit(resID, 300); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	entry, _ := l.GetBalance("agent1")
	// Started with 1000, reserved 500 (balance=500), committed 300 (refund 200), balance=700
	if entry.Balance != 700 {
		t.Fatalf("expected balance 700 after commit, got %d", entry.Balance)
	}
	if entry.Reserved != 0 {
		t.Fatalf("expected reserved 0 after commit, got %d", entry.Reserved)
	}
}

func TestLedger_Release(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	l.Credit("agent1", 1000)
	resID, _ := l.Reserve("agent1", 500)

	// Release — full refund
	if err := l.Release(resID); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	entry, _ := l.GetBalance("agent1")
	if entry.Balance != 1000 {
		t.Fatalf("expected balance 1000 after release, got %d", entry.Balance)
	}
}

func TestLedger_CommitNotFound(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	if err := l.Commit("nonexistent", 100); err != ErrReservationNotFound {
		t.Fatalf("expected ErrReservationNotFound, got %v", err)
	}
}

func TestLedger_Snapshot(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())

	l.Credit("agent1", 1000)
	l.Credit("agent2", 2000)
	l.Reserve("agent1", 300)

	snap := l.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 agents in snapshot, got %d", len(snap))
	}
	if snap["agent1"].Balance != 700 {
		t.Fatalf("expected agent1 balance 700, got %d", snap["agent1"].Balance)
	}
	if snap["agent2"].Balance != 2000 {
		t.Fatalf("expected agent2 balance 2000, got %d", snap["agent2"].Balance)
	}
}

func TestLedger_ConcurrentReserveCommit(t *testing.T) {
	l := NewSpendableLedger(5*time.Minute, testLogger())
	l.Credit("agent1", 100000)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resID, err := l.Reserve("agent1", 100)
			if err != nil {
				return // insufficient balance is ok in concurrent scenario
			}
			l.Commit(resID, 50) // charge 50, refund 50
		}()
	}
	wg.Wait()

	entry, _ := l.GetBalance("agent1")
	// All 100 reservations of 100 each, committed at 50 each
	// Total charged: 100 * 50 = 5000
	// Remaining: 100000 - 5000 = 95000
	if entry.Balance != 95000 {
		t.Fatalf("expected balance 95000, got %d", entry.Balance)
	}
}

func TestLedger_CleanupExpired(t *testing.T) {
	l := NewSpendableLedger(1*time.Millisecond, testLogger())

	l.Credit("agent1", 1000)
	l.Reserve("agent1", 500)

	// Wait for reservation to expire
	time.Sleep(10 * time.Millisecond)

	l.cleanupExpired()

	entry, _ := l.GetBalance("agent1")
	// Reservation expired, balance restored
	if entry.Balance != 1000 {
		t.Fatalf("expected balance 1000 after cleanup, got %d", entry.Balance)
	}
	if entry.Reserved != 0 {
		t.Fatalf("expected reserved 0 after cleanup, got %d", entry.Reserved)
	}
}
