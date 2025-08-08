// Package secret_reverse_proxy implements a Caddy middleware that provides API key authentication
// for reverse proxy operations. It validates API keys against multiple sources:
// 1. Configured master key
// 2. Master keys file (local file with one key per line)
// 3. Secret Network smart contract (cached for performance)
//
// The middleware integrates with Caddy's HTTP handler chain and blocks requests with invalid
// or missing API keys while forwarding valid requests to downstream handlers.
//
// Authentication Flow:
// Request → Extract API Key → Check Master Key → Check Master Keys File → Check Cache → Query Contract → Allow/Deny
//
// Configuration:
// The middleware can be configured via Caddyfile with directives for master keys, file paths,
// contract addresses, and cache settings.
package secret_reverse_proxy

import (
	"bufio"
	querycontract "github.com/scrtlabs/secret-reverse-proxy/query-contract"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
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
	
	// CacheTTL defines how long cached API key validation results remain valid.
	// After this duration, the cache will be refreshed from the smart contract.
	CacheTTL time.Duration `json:"cache_ttl,omitempty"`

	MeteringInterval time.Duration `json:"metering_interval,omitempty"` // e.g., "5m", "1h"
	MeteringContract string        `json:"metering_contract,omitempty"` // smart contract for usage reports
}

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
func defaultConfig() *Config {
	return &Config{
		// Default master keys file location - should contain one API key per line
		MasterKeysFile: "",
		
		// Default Secret Network contract address for API key validation
		ContractAddress: "",
		
		// Default cache TTL - 30 minutes provides good balance between performance and security
		CacheTTL: 30 * time.Minute,

		// Default metering contract - hardcoded for now
		MeteringContract: "",
		MeteringInterval: 10 * time.Minute,
	}
}

// APIKeyValidator encapsulates all logic for validating API keys against multiple sources.
// It manages an in-memory cache of valid API key hashes for performance optimization.
// Thread-safe operations are ensured through proper mutex usage.
type APIKeyValidator struct {
	// config holds the validator configuration parameters
	config *Config
	
	// cache stores SHA256 hashes of valid API keys mapped to their validity status
	// Key: SHA256 hash of API key, Value: true if valid
	cache map[string]bool
	
	// cacheMutex protects concurrent access to the cache map
	// Uses RWMutex to allow multiple concurrent reads while serializing writes
	cacheMutex sync.RWMutex
	
	// lastUpdate tracks when the cache was last refreshed from the smart contract
	// Used to determine if cache has exceeded TTL and needs refresh
	lastUpdate time.Time
}

// NewAPIKeyValidator creates and initializes a new APIKeyValidator instance.
// This constructor sets up the validator with the provided configuration and
// initializes the internal cache for storing API key validation results.
//
// Parameters:
//   - config: Configuration parameters for the validator
//
// Returns:
//   - *APIKeyValidator: A new validator instance ready for use
//
// Note: The cache starts empty and will be populated on first validation attempt
// or when updateAPIKeyCache() is called.
func NewAPIKeyValidator(config *Config) *APIKeyValidator {
	return &APIKeyValidator{
		// Store reference to configuration
		config: config,
		
		// Initialize empty cache - will be populated from contract on first use
		cache: make(map[string]bool),
		
		// lastUpdate will be set to zero time initially, forcing immediate cache update
	}
}

// init registers the middleware with Caddy's module system.
// This function is called automatically when the package is imported.
// It performs two critical registrations:
// 1. Registers the Middleware as a Caddy module
// 2. Registers the Caddyfile directive parser
func init() {
	// Register this middleware as a Caddy module so it can be loaded
	caddy.RegisterModule(&Middleware{})
	
	// Register the Caddyfile directive "secret_reverse_proxy" and its parser
	// This allows users to configure the middleware in their Caddyfile with block syntax
	httpcaddyfile.RegisterHandlerDirective("secret_reverse_proxy", parseCaddyfile)
}

// Middleware represents the Caddy HTTP middleware that performs API key authentication.
// It implements the required Caddy interfaces to integrate into the HTTP handler chain.
// Each request passes through this middleware before reaching downstream handlers.
type Middleware struct {
	// Config holds the middleware configuration loaded from Caddyfile
	// Made public with JSON tags so Caddy can marshal/unmarshal it properly
	Config *Config `json:"config,omitempty"`
	
	// validator performs the actual API key validation logic
	// Initialized during the Provision phase
	validator *APIKeyValidator

	accumulator *TokenAccumulator
}

