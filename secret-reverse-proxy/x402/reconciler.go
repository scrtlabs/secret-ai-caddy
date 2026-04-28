package x402

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// ReconcilerImpl keeps the SpendableLedger in sync with the billing backend.
type ReconcilerImpl struct {
	ledger    *LedgerImpl
	client    *SecretVMClientImpl
	logger    *zap.Logger
	stopCh    chan struct{}
	mu        sync.Mutex
	running   bool
}

// NewReconciler creates a new Reconciler.
func NewReconciler(ledger *LedgerImpl, client *SecretVMClientImpl, logger *zap.Logger) *ReconcilerImpl {
	return &ReconcilerImpl{
		ledger: ledger,
		client: client,
		logger: logger,
	}
}

// Start begins periodic balance reconciliation.
func (rc *ReconcilerImpl) Start(interval time.Duration) {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if rc.running {
		return
	}

	rc.stopCh = make(chan struct{})
	rc.running = true

	go func() {
		// Initial sync on startup
		rc.syncAll()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				rc.syncAll()
			case <-rc.stopCh:
				return
			}
		}
	}()

	rc.logger.Info("Reconciler started", zap.Duration("interval", interval))
}

// Stop halts the reconciler.
func (rc *ReconcilerImpl) Stop() {
	rc.mu.Lock()
	defer rc.mu.Unlock()

	if !rc.running {
		return
	}

	close(rc.stopCh)
	rc.running = false
	rc.logger.Info("Reconciler stopped")
}

// ForceSync triggers an immediate reconciliation for a specific agent.
// Handles lazy hydration: if the agent has no ledger entry, fetches from
// the billing backend and credits the ledger.
func (rc *ReconcilerImpl) ForceSync(agentAddress string) error {
	return rc.syncAgent(agentAddress)
}

// syncAll reconciles all known agents in the ledger, plus any agents
// discovered via ListAgents on the billing backend.
func (rc *ReconcilerImpl) syncAll() {
	// Get agents from ledger
	snapshot := rc.ledger.Snapshot()

	// Also try to discover agents from the billing backend
	remoteAgents, err := rc.client.ListAgents()
	if err != nil {
		rc.logger.Warn("Failed to list agents from billing backend", zap.Error(err))
		// Continue with local agents only
	}

	// Merge remote agents into sync set
	agentsToSync := make(map[string]bool)
	for addr := range snapshot {
		agentsToSync[addr] = true
	}
	for _, addr := range remoteAgents {
		agentsToSync[addr] = true
	}

	var successCount, failCount int
	for addr := range agentsToSync {
		if err := rc.syncAgent(addr); err != nil {
			failCount++
			rc.logger.Warn("Failed to sync agent",
				zap.String("agent", addr),
				zap.Error(err))
		} else {
			successCount++
		}
	}

	rc.logger.Debug("Reconciliation complete",
		zap.Int("synced", successCount),
		zap.Int("failed", failCount),
		zap.Int("total", len(agentsToSync)))
}

// syncAgent reconciles a single agent's balance.
func (rc *ReconcilerImpl) syncAgent(agentAddress string) error {
	remoteBalance, err := rc.client.GetBalance(agentAddress)
	if err != nil {
		return err
	}

	entry, _ := rc.ledger.GetBalance(agentAddress)

	var localTotal int64
	if entry != nil {
		localTotal = entry.Balance + entry.Reserved
	}

	delta := remoteBalance - localTotal
	if delta > 0 {
		if err := rc.ledger.Credit(agentAddress, delta); err != nil {
			return err
		}
		rc.logger.Debug("Credited agent from reconciliation",
			zap.String("agent", agentAddress),
			zap.Int64("delta", delta),
			zap.Int64("remote_balance", remoteBalance),
			zap.Int64("local_total", localTotal))
	} else if delta < 0 {
		rc.logger.Warn("Agent local balance exceeds remote — possible overspend",
			zap.String("agent", agentAddress),
			zap.Int64("delta", delta),
			zap.Int64("remote_balance", remoteBalance),
			zap.Int64("local_total", localTotal))
	}

	return nil
}
