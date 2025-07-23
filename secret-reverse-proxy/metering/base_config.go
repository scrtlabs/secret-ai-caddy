package metering

import (
	"time"
	
	secret_reverse_proxy "github.com/scrtlabs/secret-reverse-proxy"
)

// Config is a type alias to the main package's Config for convenience
type Config = secret_reverse_proxy.Config

// Middleware is a type alias to the main package's Middleware for convenience  
type Middleware = secret_reverse_proxy.Middleware

// TokenAccumulator is a type alias to the main package's TokenAccumulator for convenience
type TokenAccumulator = secret_reverse_proxy.TokenAccumulator

// TokenUsage is a type alias to the main package's TokenUsage for convenience
type TokenUsage = secret_reverse_proxy.TokenUsage

// NewTokenAccumulator creates a new token accumulator instance
func NewTokenAccumulator() *TokenAccumulator {
	return secret_reverse_proxy.NewTokenAccumulator()
}

// DefaultMeteringConfig returns sensible defaults for metering configuration
func DefaultMeteringConfig() *EnhancedConfig {
	baseConfig := &Config{
		MeteringInterval: 10 * time.Minute,
		MeteringContract: "secret1x6w0mzpxlwwl9j8v3r6x6r65s7wqcz3ej74n2z",
		CacheTTL:         30 * time.Minute,
	}
	
	return &EnhancedConfig{
		Config:             baseConfig,
		MaxBodySize:        1024 * 1024, // 1MB default
		TokenCountingMode:  "fast",
		MaxRetries:         3,
		RetryBackoff:       5 * time.Minute,
		EnableMetrics:      true,
		MetricsPath:        "/metrics",
		LogSampleRate:      0.1, // 10% sampling
		FailedReportsDir:   "/tmp/caddy-failed-reports",
	}
}