// CaddyModule returns the module information required by Caddy's module system.
// This method implements the caddy.Module interface and provides metadata
// about this middleware to Caddy's module loader.
//
// Returns:
//   - caddy.ModuleInfo: Module metadata including ID and constructor function
func (Middleware) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		// Unique module ID within Caddy's HTTP handler namespace
		// Format: namespace.category.module_name
		ID: "http.handlers.secret_reverse_proxy",
		
		// Constructor function that returns a new instance of this middleware
		// Called by Caddy when creating middleware instances
		New: func() caddy.Module { return new(Middleware) },
	}
}

// Provision implements caddy.Provisioner and is called by Caddy to set up the middleware.
// This method is invoked during Caddy's configuration loading phase, before any requests
// are processed. It initializes the middleware with configuration and creates the validator.
//
// Parameters:
//   - ctx: Caddy context containing global configuration and services
//
// Returns:
//   - error: nil on success, error on configuration failure
//
// Note: This method must complete successfully for the middleware to be active.
func (m *Middleware) Provision(ctx caddy.Context) error {
	logger := caddy.Log()
	logger.Debug("Provisioning secret reverse proxy middleware")
	
	// BLOCK 1: Configuration Setup
	// Ensure we have a valid configuration, using defaults if none provided
	if m.Config == nil {
		logger.Error("🔴 Nil configuration")
		// raise an error if no configuration is provided
		return fmt.Errorf("nil configuration")
	}
	
	// BLOCK 2: Validator Initialization
	// Create the API key validator that will handle authentication logic
	m.validator = NewAPIKeyValidator(m.Config)
	if m.validator == nil {
		return fmt.Errorf("failed to create API key validator")
	}

	// BLOCK 3: Token Metering
	// Create the token accumulator
	if m.accumulator == nil {
		m.accumulator = NewTokenAccumulator()
	}
	m.StartTokenReportingLoop(m.Config.MeteringInterval)

	// BLOCK 4: Configuration Logging
	// Log configuration details for debugging (excluding sensitive data)
	logger.Info("Secret reverse proxy middleware provisioned",
		zap.String("contract_address", m.Config.ContractAddress),
		zap.Duration("cache_ttl", m.Config.CacheTTL),
		zap.String("master_keys_file", m.Config.MasterKeysFile),
		zap.String("master_key_configured", m.Config.APIKey),
		zap.String("permit_file_configured", m.Config.PermitFile))

	return nil
}

// Validate implements caddy.Validator and is called by Caddy to verify configuration.
// This method checks that all required configuration parameters are present and valid.
// It's called after Provision() but before the middleware starts handling requests.
//
// Returns:
//   - error: nil if configuration is valid, error describing the problem if invalid
//
// Validation checks:
// - Configuration object exists
// - Contract address is specified
// - Cache TTL is positive
// - File paths are accessible (if specified)
func (m *Middleware) Validate() error {
	logger := caddy.Log()
	logger.Debug("Validating secret reverse proxy middleware configuration")
	
	// BLOCK 1: Basic Configuration Validation
	// Ensure we have a configuration object to work with
	if m.Config == nil {
		err := fmt.Errorf("configuration is nil")
		logger.Error("Validation failed", zap.Error(err))
		return err
	}
	
	// Log the complete configuration contents
	logger.Info("Configuration contents",
		zap.String("api_key", m.Config.APIKey),
		zap.String("master_keys_file", m.Config.MasterKeysFile),
		zap.String("permit_file", m.Config.PermitFile),
		zap.String("contract_address", m.Config.ContractAddress),
		zap.Duration("cache_ttl", m.Config.CacheTTL),
		zap.Duration("metering_interval", m.Config.MeteringInterval),
		zap.String("metering_contract", m.Config.MeteringContract))

	
	// BLOCK 2: Required Field Validation
	// Check that essential configuration parameters are provided
	if m.Config.ContractAddress == "" {
		err := fmt.Errorf("contract address is required")
		logger.Error("Validation failed", zap.Error(err))
		return err
	}
	
	if m.Config.CacheTTL <= 0 {
		err := fmt.Errorf("cache TTL must be positive, got %v", m.Config.CacheTTL)
		logger.Error("Validation failed", zap.Error(err))
		return err
	}
	
	// BLOCK 3: File Access Validation
	// Verify that specified files are accessible (warn but don't fail)
	if m.Config.MasterKeysFile != "" {
		if _, err := os.Stat(m.Config.MasterKeysFile); err != nil && !os.IsNotExist(err) {
			logger.Warn("Master keys file access issue",
				zap.String("file", m.Config.MasterKeysFile),
				zap.Error(err))
		}
	}
	
	if m.Config.PermitFile != "" {
		if _, err := os.Stat(m.Config.PermitFile); err != nil {
			logger.Warn("Permit file access issue",
				zap.String("file", m.Config.PermitFile),
				zap.Error(err))
		}
	}
	
	logger.Debug("Configuration validation successful")
	return nil
}

