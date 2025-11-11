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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"go.uber.org/zap"
	
	"github.com/scrtlabs/secret-reverse-proxy/factories"
	"github.com/scrtlabs/secret-reverse-proxy/interfaces"
	proxyconfig "github.com/scrtlabs/secret-reverse-proxy/config"
	apikeyval "github.com/scrtlabs/secret-reverse-proxy/validators"
)

type Config = proxyconfig.Config
type APIKeyValidator = apikeyval.APIKeyValidator

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

	
	// quitChan is used to signal the background token reporting goroutine to stop
	quitChan chan struct{}
	
	// meteringRunning tracks whether the token reporting goroutine is active
	meteringRunning bool
	
	// Enhanced metering components (only initialized when EnhancedMetering is enabled)
	tokenCounter      interfaces.TokenCounter
	bodyHandler       interfaces.BodyHandler
	tokenAccumulator  *TokenAccumulator
	resilientReporter *ResilientReporter
	metricsCollector  interfaces.MetricsCollector
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
	m.validator = apikeyval.NewAPIKeyValidator(m.Config)
	if m.validator == nil {
		return fmt.Errorf("failed to create API key validator")
	}

	// BLOCK 3: Enhanced Token Metering (always enabled)
	logger.Info("🔧 Initializing enhanced metering components")
	
	// Initialize TokenCounter
	m.tokenCounter = factories.CreateTokenCounter()
	if m.tokenCounter != nil {
		logger.Info("✅ Enhanced TokenCounter initialized")
	} else {
		return fmt.Errorf("failed to initialize enhanced TokenCounter")
	}
	
	// Initialize BodyHandler with configured max body size
	m.bodyHandler = factories.CreateBodyHandler(m.Config.MaxBodySize)
	if m.bodyHandler != nil {
		logger.Info("✅ Enhanced BodyHandler initialized")
	} else {
		return fmt.Errorf("failed to initialize enhanced BodyHandler")
	}
	
	// Initialize optional advanced components
	m.metricsCollector = factories.CreateMetricsCollector(m.Config)
	if m.metricsCollector != nil {
		logger.Info("✅ Enhanced MetricsCollector initialized")
	} else {
		logger.Warn("⚠️  MetricsCollector not available (advanced features disabled)")
	}
	
	logger.Info("✅ Enhanced metering system initialized successfully")
	
	// Initialize quit channel for background goroutine management
	m.quitChan = make(chan struct{})
	if m.Config.Metering {
		logger.Info("⚖️  Starting Enhanced Metering...")
		// Initialize token accumulator
		m.tokenAccumulator = NewTokenAccumulator()
		
		// Create resilient reporter with the accumulator
		m.resilientReporter = NewResilientReporter(m.Config, m.tokenAccumulator)
		if m.resilientReporter != nil {
			m.resilientReporter.StartReportingLoop(m.Config.MeteringInterval)
			logger.Info("✅ Enhanced resilient reporter with accumulator started")
		} else {
			logger.Warn("⚠️  Enhanced resilient reporter not available - token usage will be logged only")
		}
		m.meteringRunning = true
	}
	// BLOCK 4: Configuration Logging
	// Log configuration details for debugging (excluding sensitive data)
	logger.Info("Secret reverse proxy middleware provisioned",
		zap.String("secret_node", m.Config.SecretNode),
		zap.String("secret_chain_id", m.Config.SecretChainID),
		zap.String("contract_address", m.Config.ContractAddress),
		zap.Duration("cache_ttl", m.Config.CacheTTL),
		zap.String("master_keys_file", m.Config.MasterKeysFile),
		zap.String("master_key_configured", m.Config.APIKey),
		zap.String("permit_file_configured", m.Config.PermitFile),
		zap.Bool("metering", m.Config.Metering),
		zap.Duration("metering_interval", m.Config.MeteringInterval),
		zap.String("metering_url", m.Config.MeteringURL),
		zap.String("token_counting_mode", m.Config.TokenCountingMode),
		zap.Int64("max_body_size", m.Config.MaxBodySize),
		zap.Bool("enable_metrics", m.Config.EnableMetrics))

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
		zap.String("secret_node", m.Config.SecretNode),
		zap.String("secret_chain_id", m.Config.SecretChainID),
		zap.String("contract_address", m.Config.ContractAddress),
		zap.String("secret_node", m.Config.SecretNode),
		zap.Duration("cache_ttl", m.Config.CacheTTL),
		zap.Bool("metering", m.Config.Metering),
		zap.Duration("metering_interval", m.Config.MeteringInterval),
		zap.String("metering_url", m.Config.MeteringURL))

	
	// BLOCK 2: Required Field Validation
	// Check that essential configuration parameters are provided

	if m.Config.SecretNode == "" {
		err := fmt.Errorf("secret node is required")
		logger.Error("Validation failed", zap.Error(err))
		return err
	}

	if m.Config.SecretChainID == "" {
		err := fmt.Errorf("secret chain id is required")
		logger.Error("Validation failed", zap.Error(err))
		return err
	}

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
	
	if m.Config.Metering {
		if m.Config.MeteringURL == "" {
			err := fmt.Errorf("metering URL is required")
			logger.Error("Validation failed", zap.Error(err))
			return err
		}

		if m.Config.MeteringInterval <= 0 {
			err := fmt.Errorf("metering interval must be positive, got %v", m.Config.MeteringInterval)
			logger.Error("Validation failed", zap.Error(err))
			return err
		}
	}

	logger.Info("✅ Configuration validation successful")
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
	startTime := time.Now()
	
	// Record request metrics
	if m.metricsCollector != nil {
		m.metricsCollector.RecordRequest()
	}
	
	// BLOCK 1: Request Logging
	// Log incoming request details for debugging and audit purposes
	logger.Debug("Processing request through secret reverse proxy middleware",
		zap.String("method", r.Method),
		zap.String("path", r.URL.Path),
		zap.String("remote_addr", r.RemoteAddr))

	// BLOCK 1.5: Metrics Endpoint Handling
	// Check if this is a metrics request and handle it directly
	if m.Config.EnableMetrics && r.URL.Path == m.Config.MetricsPath {
		logger.Debug("Handling metrics endpoint request")
		if m.metricsCollector != nil {
			m.metricsCollector.ServeMetrics(w, r)
			return nil
		}
		// Fallback if metrics collector is not available
		http.Error(w, "Metrics not available", http.StatusServiceUnavailable)
		return nil
	}
	
	// BLOCK 1.7: URL Filtering Check
	// Check if the request URL matches any blocked patterns
	if len(m.Config.BlockedURLs) > 0 {
		// Build full request URL including host and port for filtering
		requestURL := r.Host + r.URL.Path
		if r.URL.RawQuery != "" {
			requestURL += "?" + r.URL.RawQuery
		}
		
		for _, blockedPattern := range m.Config.BlockedURLs {
			if strings.Contains(requestURL, blockedPattern) {
				logger.Info("Request blocked: URL contains blocked pattern",
					zap.String("remote_addr", r.RemoteAddr),
					zap.String("full_url", requestURL),
					zap.String("blocked_pattern", blockedPattern))
				
				// Record rejection metrics
				if m.metricsCollector != nil {
					m.metricsCollector.RecordRejected()
				}
				
				http.Error(w, "URL blocked by filter", http.StatusForbidden)
				return nil
			}
		}
	}

	// BLOCK 2: Authorization Header Extraction
	// Check if the request contains an Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		// No authorization header = immediate rejection
		logger.Debug("Request rejected: missing Authorization header",
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("path", r.URL.Path))
		
		// Record rejection metrics
		if m.metricsCollector != nil {
			m.metricsCollector.RecordRejected()
		}
		
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
		
		// Record rejection metrics
		if m.metricsCollector != nil {
			m.metricsCollector.RecordRejected()
		}
		
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
		
		// Record validation error
		if m.metricsCollector != nil {
			m.metricsCollector.RecordValidationError()
		}
		
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
		
		// Record rejection metrics
		if m.metricsCollector != nil {
			m.metricsCollector.RecordRejected()
		}
		
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return nil
	}

	// BLOCK 6: Request Forwarding
	// Authentication successful - forward request to next handler in chain
	// logger.Debug("API key validation successful, forwarding request")
	// return next.ServeHTTP(w, r)

	// Record successful authorization
	if m.metricsCollector != nil {
		m.metricsCollector.RecordAuthorized()
	}

	// Read request body BEFORE forwarding to downstream handlers
	tokenCountStart := time.Now()
	requestBody, contentType := m.readRequestBody(r)

	// Detect model from request body
	modelName := detectModelFromRequestBody(requestBody, contentType)

	inputTokens := m.countTokens(requestBody, contentType, modelName)

	// Record token counting time
	if m.metricsCollector != nil {
		m.metricsCollector.RecordTokenCountTime(time.Since(tokenCountStart))
	}

	wrappedWriter := m.createResponseWriter(w)
	err = next.ServeHTTP(wrappedWriter, r)
	if err != nil {
		return err
	}

	// Count response tokens
	responseTokenCountStart := time.Now()
	responseBody, responseContentType := m.extractResponseBody(wrappedWriter)
	outputTokens := m.countTokens(responseBody, responseContentType, modelName)

	// Record response token counting time
	if m.metricsCollector != nil {
		m.metricsCollector.RecordTokenCountTime(time.Since(responseTokenCountStart))
		// Record token metrics
		m.metricsCollector.RecordTokens(int64(inputTokens), int64(outputTokens))
		// Record processing time
		m.metricsCollector.RecordProcessingTime(time.Since(startTime))
	}

	// Record usage for reporting - create API key hash
	if m.tokenAccumulator != nil && (inputTokens > 0 || outputTokens > 0) {
		hasher := sha256.New()
		hasher.Write([]byte(apiKey))
		apiKeyHash := hex.EncodeToString(hasher.Sum(nil))
		
		m.tokenAccumulator.RecordUsageWithModel(apiKeyHash, modelName, inputTokens, outputTokens)
	}

	// Log token usage for monitoring (enhanced system handles reporting internally)
	caddy.Log().Info("Token usage recorded",
		zap.Int("input_tokens", inputTokens),
		zap.Int("output_tokens", outputTokens),
		zap.String("model", modelName),
		zap.String("endpoint", r.URL.Path))

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
		m.Config = proxyconfig.DefaultConfig()
		logger.Debug("Using default configuration")
	}
	
	// BLOCK 1.5: Environment Variable Processing
	// Check for BLOCK_URLS environment variable and parse it
	if blockURLsEnv := os.Getenv("BLOCK_URLS"); blockURLsEnv != "" {
		// Parse comma-separated URLs from environment variable
		blockedURLs := strings.Split(blockURLsEnv, ",")
		for i, url := range blockedURLs {
			blockedURLs[i] = strings.TrimSpace(url)
		}
		// Filter out empty strings
		var filteredURLs []string
		for _, url := range blockedURLs {
			if url != "" {
				filteredURLs = append(filteredURLs, url)
			}
		}
		m.Config.BlockedURLs = filteredURLs
		logger.Info("🚫 Blocked URLs configured from environment", 
			zap.Strings("blocked_urls", m.Config.BlockedURLs),
			zap.Int("count", len(m.Config.BlockedURLs)))
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
					rawValue := d.Val()
					expandedValue := expandEnvVars(rawValue)
					interval, err := time.ParseDuration(expandedValue)
					if err != nil {
						return d.Errf("invalid metering_interval: %v", err)
					}
					m.Config.MeteringInterval = interval
					logger.Info("⏰  Metering", zap.Int("Interval", int(m.Config.MeteringInterval)))

				case "metering":
					if !d.NextArg() {
						return d.ArgErr()
					}
					expandedValue := expandEnvVars(d.Val())
					m.Config.Metering = map[string]bool{
						"on": true, "true": true, "1": true,
					}[strings.ToLower(strings.TrimSpace(expandedValue))]
					logger.Info("⚖️  Metering", zap.Bool("ON/OFF", m.Config.Metering))

				case "metering_url":
					logger.Info("Processing metering_url directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 metering_url directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw metering_url value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.MeteringURL = expandedValue
					
					logger.Info("🔗 Metering URL configured successfully:", zap.String("metering_url", m.Config.MeteringURL))

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
					logger.Info("Processing master_keys_file directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 master_keys_file directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw master_keys_file value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.MasterKeysFile = expandedValue
					
					logger.Info("🔑 Master keys file path configured successfully:", zap.String("master_keys_file", m.Config.MasterKeysFile))

				case "permit_file":
					// Secret Network permit file path
					logger.Info("Processing permit_file directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 permit_file directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw permit_file value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.PermitFile = expandedValue
					
					logger.Info("🔑 Permit file configured successfully:", zap.String("permit_file", m.Config.PermitFile))

				case "contract_address":
					// Smart contract address for API key validation
					logger.Info("Processing contract_address directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 contract_address directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw contract_address value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.ContractAddress = expandedValue
					
					logger.Info("🔑 Contract address configured successfully:", zap.String("contract_address", m.Config.ContractAddress))

				case "secret_chain_id":
					logger.Info("Processing secret_chain_id directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 secret_chain_id directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw secret_chain_id value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.SecretChainID = expandedValue
					
					logger.Info("🔗 Secret chain ID configured successfully:", zap.String("secret_chain_id", m.Config.SecretChainID))

				case "secret_node":
					logger.Info("Processing secret_node directive")
					
					// Get the next token manually to handle environment variables
					if !d.NextArg() {
						logger.Error("🔴 secret_node directive missing argument")
						return d.ArgErr()
					}
					
					rawValue := d.Val()
					logger.Info("Raw secret_node value:", zap.String("raw", rawValue))
					
					// Expand environment variables if present
					expandedValue := expandEnvVars(rawValue)
					m.Config.SecretNode = expandedValue
					
					logger.Info("💻 Secret node configured successfully:", zap.String("secret_node", m.Config.SecretNode))

				case "max_body_size":
					if !d.NextArg() {
						return d.ArgErr()
					}
					rawValue := expandEnvVars(d.Val())
					if size, err := parseByteSize(rawValue); err == nil {
						m.Config.MaxBodySize = size
					} else {
						return d.Errf("invalid max_body_size: %v", err)
					}
					logger.Info("📏 Max body size", zap.Int64("bytes", m.Config.MaxBodySize))

				case "token_counting_mode":
					if !d.NextArg() {
						return d.ArgErr()
					}
					expandedValue := expandEnvVars(d.Val())
					validModes := map[string]bool{
						"heuristic": true, "fast": true, "accurate": true,
					}
					if !validModes[expandedValue] {
						return d.Errf("invalid token_counting_mode: %s (valid: heuristic, fast, accurate)", expandedValue)
					}
					m.Config.TokenCountingMode = expandedValue
					logger.Info("🔢 Token counting mode", zap.String("mode", m.Config.TokenCountingMode))

				case "max_retries":
					if !d.NextArg() {
						return d.ArgErr()
					}
					rawValue := expandEnvVars(d.Val())
					if retries, err := strconv.Atoi(rawValue); err == nil && retries >= 0 {
						m.Config.MaxRetries = retries
					} else {
						return d.Errf("invalid max_retries: %v", err)
					}
					logger.Info("🔄 Max retries", zap.Int("retries", m.Config.MaxRetries))

				case "retry_backoff":
					if !d.NextArg() {
						return d.ArgErr()
					}
					rawValue := expandEnvVars(d.Val())
					if backoff, err := time.ParseDuration(rawValue); err == nil {
						m.Config.RetryBackoff = backoff
					} else {
						return d.Errf("invalid retry_backoff: %v", err)
					}
					logger.Info("⏱️  Retry backoff", zap.Duration("backoff", m.Config.RetryBackoff))

				case "enable_metrics":
					if !d.NextArg() {
						return d.ArgErr()
					}
					expandedValue := expandEnvVars(d.Val())
					m.Config.EnableMetrics = map[string]bool{
						"on": true, "true": true, "1": true, "yes": true,
					}[strings.ToLower(strings.TrimSpace(expandedValue))]
					logger.Info("📊 Enable metrics", zap.Bool("enabled", m.Config.EnableMetrics))

				case "metrics_path":
					if !d.NextArg() {
						return d.ArgErr()
					}
					expandedValue := expandEnvVars(d.Val())
					m.Config.MetricsPath = expandedValue
					logger.Info("📍 Metrics path", zap.String("path", m.Config.MetricsPath))

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
		zap.String("secret_node", m.Config.SecretNode),
		zap.String("secret_chain_id", m.Config.SecretChainID),
		zap.Bool("metering", m.Config.Metering),
		zap.Duration("metering_interval", m.Config.MeteringInterval),
		zap.String("metering_url", m.Config.MeteringURL),
	)
	return nil
}

// parseByteSize parses byte size strings like "1MB", "500KB", etc.
func parseByteSize(value string) (int64, error) {
	value = strings.TrimSpace(strings.ToUpper(value))
	
	// Handle pure numbers (bytes)
	if size, err := strconv.ParseInt(value, 10, 64); err == nil {
		return size, nil
	}
	
	// Handle size with units
	multipliers := map[string]int64{
		"B":  1,
		"KB": 1024,
		"MB": 1024 * 1024,
		"GB": 1024 * 1024 * 1024,
	}
	
	for suffix, multiplier := range multipliers {
		if strings.HasSuffix(value, suffix) {
			numStr := strings.TrimSuffix(value, suffix)
			if num, err := strconv.ParseInt(numStr, 10, 64); err == nil {
				return num * multiplier, nil
			}
		}
	}
	
	return 0, fmt.Errorf("invalid byte size format: %s", value)
}

// countTokens counts tokens using the enhanced token counting system with model awareness
func (m *Middleware) countTokens(content, contentType, model string) int {
	if content == "" {
		return 0
	}

	// Enhanced token counting with accurate model-specific tokenizers
	if m.tokenCounter != nil {
		// Use new accurate method with model parameter
		tokens := m.tokenCounter.CountTokensWithModel(content, contentType, model)

		// Validate and adjust token count if needed
		adjustedTokens := m.tokenCounter.ValidateTokenCount(tokens, len(content))
		if adjustedTokens != tokens {
			if m.metricsCollector != nil {
				m.metricsCollector.RecordTokenCountError()
			}
			caddy.Log().Debug("Token count adjusted",
				zap.Int("original", tokens),
				zap.Int("adjusted", adjustedTokens),
				zap.Int("content_length", len(content)),
				zap.String("model", model))
		}

		return adjustedTokens
	}

	// If enhanced system is not available, log error and return 0
	caddy.Log().Warn("Enhanced token counter not available, cannot count tokens",
		zap.String("content_type", contentType),
		zap.Int("content_length", len(content)),
		zap.String("model", model))
	return 0
}

// readRequestBody reads request body using the enhanced body handler
func (m *Middleware) readRequestBody(r *http.Request) (content, contentType string) {
	if m.bodyHandler != nil {
		// Use enhanced body handler
		bodyInfo, err := m.bodyHandler.SafeReadRequestBody(r)
		if err != nil {
			caddy.Log().Error("Enhanced body reading failed", zap.Error(err))
			if m.metricsCollector != nil {
				m.metricsCollector.RecordValidationError()
			}
			return "", r.Header.Get("Content-Type")
		}
		
		// Record metrics if available
		if m.metricsCollector != nil {
			if bodyInfo.Error != nil {
				m.metricsCollector.RecordTokenCountError()
			} else {
				m.metricsCollector.RecordCacheHit() // Successful operation
			}
		}
		
		return bodyInfo.Content, bodyInfo.ContentType
	}
	
	// If enhanced system is not available, log error
	caddy.Log().Warn("Enhanced body handler not available, cannot read request body")
	return "", r.Header.Get("Content-Type")
}

// createResponseWriter creates the response writer for token metering
func (m *Middleware) createResponseWriter(w http.ResponseWriter) http.ResponseWriter {
	// Always use the token metering response writer
	return NewTokenMeteringResponseWriter(w)
}

// extractResponseBody extracts response body and content type from wrapped writer
func (m *Middleware) extractResponseBody(wrappedWriter http.ResponseWriter) (content, contentType string) {
	// For now, always use simple response writer until circular dependency is resolved
	if basicWriter, ok := wrappedWriter.(*TokenMeteringResponseWriter); ok {
		return string(basicWriter.Body()), "application/json"
	}
	
	// Should not reach here, but provide safe fallback
	return "", "application/json"
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

// detectModelFromRequestBody extracts the model name from JSON request body.
// This function parses the request body as JSON and searches for a "model" field.
//
// Parameters:
//   - requestBody: The request body content
//   - contentType: The content type of the request
//
// Returns:
//   - string: The detected model name, or "unknown" if not found or not JSON
func detectModelFromRequestBody(requestBody, contentType string) string {
	// Only process JSON content
	if !strings.Contains(strings.ToLower(contentType), "application/json") {
		return "unknown"
	}
	
	// Parse JSON to extract model field
	var jsonData map[string]any
	if err := json.Unmarshal([]byte(requestBody), &jsonData); err != nil {
		// Not valid JSON
		return "unknown"
	}
	
	// Look for model field
	if model, exists := jsonData["model"]; exists {
		if modelStr, ok := model.(string); ok && modelStr != "" {
			return modelStr
		}
	}
	
	return "unknown"
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

// Cleanup implements caddy.CleanerUpper and is called by Caddy to clean up resources
// when the module is being shut down or reloaded. This prevents memory leaks from
// background goroutines and other allocated resources.
//
// Returns:
//   - error: nil on successful cleanup, error if cleanup fails
func (m *Middleware) Cleanup() error {
	logger := caddy.Log()
	logger.Debug("Cleaning up secret reverse proxy middleware")

	// Stop any background token reporting goroutines
	if m.meteringRunning && m.quitChan != nil {
		logger.Debug("Stopping legacy token reporting goroutine")
		close(m.quitChan)
		m.meteringRunning = false
		logger.Debug("Legacy token reporting goroutine stopped")
	}
	
	// Stop the resilient reporter
	if m.resilientReporter != nil {
		logger.Debug("Stopping resilient reporter")
		m.resilientReporter.Stop()
		logger.Debug("Resilient reporter stopped")
	}

	// Clean up validator resources (if any)
	if m.validator != nil {
		logger.Debug("Cleaning up validator cache")
		m.validator.CleanupCache()
		logger.Debug("Validator cache cleaned up")}

	// Enhanced metering components handle their own cleanup

	logger.Debug("Secret reverse proxy middleware cleanup completed")
	return nil
}

// Interface guards
var (
	_ caddy.Module                = (*Middleware)(nil)
	_ caddy.Provisioner           = (*Middleware)(nil)
	_ caddy.Validator             = (*Middleware)(nil)
	_ caddyhttp.MiddlewareHandler = (*Middleware)(nil)
	_ caddyfile.Unmarshaler       = (*Middleware)(nil)
	_ caddy.CleanerUpper          = (*Middleware)(nil)
)
