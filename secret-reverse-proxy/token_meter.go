package secret_reverse_proxy

import (
	"sync"
	"time"
)

// TokenUsage stores cumulative token stats for a single API key.
type TokenUsage struct {
	InputTokens   int
	OutputTokens  int
	LastUpdatedAt time.Time
}

// TokenAccumulator tracks usage per hashed API key.
type TokenAccumulator struct {
	mu      sync.Mutex
	usage   map[string]*TokenUsage // key = SHA256(API key)
}

// NewTokenAccumulator initializes the accumulator.
func NewTokenAccumulator() *TokenAccumulator {
	return &TokenAccumulator{
		usage: make(map[string]*TokenUsage),
	}
}

// RecordUsage adds tokens to the given API key’s tally.
func (ta *TokenAccumulator) RecordUsage(apiKeyHash string, inputTokens, outputTokens int) {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	entry, exists := ta.usage[apiKeyHash]
	if !exists {
		entry = &TokenUsage{}
		ta.usage[apiKeyHash] = entry
	}
	entry.InputTokens += inputTokens
	entry.OutputTokens += outputTokens
	entry.LastUpdatedAt = time.Now()
}

// FlushUsage returns all accumulated usage and clears internal state.
func (ta *TokenAccumulator) FlushUsage() map[string]TokenUsage {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	// Deep copy the current state
	flushed := make(map[string]TokenUsage, len(ta.usage))
	for k, v := range ta.usage {
		flushed[k] = *v
	}
	// Reset
	ta.usage = make(map[string]*TokenUsage)
	return flushed
}

// PeekUsage returns a snapshot of current usage without clearing it.
func (ta *TokenAccumulator) PeekUsage() map[string]TokenUsage {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	snapshot := make(map[string]TokenUsage, len(ta.usage))
	for k, v := range ta.usage {
		snapshot[k] = *v
	}
	return snapshot
}
