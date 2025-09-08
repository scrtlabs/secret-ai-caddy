package secret_reverse_proxy

import (
	"sync"
	"time"
)

// ModelUsage stores token counts for a specific model
type ModelUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// TokenUsage stores cumulative token stats for a single API key.
type TokenUsage struct {
	InputTokens   int
	OutputTokens  int
	LastUpdatedAt time.Time
	// ModelUsage tracks token usage per model
	ModelUsage    map[string]ModelUsage `json:"model_usage,omitempty"`
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

// RecordUsage adds tokens to the given API key's tally.
func (ta *TokenAccumulator) RecordUsage(apiKeyHash string, inputTokens, outputTokens int) {
	ta.RecordUsageWithModel(apiKeyHash, "unknown", inputTokens, outputTokens)
}

// RecordUsageWithModel adds tokens to the given API key's tally, tracking per model.
func (ta *TokenAccumulator) RecordUsageWithModel(apiKeyHash, modelName string, inputTokens, outputTokens int) {
	ta.mu.Lock()
	defer ta.mu.Unlock()

	entry, exists := ta.usage[apiKeyHash]
	if !exists {
		entry = &TokenUsage{
			ModelUsage: make(map[string]ModelUsage),
		}
		ta.usage[apiKeyHash] = entry
	}
	
	// Update overall totals
	entry.InputTokens += inputTokens
	entry.OutputTokens += outputTokens
	entry.LastUpdatedAt = time.Now()
	
	// Update per-model tracking
	if entry.ModelUsage == nil {
		entry.ModelUsage = make(map[string]ModelUsage)
	}
	
	modelUsage := entry.ModelUsage[modelName]
	modelUsage.InputTokens += inputTokens
	modelUsage.OutputTokens += outputTokens
	entry.ModelUsage[modelName] = modelUsage
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