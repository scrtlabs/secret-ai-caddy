package x402

import (
	"encoding/json"
	"os"
	"sync"

	"go.uber.org/zap"
)

// QuoteEngineImpl estimates request cost from model, input tokens, and output budget.
type QuoteEngineImpl struct {
	mu       sync.RWMutex
	pricing  *PricingConfig
	currency string
	logger   *zap.Logger
}

// NewQuoteEngine creates a new QuoteEngine with the given pricing config.
func NewQuoteEngine(pricing *PricingConfig, currency string, logger *zap.Logger) *QuoteEngineImpl {
	return &QuoteEngineImpl{
		pricing:  pricing,
		currency: currency,
		logger:   logger,
	}
}

// LoadPricing loads pricing configuration from a JSON file.
// If path is empty, returns a default pricing config.
func LoadPricing(path string) (*PricingConfig, error) {
	if path == "" {
		return &PricingConfig{
			Default: ModelPricing{
				InputCostPer1kTokens:  10,
				OutputCostPer1kTokens: 30,
			},
			Models: make(map[string]ModelPricing),
		}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg PricingConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	if cfg.Models == nil {
		cfg.Models = make(map[string]ModelPricing)
	}
	return &cfg, nil
}

// Estimate returns a cost estimate for the given request parameters.
// Uses integer math with ceiling division to avoid rounding issues.
func (q *QuoteEngineImpl) Estimate(model string, inputTokens int, maxOutputTokens int) (*Quote, error) {
	q.mu.RLock()
	pricing := q.getPricing(model)
	q.mu.RUnlock()

	// Ceiling division: (tokens * cost + 999) / 1000
	inputCost := ceilDiv(int64(inputTokens)*pricing.InputCostPer1kTokens, 1000)
	outputCost := ceilDiv(int64(maxOutputTokens)*pricing.OutputCostPer1kTokens, 1000)

	return &Quote{
		EstimatedInputTokens:  inputTokens,
		EstimatedOutputTokens: maxOutputTokens,
		InputCost:             inputCost,
		OutputCost:            outputCost,
		TotalCost:             inputCost + outputCost,
		Model:                 model,
		Currency:              q.currency,
	}, nil
}

// ReloadPricing hot-reloads pricing from the given file path.
func (q *QuoteEngineImpl) ReloadPricing(path string) error {
	pricing, err := LoadPricing(path)
	if err != nil {
		return err
	}
	q.mu.Lock()
	q.pricing = pricing
	q.mu.Unlock()
	q.logger.Info("Pricing reloaded", zap.String("path", path))
	return nil
}

// getPricing returns the pricing for a model, falling back to default.
func (q *QuoteEngineImpl) getPricing(model string) ModelPricing {
	if p, ok := q.pricing.Models[model]; ok {
		return p
	}
	return q.pricing.Default
}

// ceilDiv performs ceiling integer division.
func ceilDiv(a, b int64) int64 {
	if b == 0 {
		return 0
	}
	return (a + b - 1) / b
}