// ServeHTTP implements caddyhttp.MiddlewareHandler and is the main request processing method.
// This method is called for every HTTP request that passes through this middleware.
// It performs API key authentication and either blocks or forwards the request.
//
// Request Processing Flow:
// 1. Extract Authorization header
// 2. Parse API key from header
// 3. Validate API key through multiple sources
// 4. Allow/deny request based on validation result
//
// Parameters:
//   - w: HTTP response writer for sending responses
//   - r: HTTP request containing headers and request data
//   - next: Next handler in the Caddy chain to call if authentication succeeds
//
// Returns:
//   - error: nil on success, error if request processing fails
func (m Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	logger := caddy.Log()
	
	// BLOCK 1: Request Logging
	// Log incoming request details for debugging and audit purposes
	logger.Debug("Processing request through secret reverse proxy middleware",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr))
	
	// BLOCK 2: Authorization Header Extraction
	// Check if the request contains an Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		// No authorization header = immediate rejection
		logger.Debug("Request rejected: missing Authorization header",
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("path", r.URL.Path))
		http.Error(w, "Missing Authorization header", http.StatusUnauthorized)
		return nil
	}

	// BLOCK 3: API Key Parsing
	// Extract the actual API key from the Authorization header
	// Handles both "Basic" and "Bearer" token formats
	apiKey := extractAPIKey(authHeader)
	if apiKey == "" {
		// Invalid header format = rejection
		logger.Debug("Request rejected: invalid Authorization header format",
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("auth_header_prefix", authHeader[:min(20, len(authHeader))]))
		http.Error(w, "Invalid Authorization header format", http.StatusUnauthorized)
		return nil
	}

	// BLOCK 4: API Key Validation
	// Perform the core authentication logic using the validator
	logger.Debug("Validating API key",
		zap.String("key_prefix", apiKey[:min(8, len(apiKey))]+"..."))

	isValid, err := m.validator.ValidateAPIKey(apiKey)
	if err != nil {
		// Validation process failed (not invalid key, but system error)
		logger.Error("API key validation failed",
			zap.Error(err),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("path", r.URL.Path))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return nil
	}

	// BLOCK 5: Validation Result Processing
	// Check the validation result and either block or allow the request
	if !isValid {
		// Valid validation process but invalid key = rejection
		logger.Info("Request rejected: invalid API key",
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("path", r.URL.Path),
			zap.String("key_prefix", apiKey[:min(8, len(apiKey))]+"..."))
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return nil
	}

	// BLOCK 6: Request Forwarding
	// Authentication successful - forward request to next handler in chain
	// logger.Debug("API key validation successful, forwarding request")
	// return next.ServeHTTP(w, r)

	wrappedWriter := NewTokenMeteringResponseWriter(w)
	err = next.ServeHTTP(wrappedWriter, r)
	if err != nil {
		return err
	}

	// Re-read request body (see below)
	requestBody := readRequestBody(r)
	inputTokens := CountTokensHeuristic(requestBody)

	// Count response tokens
	responseBody := wrappedWriter.Body()
	outputTokens := CountTokensHeuristic(string(responseBody))

	// Hash and record usage
	apiKeyHash := sha256.Sum256([]byte(apiKey))
	hashHex := hex.EncodeToString(apiKeyHash[:])
	m.accumulator.RecordUsage(hashHex, inputTokens, outputTokens)

	return nil
}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler and parses Caddyfile configuration.
// This method is called by Caddy when parsing the Caddyfile to extract configuration
// directives specific to this middleware.
//
// Supported Caddyfile directives:
//   - API_MASTER_KEY <key>         : Sets the primary master API key
//   - master_keys_file <path>      : Path to file containing additional master keys
//   - permit_file <path>           : Path to JSON file with Secret Network permit
//   - contract_address <address>   : Secret Network contract address for API key validation
//
// Example Caddyfile configuration:
//   secret_reverse_proxy {
//       API_MASTER_KEY "your-master-key-here"
//       master_keys_file "/etc/caddy/master_keys.txt"
//       contract_address "secret1abc..."
//   }
//
// Parameters:
//   - d: Caddyfile dispenser for parsing configuration tokens
//
// Returns:
//   - error: nil on successful parsing, error if configuration is invalid
func (m *Middleware) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	logger := caddy.Log()
	// BLOCK 1: Configuration Initialization
	// Ensure we have a config object to populate
	if m.Config == nil {
		m.Config = defaultConfig()
		logger.Debug("Using default configuration")
	}
	
	logger.Info("UnmarshalCaddyfile called - parsing secret_reverse_proxy configuration")
	// BLOCK 2: Directive Parsing Loop
	// Parse each configuration directive from the Caddyfile
	logger.Info("📋 Starting directive parsing loop")
	for d.Next() {
		// Process configuration block contents
		for d.NextBlock(0) {
			// Parse individual configuration directives
			switch d.Val() {
				case "metering_interval":
					if !d.NextArg() {
						return d.ArgErr()
					}
					intervalStr := d.Val()
					interval, err := time.ParseDuration(intervalStr)
					if err != nil {
						return d.Errf("invalid metering_interval: %v", err)
					}
					m.Config.MeteringInterval = interval

				case "metering_contract":
					if !d.Args(&m.Config.MeteringContract) {
						return d.ArgErr()
					}
				case "API_MASTER_KEY":
					// Primary master key configuration
					logger.Info("Processing API_MASTER_KEY directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 API_MASTER_KEY directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw API_MASTER_KEY value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.APIKey = expandedValue
					
					logger.Info("🔑 Primary master key configured successfully:", zap.String("API_MASTER_KEY", m.Config.APIKey))
				case "master_keys_file":
					// Additional master keys file path
					if !d.Args(&m.Config.MasterKeysFile) {
						return d.ArgErr()
					}
					logger.Info("🔑 Master keys file path", zap.String("master keys file path", m.Config.MasterKeysFile))
				case "permit_file":
					// Secret Network permit file path
					if !d.Args(&m.Config.PermitFile) {
						return d.ArgErr()
					}
					logger.Info("🔑 Permit file", zap.String("permit file path", m.Config.PermitFile))
				case "contract_address":
					// Smart contract address for API key validation
					if !d.Args(&m.Config.ContractAddress) {
						return d.ArgErr()
					}
					logger.Info("🔑 Contract address", zap.String("contract address", m.Config.ContractAddress))
				default:
					logger.Error("🔴 Unmarshal Caddyfile", zap.String("unknown subdirective", d.Val()))
					// Unknown directive - return error
					return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
		logger.Info("🔄 Finished processing block for token:", zap.String("token", d.Val()))
	}
	logger.Info("✅ Final configuration:", 
		zap.String("api_key", m.Config.APIKey),
		zap.String("master_keys_file", m.Config.MasterKeysFile),
		zap.String("permit_file", m.Config.PermitFile),
		zap.String("contract_address", m.Config.ContractAddress),
		zap.Duration("metering_interval", m.Config.MeteringInterval),
		zap.String("metering_contract", m.Config.MeteringContract),
	)
	return nil
}

// expandEnvVars expands environment variables in Caddyfile format {env.VAR_NAME}
// This function handles the {env.VAR_NAME} syntax used in Caddyfiles.
//
// Parameters:
//   - value: String that may contain environment variable references
//
// Returns:
//   - string: String with environment variables expanded
func expandEnvVars(value string) string {
	// Pattern to match {env.VAR_NAME}
	envPattern := regexp.MustCompile(`\{env\.([A-Za-z_][A-Za-z0-9_]*)\}`)
	
	return envPattern.ReplaceAllStringFunc(value, func(match string) string {
		// Extract the variable name from {env.VAR_NAME}
		varName := envPattern.FindStringSubmatch(match)[1]
		
		// Get the environment variable value
		envValue := os.Getenv(varName)
		
		// Log for debugging
		logger := caddy.Log()
		logger.Debug("Expanding environment variable",
			zap.String("var_name", varName),
			zap.String("env_value", envValue),
			zap.String("original", match))
		
		return envValue
	})
}

// min returns the minimum of two integers.
// This utility function is used for safe string slicing to prevent
// index out of bounds errors when logging partial API keys.
//
// Parameters:
//   - a, b: Two integers to compare
//
// Returns:
//   - int: The smaller of the two input values
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// extractAPIKey extracts the actual API key from an Authorization header.
// This function handles the standard HTTP Authorization header formats:
// - "Basic <key>" - Used for basic authentication
// - "Bearer <key>" - Used for token-based authentication
// - Raw key without prefix - Fallback for custom implementations
//
// Parameters:
//   - authHeader: The complete Authorization header value
//
// Returns:
//   - string: The extracted API key, or original header if no known prefix found
//
// Example:
//   extractAPIKey("Bearer abc123") returns "abc123"
//   extractAPIKey("Basic xyz789") returns "xyz789"
//   extractAPIKey("plainkey") returns "plainkey"
func extractAPIKey(authHeader string) string {
	// Try to remove "Basic " prefix (HTTP Basic Auth format)
	if apiKey, found := strings.CutPrefix(authHeader, "Basic "); found {
		return apiKey
	}
	
	// Try to remove "Bearer " prefix (OAuth/JWT token format)
	if apiKey, found := strings.CutPrefix(authHeader, "Bearer "); found {
		return apiKey
	}
	
	// No recognized prefix - return the header as-is (custom format)
	return authHeader
}

// ValidateAPIKey performs comprehensive API key validation through multiple authentication sources.
// This is the core authentication method that implements a tiered validation strategy:
//
// Validation Hierarchy (in order of precedence):
// 1. Configured master key (immediate access)
// 2. Master keys file (local file-based keys)
// 3. Cached contract results (performance optimization)
// 4. Fresh contract query (authoritative source)
//
// Parameters:
//   - apiKey: The API key string to validate
//
// Returns:
//   - bool: true if the API key is valid, false otherwise
//   - error: nil on successful validation process, error if validation cannot be performed
//
// Note: A return of (false, nil) means the key is definitively invalid.
//       A return of (false, error) means the validation process failed.
func (v *APIKeyValidator) ValidateAPIKey(apiKey string) (bool, error) {
	logger := caddy.Log()
	
	// BLOCK 1: Input Validation
	// Ensure we have a non-empty API key to work with
	if strings.TrimSpace(apiKey) == "" {
		logger.Debug("API key validation failed: empty key")
		return false, fmt.Errorf("empty API key")
	}
	
	// Create safe logging prefix for debugging (only first 8 characters)
	keyPrefix := apiKey[:min(8, len(apiKey))] + "..."
	logger.Debug("Starting API key validation", zap.String("key_prefix", keyPrefix))
	
	// BLOCK 2: Master Key Check
	// Check against the primary configured master key (highest priority)
	if v.config.APIKey != "" && apiKey == v.config.APIKey {
		logger.Debug("API key validated: matches configured master key")
		return true, nil
	}

	// BLOCK 3: Master Keys File Check
	// Check against additional master keys stored in a local file
	isMasterKey, err := v.checkMasterKeys(apiKey)
	if err != nil {
		logger.Error("Master key check failed",
			zap.Error(err),
			zap.String("key_prefix", keyPrefix))
		return false, fmt.Errorf("master key check failed: %w", err)
	}
	if isMasterKey {
		logger.Debug("API key validated: found in master keys file")
		return true, nil
	}

	// BLOCK 4: Cache Preparation
	// Hash the API key for secure cache storage and lookup
	// We store hashes instead of plain keys for security
	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	apiKeyHash := hex.EncodeToString(hasher.Sum(nil))

	// BLOCK 5: Cache Lookup
	// Check if we have a recent validation result cached
	v.cacheMutex.RLock()
	cached, found := v.cache[apiKeyHash]
	isStale := time.Since(v.lastUpdate) > v.config.CacheTTL
	cacheSize := len(v.cache)
	v.cacheMutex.RUnlock()

	logger.Debug("Cache lookup",
		zap.String("hash_prefix", apiKeyHash[:min(16, len(apiKeyHash))]+"..."),
		zap.Bool("found", found),
		zap.Bool("stale", isStale),
		zap.Int("cache_size", cacheSize),
		zap.Duration("age", time.Since(v.lastUpdate)))

	// BLOCK 6: Cache Hit Processing
	// If we have a fresh cache entry, use it to avoid contract query
	if found && !isStale {
		logger.Debug("API key validation result from cache", zap.Bool("valid", cached))
		return cached, nil
	}

	// BLOCK 7: Contract Query
	// Cache miss or stale data - query the authoritative smart contract
	logger.Debug("Updating API key cache from contract")
	if err := v.updateAPIKeyCache(); err != nil {
		logger.Error("Cache update failed",
			zap.Error(err),
			zap.String("contract_address", v.config.ContractAddress))
		return false, fmt.Errorf("cache update failed: %w", err)
	}

	// BLOCK 8: Final Result
	// Check the updated cache for the validation result
	v.cacheMutex.RLock()
	defer v.cacheMutex.RUnlock()
	result := v.cache[apiKeyHash]
	logger.Debug("API key validation result after cache update",
		zap.Bool("valid", result),
		zap.Int("new_cache_size", len(v.cache)))
	return result, nil
}

// checkMasterKeys checks if the provided API key exists in the master keys file
func (v *APIKeyValidator) checkMasterKeys(apiKey string) (bool, error) {
	logger := caddy.Log()
	
	if v.config.MasterKeysFile == "" {
		logger.Debug("No master keys file configured")
		return false, nil
	}
	
	logger.Debug("Checking master keys file", zap.String("file", v.config.MasterKeysFile))
	
	file, err := os.Open(v.config.MasterKeysFile)
	if err != nil {
		// If file doesn't exist, that's not an error
		if os.IsNotExist(err) {
			logger.Debug("Master keys file does not exist", zap.String("file", v.config.MasterKeysFile))
			return false, nil
		}
		logger.Error("Failed to open master keys file",
			zap.String("file", v.config.MasterKeysFile),
			zap.Error(err))
		return false, fmt.Errorf("failed to open master keys file: %w", err)
	}
	defer file.Close()

	lineCount := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lineCount++
		nextMasterKey := strings.TrimSpace(scanner.Text())
		if nextMasterKey != "" && nextMasterKey == apiKey {
			logger.Debug("API key found in master keys file",
				zap.String("file", v.config.MasterKeysFile),
				zap.Int("line", lineCount))
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		logger.Error("Error reading master keys file",
			zap.String("file", v.config.MasterKeysFile),
			zap.Error(err))
		return false, fmt.Errorf("error reading master keys file: %w", err)
	}

	logger.Debug("API key not found in master keys file",
		zap.String("file", v.config.MasterKeysFile),
		zap.Int("lines_checked", lineCount))
	return false, nil
}

// updateAPIKeyCache queries the contract and updates the in-memory cache
func (v *APIKeyValidator) updateAPIKeyCache() error {
	logger := caddy.Log()
	start := time.Now()
	
	logger.Debug("Starting cache update", zap.String("contract_address", v.config.ContractAddress))
	
	// Load permit from file or use default
	var permit map[string]any
	var err error
	
	if v.config.PermitFile != "" {
		logger.Debug("Loading permit from file", zap.String("file", v.config.PermitFile))
		permit, err = readPermitFromFile(v.config.PermitFile)
		if err != nil {
			logger.Error("Failed to read permit file",
				zap.String("file", v.config.PermitFile),
				zap.Error(err))
			return fmt.Errorf("failed to read permit file: %w", err)
		}
	} else {
		logger.Debug("Using default permit configuration")
		// Use default permit if no file specified
		permit = getDefaultPermit()
	}

	query := map[string]any{
		"api_keys_with_permit": map[string]any{
			"permit": permit,
		},
	}

	logger.Debug("Querying contract for API keys")
	result, err := querycontract.QueryContract(v.config.ContractAddress, query)
	if err != nil {
		logger.Error("Contract query failed",
			zap.String("contract_address", v.config.ContractAddress),
			zap.Error(err),
			zap.Duration("duration", time.Since(start)))
		return fmt.Errorf("contract query failed: %w", err)
	}

	apiKeys, ok := result["api_keys"].([]any)
	if !ok {
		logger.Error("Unexpected response format: api_keys field not found or wrong type",
			zap.Any("response_keys", getResponseKeys(result)))
		return fmt.Errorf("unexpected response format: api_keys not found")
	}

	newCache := make(map[string]bool)
	validKeys := 0
	skippedEntries := 0
	
	for i, entry := range apiKeys {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			skippedEntries++
			logger.Debug("Skipping invalid entry", zap.Int("index", i))
			continue
		}
		hashedKey, ok := entryMap["hashed_key"].(string)
		if ok && hashedKey != "" {
			newCache[hashedKey] = true
			validKeys++
		} else {
			skippedEntries++
			logger.Debug("Skipping entry with invalid hashed_key", zap.Int("index", i))
		}
	}

	v.cacheMutex.Lock()
	oldCacheSize := len(v.cache)
	v.cache = newCache
	v.lastUpdate = time.Now()
	v.cacheMutex.Unlock()

	duration := time.Since(start)
	logger.Info("Cache update completed",
		zap.Int("old_cache_size", oldCacheSize),
		zap.Int("new_cache_size", len(newCache)),
		zap.Int("valid_keys", validKeys),
		zap.Int("skipped_entries", skippedEntries),
		zap.Duration("duration", duration),
		zap.String("contract_address", v.config.ContractAddress))

	return nil
}

// getResponseKeys extracts the keys from a response map for debugging
func getResponseKeys(response map[string]any) []string {
	keys := make([]string, 0, len(response))
	for k := range response {
		keys = append(keys, k)
	}
	return keys
}

// getDefaultPermit returns the default permit configuration
func getDefaultPermit() map[string]any {
	return map[string]any{
		"params": map[string]any{
			"permit_name":    "api_keys_permit",
			"allowed_tokens": []any{"secret1ttm9axv8hqwjv3qxvxseecppsrw4cd68getrvr"},
			"chain_id":       "pulsar-3",
			"permissions":    []any{},
		},
		"signature": map[string]any{
			"pub_key": map[string]any{
				"type":  "tendermint/PubKeySecp256k1",
				"value": "Aur9D8RLqYMf3sBTiXdhH8mMI9bPHisdDa9y9jwW9RyT",
			},
			"signature": "OLvqQxg0KxAb2fWz+O4pSZK3m0EsLKHp+gSQXmM0NxpDZs0BBBWltGLbqzA8jdiXr/JTpG27a4TiwDIEA8nZ8g==",
		},
	}
}

// readPermitFromFile reads the permit from a JSON file
func readPermitFromFile(filePath string) (map[string]any, error) {
	logger := caddy.Log()
	logger.Debug("Reading permit file", zap.String("file", filePath))
	
	file, err := os.Open(filePath)
	if err != nil {
		logger.Error("Failed to open permit file",
			zap.String("file", filePath),
			zap.Error(err))
		return nil, fmt.Errorf("failed to open permit file: %w", err)
	}
	defer file.Close()

	var permit map[string]any
	if err := json.NewDecoder(file).Decode(&permit); err != nil {
		logger.Error("Failed to decode permit file",
			zap.String("file", filePath),
			zap.Error(err))
		return nil, fmt.Errorf("failed to decode permit file: %w", err)
	}

	logger.Debug("Permit file loaded successfully",
		zap.String("file", filePath),
		zap.Int("keys_count", len(permit)))
	return permit, nil
}

// parseCaddyfile is the entry point for Caddyfile parsing of this middleware.
// This function is registered with Caddy's directive system and is called
// when the "secret_reverse_proxy" directive is encountered in a Caddyfile.
//
// Parameters:
//   - h: Helper containing the Caddyfile dispenser and parsing utilities
//
// Returns:
//   - caddyhttp.MiddlewareHandler: Configured middleware instance
//   - error: nil on success, error if parsing fails
//
// Note: This function creates a new Middleware instance and delegates
//       the actual parsing to UnmarshalCaddyfile.
func parseCaddyfile(h httpcaddyfile.Helper) (caddyhttp.MiddlewareHandler, error) {
	logger := caddy.Log()
	logger.Info("🔧 parseCaddyfile called - creating middleware instance")
	
	// Create new middleware instance
	var m Middleware
	
	// Parse configuration using the Caddyfile dispenser
	err := m.UnmarshalCaddyfile(h.Dispenser)
	if err != nil {
		logger.Error("Failed to parse Caddyfile configuration", zap.Error(err))
		return nil, err
	}
	
	logger.Info("✅ parseCaddyfile completed successfully")
	// Return configured middleware
	return m, nil
}

// Interface guards
var (
	_ caddy.Module                = (*Middleware)(nil)
	_ caddy.Provisioner           = (*Middleware)(nil)
	_ caddy.Validator             = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
	_ caddyfile.Unmarshaler       = (*Middleware)(nil)
)
