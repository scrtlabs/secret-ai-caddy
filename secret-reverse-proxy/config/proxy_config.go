package proxyconfig

import (
	"time"
)

// Config holds the configuration parameters for the secret reverse proxy middleware.
// This struct defines all the configurable aspects of the authentication system.
type Config struct {
	// APIKey is the primary master key that grants immediate access without further validation.
	// This key should be kept secure and rotated regularly.
	APIKey string `json:"api_key,omitempty"`
	
	// MasterKeysFile is the path to a file containing additional master keys (one per line).
	// This allows for multiple master keys without hardcoding them in configuration.
	// The file is read on each validation attempt, so keys can be rotated without restart.
	MasterKeysFile string `json:"master_keys_file,omitempty"`
	
	// PermitFile is the path to a JSON file containing the Secret Network permit configuration.
	// If not specified, a default hardcoded permit will be used.
	// The permit defines blockchain authentication parameters for contract queries.
	PermitFile string `json:"permit_file,omitempty"`
	
	// ContractAddress is the Secret Network smart contract address that stores valid API keys.
	// This contract is queried to refresh the cache of valid hashed API keys.
	ContractAddress string `json:"contract_address,omitempty"`
	
	SecretNode string `json:"secret_node,omitempty"`

	SecretChainID string `json:"secret_chain_id,omitempty"`

	// CacheTTL defines how long cached API key validation results remain valid.
	// After this duration, the cache will be refreshed from the smart contract.
	CacheTTL time.Duration `json:"cache_ttl,omitempty"`

	MeteringInterval time.Duration `json:"metering_interval,omitempty"` // e.g., "5m", "1h"
	Metering bool        `json:"metering,omitempty"` // metering if enabled
	MeteringURL string    `json:"metering_url,omitempty"` // metering URL
	
	// Enhanced metering options (always enabled)
	MaxBodySize        int64         `json:"max_body_size,omitempty"`       // Max body size for token counting
	TokenCountingMode  string        `json:"token_counting_mode,omitempty"` // "accurate", "fast", "heuristic"
	MaxRetries         int           `json:"max_retries,omitempty"`         // Max retries for failed reports
	RetryBackoff       time.Duration `json:"retry_backoff,omitempty"`       // Base retry backoff duration
	EnableMetrics      bool          `json:"enable_metrics,omitempty"`      // Enable metrics collection
	MetricsPath        string        `json:"metrics_path,omitempty"`        // Metrics endpoint path
}

// Config interface implementation for enhanced metering
func (c *Config) GetMeteringURL() string           { return c.MeteringURL }
func (c *Config) GetMeteringInterval() time.Duration { return c.MeteringInterval }
func (c *Config) GetMaxBodySize() int64            { return c.MaxBodySize }
func (c *Config) GetTokenCountingMode() string     { return c.TokenCountingMode }
func (c *Config) GetMaxRetries() int               { return c.MaxRetries }
func (c *Config) GetRetryBackoff() time.Duration   { return c.RetryBackoff }
func (c *Config) IsMetricsEnabled() bool           { return c.EnableMetrics }
func (c *Config) GetMetricsPath() string           { return c.MetricsPath }

// defaultConfig returns a Config struct populated with sensible default values.
// These defaults are used when no explicit configuration is provided.
//
// Default values:
// - MasterKeysFile: "master_keys.txt" (local file in working directory)
// - ContractAddress: Secret Network contract for API key validation
// - CacheTTL: 30 minutes (balances performance vs. key rotation speed)
//
// Returns:
//   - *Config: A new Config instance with default values
func DefaultConfig() *Config {
	return &Config{
		// Default master keys file location - should contain one API key per line
		MasterKeysFile: "",
		
		// Default Secret Network contract address for API key validation
		ContractAddress: "",

		SecretNode: "",

		SecretChainID: "",
		
		// Default cache TTL - 30 minutes provides good balance between performance and security
		CacheTTL: 30 * time.Minute,

		// Default metering contract - hardcoded for now
		Metering: false,
		MeteringInterval: 10 * time.Minute,
		
		// Enhanced metering defaults (always enabled)
		MaxBodySize:        10 * 1024 * 1024,    // 10MB default
		TokenCountingMode:  "accurate",          // Use enhanced accurate counting by default
		MaxRetries:         3,                   // 3 retry attempts
		RetryBackoff:       5 * time.Minute,     // 5 minute base backoff
		EnableMetrics:      false,               // Disabled by default
		MetricsPath:        "/metrics",          // Standard metrics path
	}
}