package x402

import (
	"sync"
	"time"

	"github.com/rs/xid"
	"go.uber.org/zap"
)

// agentEntry holds per-agent state with its own mutex for fine-grained locking.
type agentEntry struct {
	mu           sync.Mutex
	balance      int64 // available funds (excludes reserved)
	reservations map[string]*Reservation
	updatedAt    time.Time
}

// sumReserved returns the total reserved amount across all reservations.
func (a *agentEntry) sumReserved() int64 {
	var total int64
	for _, r := range a.reservations {
		total += r.Amount
	}
	return total
}

// LedgerImpl is the in-memory implementation of SpendableLedger.
type LedgerImpl struct {
	mu      sync.RWMutex
	agents  map[string]*agentEntry
	ttl     time.Duration
	logger  *zap.Logger
	stopCh  chan struct{}
}

// NewSpendableLedger creates a new in-memory SpendableLedger.
func NewSpendableLedger(reservationTTL time.Duration, logger *zap.Logger) *LedgerImpl {
	return &LedgerImpl{
		agents: make(map[string]*agentEntry),
		ttl:    reservationTTL,
		logger: logger,
	}
}

// getOrCreateAgent returns an existing agent entry or creates a new one.
// Caller must hold at least a read lock on l.mu; if creating, caller must hold write lock.
func (l *LedgerImpl) getAgent(address string) *agentEntry {
	return l.agents[address]
}

func (l *LedgerImpl) getOrCreateAgent(address string) *agentEntry {
	if entry, ok := l.agents[address]; ok {
		return entry
	}
	entry := &agentEntry{
		reservations: make(map[string]*Reservation),
		updatedAt:    time.Now(),
	}
	l.agents[address] = entry
	return entry
}

// Credit adds funds to an agent's available balance.
func (l *LedgerImpl) Credit(agentAddress string, amount int64) error {
	l.mu.Lock()
	agent := l.getOrCreateAgent(agentAddress)
	l.mu.Unlock()

	agent.mu.Lock()
	defer agent.mu.Unlock()

	agent.balance += amount
	agent.updatedAt = time.Now()

	l.logger.Debug("Credited agent balance",
		zap.String("agent", agentAddress),
		zap.Int64("amount", amount),
		zap.Int64("new_balance", agent.balance))
	return nil
}

// Reserve attempts to hold amount from the agent's available balance.
// Returns a reservation ID or ErrInsufficientBalance.
func (l *LedgerImpl) Reserve(agentAddress string, amount int64) (string, error) {
	l.mu.RLock()
	agent := l.getAgent(agentAddress)
	l.mu.RUnlock()

	if agent == nil {
		return "", ErrInsufficientBalance
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()

	if agent.balance < amount {
		return "", ErrInsufficientBalance
	}

	id := xid.New().String()
	agent.balance -= amount
	agent.reservations[id] = &Reservation{
		ID:           id,
		AgentAddress: agentAddress,
		Amount:       amount,
		CreatedAt:    time.Now(),
	}

	l.logger.Debug("Created reservation",
		zap.String("reservation_id", id),
		zap.String("agent", agentAddress),
		zap.Int64("amount", amount),
		zap.Int64("remaining_balance", agent.balance))
	return id, nil
}

// Commit finalizes a reservation, refunding the difference between reserved and actual.
func (l *LedgerImpl) Commit(reservationID string, actualAmount int64) error {
	agent, reservation := l.findReservation(reservationID)
	if agent == nil || reservation == nil {
		return ErrReservationNotFound
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()

	refund := reservation.Amount - actualAmount
	if refund > 0 {
		agent.balance += refund
	}
	delete(agent.reservations, reservationID)
	agent.updatedAt = time.Now()

	l.logger.Debug("Committed reservation",
		zap.String("reservation_id", reservationID),
		zap.Int64("reserved", reservation.Amount),
		zap.Int64("actual", actualAmount),
		zap.Int64("refunded", refund),
		zap.Int64("new_balance", agent.balance))
	return nil
}

// Release cancels a reservation, returning the full reserved amount to balance.
func (l *LedgerImpl) Release(reservationID string) error {
	agent, reservation := l.findReservation(reservationID)
	if agent == nil || reservation == nil {
		return ErrReservationNotFound
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()

	agent.balance += reservation.Amount
	delete(agent.reservations, reservationID)
	agent.updatedAt = time.Now()

	l.logger.Debug("Released reservation",
		zap.String("reservation_id", reservationID),
		zap.Int64("refunded", reservation.Amount),
		zap.Int64("new_balance", agent.balance))
	return nil
}

// GetBalance returns the current balance and reserved total for an agent.
func (l *LedgerImpl) GetBalance(agentAddress string) (*LedgerEntry, error) {
	l.mu.RLock()
	agent := l.getAgent(agentAddress)
	l.mu.RUnlock()

	if agent == nil {
		return nil, nil
	}

	agent.mu.Lock()
	defer agent.mu.Unlock()

	return &LedgerEntry{
		Balance:   agent.balance,
		Reserved:  agent.sumReserved(),
		UpdatedAt: agent.updatedAt,
	}, nil
}

// Snapshot returns a copy of all agent balances.
func (l *LedgerImpl) Snapshot() map[string]LedgerEntry {
	l.mu.RLock()
	defer l.mu.RUnlock()

	snap := make(map[string]LedgerEntry, len(l.agents))
	for addr, agent := range l.agents {
		agent.mu.Lock()
		snap[addr] = LedgerEntry{
			Balance:   agent.balance,
			Reserved:  agent.sumReserved(),
			UpdatedAt: agent.updatedAt,
		}
		agent.mu.Unlock()
	}
	return snap
}

// StartCleanup starts a background goroutine that expires stale reservations.
func (l *LedgerImpl) StartCleanup(interval time.Duration) {
	l.stopCh = make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				l.cleanupExpired()
			case <-l.stopCh:
				return
			}
		}
	}()
}

// StopCleanup stops the background cleanup goroutine.
func (l *LedgerImpl) StopCleanup() {
	if l.stopCh != nil {
		close(l.stopCh)
	}
}

// cleanupExpired removes reservations older than TTL and refunds them.
func (l *LedgerImpl) cleanupExpired() {
	l.mu.RLock()
	defer l.mu.RUnlock()

	now := time.Now()
	for addr, agent := range l.agents {
		agent.mu.Lock()
		for id, res := range agent.reservations {
			if now.Sub(res.CreatedAt) > l.ttl {
				agent.balance += res.Amount
				delete(agent.reservations, id)
				l.logger.Warn("Expired stale reservation",
					zap.String("reservation_id", id),
					zap.String("agent", addr),
					zap.Int64("refunded", res.Amount),
					zap.Duration("age", now.Sub(res.CreatedAt)))
			}
		}
		agent.mu.Unlock()
	}
}

// findReservation locates the agent entry and reservation for a given ID.
// Returns (nil, nil) if not found. Does NOT lock the agent.
func (l *LedgerImpl) findReservation(reservationID string) (*agentEntry, *Reservation) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	for _, agent := range l.agents {
		// We need to briefly lock to check reservations map
		agent.mu.Lock()
		if res, ok := agent.reservations[reservationID]; ok {
			agent.mu.Unlock()
			return agent, res
		}
		agent.mu.Unlock()
	}
	return nil, nil
}
