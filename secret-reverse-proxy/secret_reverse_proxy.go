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
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

	proxyconfig "github.com/scrtlabs/secret-reverse-proxy/config"
	"github.com/scrtlabs/secret-reverse-proxy/factories"
	"github.com/scrtlabs/secret-reverse-proxy/interfaces"
	apikeyval "github.com/scrtlabs/secret-reverse-proxy/validators"
	"github.com/scrtlabs/secret-reverse-proxy/x402"
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

	// x402 portal-based components (nil when x402 is disabled)
	portalClient     *x402.PortalClient
	challengeBuilder *x402.ChallengeBuilder
	x402MinBalance   float64 // minimum balance in USD
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

// optionalConfigSource returns a log-friendly description for an optional config param.
// If the config value is set, it returns it. If not, it checks whether the corresponding
// env var(s) are set and reports that instead.
func optionalConfigSource(configValue string, envVarNames ...string) string {
	if configValue != "" {
		return configValue
	}
	var setVars []string
	for _, name := range envVarNames {
		if os.Getenv(name) != "" {
			setVars = append(setVars, name)
		}
	}
	if len(setVars) > 0 {
		return "(using env: " + strings.Join(setVars, ", ") + ")"
	}
	return "(not configured)"
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
		m.Config = proxyconfig.DefaultConfig()
		logger.Info("No configuration provided, using defaults")
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
	// BLOCK 3.5: x402 Portal-Based Payment Protocol Initialization
	if m.Config.X402Enabled {
		logger.Info("Initializing x402 portal-based payment protocol")

		m.portalClient = x402.NewPortalClient(
			m.Config.DevPortalURL,
			m.Config.DevPortalServiceKey,
			logger,
		)

		// Determine topup URL: explicit override or derived from portal URL
		topupURL := m.Config.X402TopupURL
		if topupURL == "" {
			topupURL = m.Config.DevPortalURL + "/api/agent/add-funds"
		}
		m.challengeBuilder = x402.NewChallengeBuilder(topupURL)

		m.x402MinBalance = m.Config.X402MinBalanceUSD

		logger.Info("x402 portal-based payment protocol initialized",
			zap.String("devportal_url", m.Config.DevPortalURL),
			zap.Float64("min_balance_usd", m.x402MinBalance))
	}

	// BLOCK 4: Configuration Logging
	// Log configuration details for debugging (excluding sensitive data)
	logger.Info("Secret reverse proxy middleware provisioned",
		zap.String("secret_node", m.Config.SecretNode),
		zap.String("secret_chain_id", m.Config.SecretChainID),
		zap.String("contract_address", m.Config.ContractAddress),
		zap.Duration("cache_ttl", m.Config.CacheTTL),
		zap.String("master_keys", optionalConfigSource(m.Config.MasterKeysFile, "SECRETAI_MASTER_KEYS")),
		zap.Bool("master_key_configured", m.Config.APIKey != ""),
		zap.String("permit", optionalConfigSource(m.Config.PermitFile, "SECRETAI_PERMIT_TYPE", "SECRETAI_PERMIT_PUBKEY", "SECRETAI_PERMIT_SIG")),
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
		zap.Bool("api_key_set", m.Config.APIKey != ""),
		zap.String("master_keys", optionalConfigSource(m.Config.MasterKeysFile, "SECRETAI_MASTER_KEYS")),
		zap.String("permit", optionalConfigSource(m.Config.PermitFile, "SECRETAI_PERMIT_TYPE", "SECRETAI_PERMIT_PUBKEY", "SECRETAI_PERMIT_SIG")),
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

	if strings.TrimSpace(m.Config.ContractAddress) == "" {
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
	} else {
		// No permit file configured — ensure the required env vars are set
		permitType := os.Getenv("SECRETAI_PERMIT_TYPE")
		permitPubKey := os.Getenv("SECRETAI_PERMIT_PUBKEY")
		permitSig := os.Getenv("SECRETAI_PERMIT_SIG")
		if permitType == "" || permitPubKey == "" || permitSig == "" {
			var missing []string
			if permitType == "" {
				missing = append(missing, "SECRETAI_PERMIT_TYPE")
			}
			if permitPubKey == "" {
				missing = append(missing, "SECRETAI_PERMIT_PUBKEY")
			}
			if permitSig == "" {
				missing = append(missing, "SECRETAI_PERMIT_SIG")
			}
			if len(missing) > 0 {
				logger.Warn("Permit not fully configured, contract queries may fail",
					zap.Strings("missing_vars", missing))
			}
		}
		logger.Info("No permit file configured, using permit env vars (SECRETAI_PERMIT_TYPE, SECRETAI_PERMIT_PUBKEY, SECRETAI_PERMIT_SIG)")
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

	// x402 validation
	if m.Config.X402Enabled {
		if m.Config.DevPortalURL == "" {
			return fmt.Errorf("devportal_url is required when x402 is enabled")
		}
		if m.Config.DevPortalServiceKey == "" {
			return fmt.Errorf("devportal_service_key is required when x402 is enabled")
		}
		if m.Config.X402MinBalanceUSD <= 0 {
			return fmt.Errorf("x402_min_balance_usd must be positive when x402 is enabled")
		}
	}

	logger.Info("Configuration validation successful")
	return nil
}

// billingEnabled reports whether any billing mode is active for this
// middleware instance: x402 portal-based billing (agent requests) or
// metering-based billing (accumulator → ResilientReporter, for legacy
// Bearer-token users). The legacy body gates below (oversized-body 413,
// model-less-POST 400) apply only when billing is enabled; with billing off
// this middleware is a pure auth proxy and must keep proxying such requests
// unchanged.
func (m Middleware) billingEnabled() bool {
	return (m.Config.X402Enabled && m.portalClient != nil) || m.Config.Metering
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

	// BLOCK 1.6: x402 Portal-Based Payment Path
	// Requests carrying x-agent-address take the x402 path (EIP-191 signature + portal balance).
	// Legacy Bearer-token path is unchanged below.
	if m.Config.X402Enabled && m.portalClient != nil {
		if walletAddr := r.Header.Get("X-Agent-Address"); walletAddr != "" {
			return m.serveX402(w, r, next, walletAddr)
		}
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

	// BLOCK 4: Standard API Key Validation (legacy path)
	logger.Debug("Validating API key",
		zap.String("key_prefix", apiKey[:min(8, len(apiKey))]+"..."))

	isValid, err := m.validator.ValidateAPIKey(apiKey)
	if err != nil {
		logger.Warn("API key validation failed, denying access",
			zap.Error(err),
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("path", r.URL.Path))
		if m.metricsCollector != nil {
			m.metricsCollector.RecordRejected()
		}
		http.Error(w, "Invalid API key", http.StatusUnauthorized)
		return nil
	}

	if !isValid {
		logger.Info("Request rejected: invalid API key",
			zap.String("remote_addr", r.RemoteAddr),
			zap.String("path", r.URL.Path),
			zap.String("key_prefix", apiKey[:min(8, len(apiKey))]+"..."))
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

	// Read request body BEFORE forwarding to downstream handlers
	tokenCountStart := time.Now()
	requestBody, _, bodyTruncated := m.readRequestBody(r)

	// Computed once and reused below: free-pass paths (e.g. /api/show) skip
	// the model-less-POST 400 gate, the balance check, and token-accumulator
	// recording even when their body happens to contain a "model" field. The
	// 413 truncation gate below still applies to them — an unbufferable body
	// is unbufferable regardless of path.
	freePass := isFreePassPath(r.URL.Path)

	// A truncated body can't be reliably model-detected or billed. Scoped to
	// billing-enabled deployments only, same as the "no detectable model" gate
	// below: with billing off this middleware is a pure auth proxy and must
	// keep proxying oversized bodies through complete and unmodified (some
	// backends handle their own limits) — SafeReadRequestBody restores the
	// full body (buffered prefix + unread remainder) precisely so this proxy
	// path never forwards a silently truncated request.
	if bodyTruncated && m.billingEnabled() {
		logger.Info("Rejecting request with oversized body",
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr))
		if m.metricsCollector != nil {
			m.metricsCollector.RecordRejected()
		}
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return nil
	}

	// Detect model from request body
	modelName := detectModelFromRequestBody(requestBody)

	// Fail closed: a POST with no detectable model is billable by default and
	// only proxied for free if the path is explicitly on the free-pass list
	// (see isFreePassPath). Non-POST requests (GET/HEAD/OPTIONS/...) are
	// always free-passed, preserving discovery/health-check behavior.
	// Scoped to billing-enabled deployments only: when billing is off, this
	// middleware runs as a pure auth proxy and some backends legitimately default
	// the model when the field is omitted, so we must not break that proxy-through
	// behavior.
	if m.billingEnabled() && modelName == "unknown" &&
		r.Method == http.MethodPost && !freePass {
		logger.Info("Rejecting inference request with no detectable model",
			zap.String("path", r.URL.Path),
			zap.String("remote_addr", r.RemoteAddr))
		if m.metricsCollector != nil {
			m.metricsCollector.RecordRejected()
		}
		http.Error(w, "Missing or invalid model in request body", http.StatusBadRequest)
		return nil
	}

	// Record successful authorization — only now, after the body gates above
	// have had their chance to reject: a gate-rejected request must count
	// only as rejected, never as both authorized and rejected.
	if m.metricsCollector != nil {
		m.metricsCollector.RecordAuthorized()
	}

	// Pre-request balance check for legacy users (only for LLM requests that
	// have a model field; free-pass paths are exempt from billing entirely).
	if m.Config.X402Enabled && m.portalClient != nil && modelName != "unknown" && !freePass {
		hasher := sha256.New()
		hasher.Write([]byte(apiKey))
		userApiKeyHash := hex.EncodeToString(hasher.Sum(nil))
		userBalance, balanceErr := m.portalClient.GetUserBalance(userApiKeyHash)
		if balanceErr != nil {
			// Fail-open: if portal is unreachable, allow the request (key was already validated)
			logger.Warn("User balance check failed, allowing request",
				zap.Error(balanceErr),
				zap.String("api_key_hash", userApiKeyHash[:8]+"..."))
		} else if userBalance >= 0 && userBalance < m.x402MinBalance {
			logger.Info("Legacy user: insufficient balance",
				zap.Float64("balance", userBalance),
				zap.Float64("required", m.x402MinBalance),
				zap.String("api_key_hash", userApiKeyHash[:8]+"..."))
			return m.challengeBuilder.Build402Response(w, userBalance, m.x402MinBalance)
		}
	}

	// Count input tokens from message text only (not full JSON with metadata).
	inputContent := extractRequestMessageContent(requestBody)
	if inputContent == "" {
		inputContent = requestBody
	}
	inputTokens := m.countTokens(inputContent, "text/plain", modelName)

	// Record token counting time
	if m.metricsCollector != nil {
		m.metricsCollector.RecordTokenCountTime(time.Since(tokenCountStart))
	}

	// Prepare forwarded body: inject operator defaults + stream_options.include_usage=true.
	// stream_options injection ensures vLLM returns authoritative usage in the final SSE chunk.
	// This runs AFTER token counting so input billing uses the original prompt length.
	{
		forwarded := requestBody
		if m.Config != nil && (m.Config.DefaultThink != "" || m.Config.DefaultNumPredict != 0) {
			forwarded = injectRequestDefaults(forwarded,
				m.Config.DefaultThink, m.Config.DefaultNumPredict)
		}
		forwarded = injectStreamUsageOption(forwarded)
		if forwarded != requestBody {
			bodyBytes := []byte(forwarded)
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
			r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
		}
	}

	wrappedWriter := m.createResponseWriter(w)
	err = next.ServeHTTP(wrappedWriter, r)
	if err != nil {
		return err
	}

	responseStatus := http.StatusOK
	if statusWriter, ok := wrappedWriter.(*TokenMeteringResponseWriter); ok {
		responseStatus = statusWriter.Status()
	}
	upstreamOK := responseStatus >= 200 && responseStatus < 300

	// Count response tokens
	responseTokenCountStart := time.Now()
	responseBody, _ := m.extractResponseBody(wrappedWriter)
	if modelName == "unknown" {
		modelName = detectModelFromResponseBody(responseBody)
	}
	// Priority 1: authoritative usage from backend (vLLM always provides this for
	// non-streaming; for streaming it requires stream_options.include_usage=true which
	// we inject above).
	var outputTokens, cachedTokens int
	if promptTok, completionTok, cachedTok, ok := extractUsageFromResponse(responseBody); ok {
		cachedTokens = cachedTok
		inputTokens = promptTok - cachedTok // non-cached input only; cached billed separately
		outputTokens = completionTok
	} else {
		// Priority 2: extract only the generated text, then tokenize.
		// This avoids counting SSE JSON wrappers or response metadata.
		content := extractResponseContent(responseBody)
		if content == "" {
			content = responseBody
		}
		outputTokens = m.countTokens(content, "text/plain", modelName)
	}

	// Record response token counting time
	if m.metricsCollector != nil {
		m.metricsCollector.RecordTokenCountTime(time.Since(responseTokenCountStart))
		// Record token metrics — only for successful upstream responses; a failed
		// upstream shouldn't inflate token counters, matching the x402 path.
		if upstreamOK {
			m.metricsCollector.RecordTokens(int64(inputTokens), int64(outputTokens))
		}
		// Record processing time
		m.metricsCollector.RecordProcessingTime(time.Since(startTime))
	}

	// Record usage for reporting — only for LLM requests (model must be known)
	// that are not on the free-pass list. Non-LLM endpoints (GET /v1/models,
	// GET /api/tags, /api/version, etc.) produce output tokens from their JSON
	// responses but should never be billed; free-pass paths (e.g. /api/show)
	// must never be billed either, even when their body contains a "model" field.
	if modelName != "unknown" && !freePass {
		if !upstreamOK {
			// The upstream failed: do not bill the caller for it.
			caddy.Log().Info("Upstream error, skipping usage recording",
				zap.Int("status", responseStatus),
				zap.String("model", modelName),
				zap.String("endpoint", r.URL.Path))
		} else {
			if m.tokenAccumulator != nil && (inputTokens > 0 || outputTokens > 0) {
				hasher := sha256.New()
				hasher.Write([]byte(apiKey))
				apiKeyHash := hex.EncodeToString(hasher.Sum(nil))

				m.tokenAccumulator.RecordUsageWithModel(apiKeyHash, modelName, inputTokens, outputTokens, cachedTokens)
			}

			// Log token usage for monitoring (enhanced system handles reporting internally)
			caddy.Log().Info("Token usage recorded",
				zap.Int("input_tokens", inputTokens),
				zap.Int("output_tokens", outputTokens),
				zap.Int("cached_tokens", cachedTokens),
				zap.String("model", modelName),
				zap.String("endpoint", r.URL.Path))
		}
	}

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
//
//	secret_reverse_proxy {
//	    API_MASTER_KEY "your-master-key-here"
//	    master_keys_file "/etc/caddy/master_keys.txt"
//	    contract_address "secret1abc..."
//	}
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

				// Expand environment variables if present
				expandedValue := expandEnvVars(rawValue)
				m.Config.APIKey = expandedValue

				if m.Config.APIKey != "" {
					logger.Info("🔑 Primary master key configured successfully")
				} else {
					logger.Warn("⚠️  API_MASTER_KEY directive present but value is empty")
				}

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

			case "default_think":
				if !d.NextArg() {
					return d.ArgErr()
				}
				val := strings.ToLower(strings.TrimSpace(expandEnvVars(d.Val())))
				valid := map[string]bool{"low": true, "medium": true, "high": true, "off": true}
				if !valid[val] {
					return d.Errf("invalid default_think %q (must be low, medium, high, or off)", val)
				}
				m.Config.DefaultThink = val
				logger.Info("🧠 Default think level", zap.String("think", val))

			case "default_num_predict":
				if !d.NextArg() {
					return d.ArgErr()
				}
				n, err := strconv.Atoi(strings.TrimSpace(expandEnvVars(d.Val())))
				if err != nil {
					return d.Errf("invalid default_num_predict: %v", err)
				}
				m.Config.DefaultNumPredict = n
				logger.Info("📏 Default num_predict", zap.Int("num_predict", n))

			// x402 Payment Protocol directives (portal-based)
			case "x402_enabled":
				if !d.NextArg() {
					return d.ArgErr()
				}
				expandedValue := expandEnvVars(d.Val())
				m.Config.X402Enabled = map[string]bool{
					"on": true, "true": true, "1": true, "yes": true,
				}[strings.ToLower(strings.TrimSpace(expandedValue))]
				logger.Info("x402 enabled", zap.Bool("enabled", m.Config.X402Enabled))

			case "devportal_url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.Config.DevPortalURL = expandEnvVars(d.Val())
				logger.Info("DevPortal URL configured", zap.String("url", m.Config.DevPortalURL))

			case "devportal_service_key":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.Config.DevPortalServiceKey = expandEnvVars(d.Val())
				logger.Info("DevPortal service key configured")

			case "x402_min_balance_usd":
				if !d.NextArg() {
					return d.ArgErr()
				}
				parsedMinBalance, parseErr := strconv.ParseFloat(expandEnvVars(d.Val()), 64)
				if parseErr != nil {
					return d.Errf("invalid x402_min_balance_usd value: %v", parseErr)
				}
				m.Config.X402MinBalanceUSD = parsedMinBalance
				logger.Info("x402 min balance configured", zap.Float64("min_balance_usd", m.Config.X402MinBalanceUSD))

			case "x402_topup_url":
				if !d.NextArg() {
					return d.ArgErr()
				}
				m.Config.X402TopupURL = expandEnvVars(d.Val())
				logger.Info("x402 topup URL configured", zap.String("url", m.Config.X402TopupURL))

			default:
				logger.Error("Unmarshal Caddyfile: unknown subdirective", zap.String("directive", d.Val()))
				// Unknown directive - return error
				return d.Errf("unknown subdirective: %s", d.Val())
			}
		}
		// Handle directives in outer loop (no-brace / flat format)
		switch d.Val() {
		case "cache_ttl":
			args := d.RemainingArgs()
			if len(args) != 1 {
				return d.Errf("cache_ttl requires exactly one argument")
			}
			dur, durErr := time.ParseDuration(args[0])
			if durErr != nil {
				return d.Errf("invalid cache TTL: %v", durErr)
			}
			m.Config.CacheTTL = dur
			logger.Info("⏱️  Cache TTL configured", zap.Duration("cache_ttl", m.Config.CacheTTL))
		}
		logger.Info("🔄 Finished processing block for token:", zap.String("token", d.Val()))
	}
	logger.Info("✅ Final configuration:",
		zap.Bool("api_key_set", m.Config.APIKey != ""),
		zap.String("master_keys", optionalConfigSource(m.Config.MasterKeysFile, "SECRETAI_MASTER_KEYS")),
		zap.String("permit", optionalConfigSource(m.Config.PermitFile, "SECRETAI_PERMIT_TYPE", "SECRETAI_PERMIT_PUBKEY", "SECRETAI_PERMIT_SIG")),
		zap.String("contract_address", m.Config.ContractAddress),
		zap.String("secret_node", m.Config.SecretNode),
		zap.String("secret_chain_id", m.Config.SecretChainID),
		zap.Bool("metering", m.Config.Metering),
		zap.Duration("metering_interval", m.Config.MeteringInterval),
		zap.String("metering_url", m.Config.MeteringURL),
	)
	return nil
}

// defaultMaxBodySize is the fallback cap applied when Config.MaxBodySize is
// unset/zero. Kept equal to proxyconfig.DefaultMaxBodySize (which also backs
// config.DefaultConfig() and metering.NewBodyHandler's own <= 0 fallback) so
// all three defaults move together instead of silently drifting apart.
const defaultMaxBodySize int64 = proxyconfig.DefaultMaxBodySize

// effectiveMaxBodySize returns configured, falling back to defaultMaxBodySize
// when configured is unset/zero.
func effectiveMaxBodySize(configured int64) int64 {
	if configured <= 0 {
		return defaultMaxBodySize
	}
	return configured
}

// isGzipContentEncoding reports whether encoding names a gzip-compressed
// body. "x-gzip" is a long-standing alias for "gzip" (predating the
// standardized token) that some HTTP clients still send.
func isGzipContentEncoding(encoding string) bool {
	return strings.EqualFold(encoding, "gzip") || strings.EqualFold(encoding, "x-gzip")
}

// errGzipBodyTooLarge indicates the gzip stream decompressed past maxBodySize.
// It is distinct from a gzip-format/decode error so callers can respond 413
// instead of treating the body as unparseable.
var errGzipBodyTooLarge = errors.New("decompressed body exceeds max size")

// decompressGzipBody gunzips a compressed request body for analysis purposes
// only (model detection, message-content extraction, input-token counting).
// The read is capped at maxBodySize+1 to guard against zip bombs; exceeding
// the cap returns errGzipBodyTooLarge so callers can distinguish it from a
// genuine decode failure.
func decompressGzipBody(rawBody []byte, maxBodySize int64) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(rawBody))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	decoded, err := io.ReadAll(io.LimitReader(zr, maxBodySize+1))
	if err != nil {
		return nil, err
	}
	if int64(len(decoded)) > maxBodySize {
		return nil, fmt.Errorf("%w: %d bytes", errGzipBodyTooLarge, maxBodySize)
	}
	return decoded, nil
}

// serveX402 handles requests from x402 agents identified by x-agent-address header.
// Flow: verify EIP-191 signature → check portal balance → forward → report usage.
func (m Middleware) serveX402(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler, walletAddr string) error {
	logger := caddy.Log()

	// 1. Read raw body before anything else (needed for EIP-191 signature verification).
	// The read is capped at the configured max body size: a body we cannot fully
	// buffer can't be verified (the signature covers the whole body) or billed,
	// so we reject it with 413 here, before signature verification even runs.
	var rawBody []byte
	if r.Body != nil {
		maxBodySize := effectiveMaxBodySize(m.Config.MaxBodySize)
		limited, err := io.ReadAll(io.LimitReader(r.Body, maxBodySize+1))
		if err != nil {
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return nil
		}
		r.Body.Close()
		if int64(len(limited)) > maxBodySize {
			logger.Info("x402: request body exceeds max size",
				zap.Int64("max_size", maxBodySize),
				zap.String("wallet", walletAddr))
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return nil
		}
		rawBody = limited
		// Restore body so readRequestBody and next.ServeHTTP can consume it
		r.Body = io.NopCloser(bytes.NewReader(rawBody))
	}

	// 2. Verify EIP-191 agent signature (matches DevPortal verifySignature.ts)
	timestamp := r.Header.Get("X-Agent-Timestamp")
	if err := x402.ValidateTimestamp(timestamp, 30*time.Second); err != nil {
		logger.Info("x402: invalid timestamp", zap.Error(err), zap.String("wallet", walletAddr))
		http.Error(w, "Invalid or expired agent timestamp", http.StatusUnauthorized)
		return nil
	}

	sig := r.Header.Get("X-Agent-Signature")
	ok, err := x402.VerifyAgentSignature(walletAddr, sig, r.Method, r.URL.Path, string(rawBody), timestamp)
	if err != nil || !ok {
		logger.Info("x402: signature verification failed",
			zap.Bool("ok", ok),
			zap.Error(err),
			zap.String("wallet", walletAddr))
		http.Error(w, "Invalid agent signature", http.StatusUnauthorized)
		return nil
	}

	// 2.5. Free-pass paths (e.g. /api/show) are exempt from billing entirely —
	// checked here, right after signature verification, so a free-pass route
	// is proxied through even when its body happens to contain a "model"
	// field (modern Ollama's /api/show request body is one such case). This
	// must run after signature verification above, not before: a free pass
	// on the billing gate is not a free pass on authentication.
	if isFreePassPath(r.URL.Path) {
		logger.Debug("x402: free-pass path, skipping billing entirely",
			zap.String("path", r.URL.Path),
			zap.String("wallet", walletAddr))
		return next.ServeHTTP(w, r)
	}

	// 3. Detect model from rawBody (already available from sig step above).
	// This determines whether the request is an LLM call (has "model" field) or not.
	// We do this BEFORE the balance check so that non-LLM paths (e.g. GET /v1/models,
	// health checks, embedding queries without billing) are never blocked by balance.
	//
	// A gzip-encoded body never sniffs as JSON, so for gzip requests we gunzip
	// rawBody into an analysis-only copy here and reuse it below (step 7) for
	// message-content extraction and input-token counting. This copy is never
	// forwarded upstream: the signature above was verified over rawBody, and the
	// bytes forwarded to next.ServeHTTP (step 8) stay exactly what step 1 restored,
	// so the upstream still receives the original compressed body. If the decompressed
	// size exceeds the configured cap (guarding against zip bombs), that's a 413, not a
	// format problem; any other gunzip failure means the body isn't valid gzip at all,
	// so we reject it immediately with a 400 rather than falling through to model
	// detection and reporting the misleading "missing model" error.
	isGzipRequest := isGzipContentEncoding(r.Header.Get("Content-Encoding"))
	analysisBody := rawBody
	if isGzipRequest {
		decoded, err := decompressGzipBody(rawBody, effectiveMaxBodySize(m.Config.MaxBodySize))
		if err != nil {
			if errors.Is(err, errGzipBodyTooLarge) {
				logger.Info("x402: decompressed gzip body exceeds max size",
					zap.String("wallet", walletAddr))
				http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
				return nil
			}
			logger.Info("x402: failed to gunzip request body",
				zap.Error(err),
				zap.String("wallet", walletAddr))
			http.Error(w, "Invalid gzip request body", http.StatusBadRequest)
			return nil
		}
		analysisBody = decoded
	}
	model := detectModelFromRequestBody(string(analysisBody))

	// 4. For non-LLM requests (no "model" field in body), skip balance check entirely
	// and proxy directly — these paths consume no tokens and should never be gated.
	// A GET/HEAD/etc. is always free-passed (discovery/health checks). A POST with
	// no detectable model is billable by default and fails closed instead of being
	// proxied for free (free-pass paths were already exempted above in step 2.5,
	// regardless of method or detected model).
	if model == "unknown" {
		if r.Method != http.MethodPost {
			logger.Debug("x402: non-LLM request, skipping balance check",
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
				zap.String("wallet", walletAddr))
			return next.ServeHTTP(w, r)
		}
		logger.Info("x402: rejecting inference request with no detectable model",
			zap.String("path", r.URL.Path),
			zap.String("wallet", walletAddr))
		http.Error(w, "Missing or invalid model in request body", http.StatusBadRequest)
		return nil
	}

	// 5. Check balance with DevPortal (LLM requests only)
	balance, err := m.portalClient.GetBalance(walletAddr)
	if err != nil {
		if errors.Is(err, x402.ErrPortalUnreachable) {
			logger.Error("Portal unreachable, failing closed", zap.Error(err))
			http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
			return nil
		}
		logger.Error("Portal balance check failed", zap.Error(err))
		http.Error(w, "Service temporarily unavailable", http.StatusServiceUnavailable)
		return nil
	}

	// 6. Enforce minimum balance
	if balance < m.x402MinBalance {
		logger.Info("x402: insufficient balance",
			zap.Float64("balance", balance),
			zap.Float64("required", m.x402MinBalance),
			zap.String("wallet", walletAddr))
		return m.challengeBuilder.Build402Response(w, balance, m.x402MinBalance)
	}

	// 7. Count input tokens from message text only (not the full JSON envelope).
	// readRequestBody decompresses gzip bodies itself via the metering BodyHandler,
	// but for x402 requests we already have a decompressed analysis copy from step 3
	// above — reuse it instead of gunzipping a second time, and skip readRequestBody's
	// r.Body reassignment entirely so the compressed bytes restored in step 1 remain
	// untouched for forwarding.
	var requestBody string
	if isGzipRequest {
		requestBody = string(analysisBody)
	} else {
		var truncated bool
		requestBody, _, truncated = m.readRequestBody(r)
		// Defense in depth: step 1 already capped rawBody at maxBodySize, so
		// readRequestBody should never report truncation here. If it ever
		// does (e.g. bodyHandler configured with a smaller cap than the
		// step-1 check), fail the same way the legacy path does rather than
		// billing/proxying a request we can't fully account for.
		if truncated {
			logger.Info("x402: request body unexpectedly truncated after size check",
				zap.String("wallet", walletAddr))
			http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
			return nil
		}
	}
	inputContent := extractRequestMessageContent(requestBody)
	if inputContent == "" {
		inputContent = requestBody
	}
	inputTokens := m.countTokens(inputContent, "text/plain", model)

	// Inject stream_options.include_usage=true so vLLM returns authoritative usage
	// counts. Skipped for compressed requests: rewriting the body would produce
	// plaintext JSON under a gzip Content-Encoding header, which the upstream can't
	// decode — the original compressed body (restored in step 1) is forwarded as-is.
	if !isGzipRequest {
		forwarded := injectStreamUsageOption(requestBody)
		if forwarded != requestBody {
			bodyBytes := []byte(forwarded)
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
			r.ContentLength = int64(len(bodyBytes))
			r.Header.Set("Content-Length", strconv.Itoa(len(bodyBytes)))
		}
	}

	// 8. Forward to upstream with response capture
	tw := NewTokenMeteringResponseWriter(w)
	if err := next.ServeHTTP(tw, r); err != nil {
		return err
	}

	// If the upstream failed, the client must not be billed for it: skip output-token
	// counting (an error body has no "usage" object, so the tokenizer would otherwise
	// count the error body itself as output tokens), skip reporting usage to the
	// portal, and skip token metrics. The balance check already succeeded, so the
	// request is still recorded as authorized.
	if status := tw.Status(); status < 200 || status >= 300 {
		logger.Info("x402: upstream error, skipping usage reporting",
			zap.Int("status", status),
			zap.String("wallet", walletAddr),
			zap.String("model", model))
		if m.metricsCollector != nil {
			m.metricsCollector.RecordAuthorized()
		}
		return nil
	}

	// 9. Count output tokens — prefer authoritative usage from backend response,
	// fall back to extracting generated text and running the tokenizer.
	respBody := string(tw.Body())
	if model == "unknown" {
		model = detectModelFromResponseBody(respBody)
	}
	var outputTokens, cachedTokens int
	if promptTok, completionTok, cachedTok, ok := extractUsageFromResponse(respBody); ok {
		cachedTokens = cachedTok
		inputTokens = promptTok - cachedTok // non-cached input only; cached billed separately
		if inputTokens < 0 {
			// A backend that reports cached_tokens > prompt_tokens (seen with some
			// vLLM cache-hit accounting quirks) must never bill a negative amount.
			inputTokens = 0
		}
		outputTokens = completionTok
		_ = cachedTokens // x402 path: portal ReportUsage API doesn't support cached yet
	} else if m.tokenCounter != nil {
		content := extractResponseContent(respBody)
		if content == "" {
			content = respBody
		}
		outputTokens = m.tokenCounter.CountTokensWithModel(content, "text/plain", model)
	}

	// 10. Report usage to DevPortal (we only reach here for LLM requests with model != "unknown")
	reportTimestamp := time.Now().Unix()
	go func() {
		if reportErr := m.portalClient.ReportUsage(walletAddr, model, inputTokens, outputTokens, reportTimestamp); reportErr != nil {
			logger.Error("x402: failed to report usage to portal",
				zap.Error(reportErr),
				zap.String("wallet", walletAddr),
				zap.String("model", model),
				zap.Int("input_tokens", inputTokens),
				zap.Int("output_tokens", outputTokens))
		} else {
			logger.Info("x402: usage reported to portal",
				zap.String("wallet", walletAddr),
				zap.String("model", model),
				zap.Int("input_tokens", inputTokens),
				zap.Int("output_tokens", outputTokens))
		}
	}()

	// 11. Record metrics
	if m.metricsCollector != nil {
		m.metricsCollector.RecordAuthorized()
		m.metricsCollector.RecordTokens(int64(inputTokens), int64(outputTokens))
	}

	// NOTE: do NOT record into tokenAccumulator here.
	// tokenAccumulator feeds ResilientReporter which identifies records by api_key_hash (legacy user path).
	// For x402 agents usage is already reported above via portalClient.ReportUsage (step 10).
	// Adding it to the accumulator would cause double-billing on the portal.

	return nil
}

// hashString returns a hex-encoded SHA-256 hash of the input string.
func hashString(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
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

// readRequestBody reads request body using the enhanced body handler.
// truncated reports whether the body exceeded the handler's buffer cap
// (BodyInfo.IsTruncated) — callers on billing paths use it to distinguish a
// merely-oversized body from one that is genuinely missing/invalid.
func (m *Middleware) readRequestBody(r *http.Request) (content, contentType string, truncated bool) {
	if m.bodyHandler != nil {
		// Use enhanced body handler
		bodyInfo, err := m.bodyHandler.SafeReadRequestBody(r)
		if err != nil {
			caddy.Log().Error("Enhanced body reading failed", zap.Error(err))
			if m.metricsCollector != nil {
				m.metricsCollector.RecordValidationError()
			}
			return "", r.Header.Get("Content-Type"), false
		}

		// Record metrics if available
		if m.metricsCollector != nil {
			if bodyInfo.Error != nil {
				m.metricsCollector.RecordTokenCountError()
			} else {
				m.metricsCollector.RecordCacheHit() // Successful operation
			}
		}

		return bodyInfo.Content, bodyInfo.ContentType, bodyInfo.IsTruncated
	}

	// If enhanced system is not available, log error
	caddy.Log().Warn("Enhanced body handler not available, cannot read request body")
	return "", r.Header.Get("Content-Type"), false
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
//
//	extractAPIKey("Bearer abc123") returns "abc123"
//	extractAPIKey("Basic xyz789") returns "xyz789"
//	extractAPIKey("plainkey") returns "plainkey"
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

// extractUsageFromResponse parses authoritative token counts from the backend response.
// Works for both non-streaming JSON (usage field at top level) and SSE streaming
// (usage field in the final chunk, requires stream_options.include_usage=true).
// Returns (promptTokens, completionTokens, cachedTokens, true) when found.
// cachedTokens comes from usage.prompt_tokens_details.cached_tokens (vLLM --enable-prompt-tokens-details).
func extractUsageFromResponse(body string) (promptTokens, completionTokens, cachedTokens int, ok bool) {
	parseUsageObj := func(raw string) (int, int, int, bool) {
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err != nil {
			return 0, 0, 0, false
		}
		usage, _ := obj["usage"].(map[string]any)
		if usage == nil {
			return 0, 0, 0, false
		}
		toInt := func(v any) int {
			if f, ok := v.(float64); ok {
				return int(f)
			}
			return 0
		}
		pt := toInt(usage["prompt_tokens"])
		ct := toInt(usage["completion_tokens"])
		if ct == 0 && pt == 0 {
			return 0, 0, 0, false
		}
		var cached int
		if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
			cached = toInt(details["cached_tokens"])
		}
		return pt, ct, cached, true
	}

	// Non-streaming: direct JSON
	if !strings.Contains(body, "\ndata:") && !strings.HasPrefix(body, "data:") {
		pt, ct, cached, found := parseUsageObj(strings.TrimSpace(body))
		return pt, ct, cached, found
	}

	// Streaming SSE: scan every chunk (usage is usually in the last non-[DONE] chunk)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[5:])
		if data == "[DONE]" {
			continue
		}
		if pt, ct, cached, found := parseUsageObj(data); found {
			return pt, ct, cached, true
		}
	}
	return 0, 0, 0, false
}

// extractResponseContent extracts the actual generated text from a response body.
// For SSE streaming: concatenates delta.content from all chunks.
// For non-streaming JSON: extracts choices[].message.content.
// Falls back to empty string if neither format is recognized.
func extractResponseContent(body string) string {
	var sb strings.Builder

	// SSE streaming
	if strings.Contains(body, "\ndata:") || strings.HasPrefix(body, "data:") {
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(line[5:])
			if data == "[DONE]" {
				continue
			}
			var obj map[string]any
			if err := json.Unmarshal([]byte(data), &obj); err != nil {
				continue
			}
			choices, _ := obj["choices"].([]any)
			for _, c := range choices {
				cm, _ := c.(map[string]any)
				delta, _ := cm["delta"].(map[string]any)
				if content, ok := delta["content"].(string); ok {
					sb.WriteString(content)
				}
			}
		}
		return sb.String()
	}

	// Non-streaming JSON
	var obj map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &obj); err != nil {
		return ""
	}
	choices, _ := obj["choices"].([]any)
	for _, c := range choices {
		cm, _ := c.(map[string]any)
		msg, _ := cm["message"].(map[string]any)
		if content, ok := msg["content"].(string); ok {
			sb.WriteString(content)
		}
	}
	return sb.String()
}

// extractRequestMessageContent extracts plain text from the messages array in a chat
// completion request. Only text is counted — not JSON structure, tool definitions, etc.
func extractRequestMessageContent(body string) string {
	var obj map[string]any
	if err := json.Unmarshal([]byte(body), &obj); err != nil {
		return ""
	}
	var sb strings.Builder
	messages, _ := obj["messages"].([]any)
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		switch content := msg["content"].(type) {
		case string:
			sb.WriteString(content)
			sb.WriteByte('\n')
		case []any:
			for _, block := range content {
				b, _ := block.(map[string]any)
				if b["type"] == "text" {
					if text, ok := b["text"].(string); ok {
						sb.WriteString(text)
						sb.WriteByte('\n')
					}
				}
			}
		}
	}
	// Include top-level system prompt if present
	if system, ok := obj["system"].(string); ok {
		sb.WriteString(system)
	}
	return strings.TrimSpace(sb.String())
}

// injectStreamUsageOption adds stream_options.include_usage=true to streaming requests.
// This ensures vLLM returns authoritative token counts in the final SSE chunk.
// Only modifies the body when stream=true and include_usage is not already set.
// Whether the body is treated as JSON is determined by sniffing the body itself
// (see looksLikeJSONObject), not by the client-controlled Content-Type header.
func injectStreamUsageOption(body string) string {
	if body == "" || !looksLikeJSONObject(body) {
		return body
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return body
	}
	isStream, _ := parsed["stream"].(bool)
	if !isStream {
		return body
	}
	opts, _ := parsed["stream_options"].(map[string]any)
	if opts == nil {
		opts = make(map[string]any)
	}
	if _, exists := opts["include_usage"]; exists {
		return body
	}
	opts["include_usage"] = true
	parsed["stream_options"] = opts
	result, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return string(result)
}

// detectModelFromResponseBody extracts the model name from a response body.
// Handles both streaming (SSE) and regular JSON responses.
func detectModelFromResponseBody(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		var raw string
		if strings.HasPrefix(line, "data:") {
			raw = strings.TrimSpace(line[5:])
			if raw == "[DONE]" {
				continue
			}
		} else if strings.HasPrefix(line, "{") {
			raw = line
		} else {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal([]byte(raw), &obj); err == nil {
			if m, ok := obj["model"].(string); ok && m != "" {
				return m
			}
		}
	}
	return "unknown"
}

// looksLikeJSONObject reports whether body, after trimming leading whitespace,
// starts with '{'. This is a cheap body-sniffing check used to decide whether
// a request should be treated as an LLM JSON call, since the client-controlled
// Content-Type header cannot be trusted for that decision.
func looksLikeJSONObject(body string) bool {
	trimmed := strings.TrimLeft(body, " \t\r\n")
	return strings.HasPrefix(trimmed, "{")
}

// freePassPaths lists the URL path suffixes that are allowed to proceed
// without a detectable "model" field even on POST. This is deliberately an
// allowlist of known-safe, non-token-consuming routes rather than an
// allowlist of known inference routes: an inference-route allowlist fails
// open for every LLM/embedding endpoint it doesn't yet know about (new
// backends, new API surfaces, trailing-slash variants, ...), which is exactly
// how the billing bypass this gate exists to close kept reappearing. A
// free-pass list fails closed by default — anything not explicitly named
// here is treated as billable and rejected when it has no model. Matched by
// suffix so endpoints mounted under a base path (e.g. "/proxy/api/show") are
// still covered.
var freePassPaths = []string{
	"/api/show", // Ollama model-metadata lookup — legitimate, non-token-consuming
}

// isFreePassPath reports whether path is on the free-pass list, i.e. safe to
// proxy through on POST even without a detectable model. The path is
// normalized (trailing slashes stripped) before matching. Used to fail
// closed: a POST to any other path with no detectable model must not be
// proxied for free (see BLOCK 4/serveX402 step 4).
func isFreePassPath(path string) bool {
	normalized := strings.TrimRight(path, "/")
	if normalized == "" {
		normalized = "/"
	}
	for _, suffix := range freePassPaths {
		if strings.HasSuffix(normalized, suffix) {
			return true
		}
	}
	return false
}

// detectModelFromRequestBody extracts the model name from a JSON request body.
// This function parses the request body as JSON and searches for a "model" field.
// Whether the body is treated as JSON is determined by sniffing the body itself
// (see looksLikeJSONObject), not by the client-controlled Content-Type header,
// so billing cannot be bypassed by mislabeling the request.
//
// Parameters:
//   - requestBody: The request body content
//
// Returns:
//   - string: The detected model name, or "unknown" if not found or not JSON
func detectModelFromRequestBody(requestBody string) string {
	// Only process bodies that look like a JSON object
	if !looksLikeJSONObject(requestBody) {
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

// injectRequestDefaults adds operator-defined default values to an LLM request body.
// It only sets fields that the client has not already provided, so client values
// always take precedence.  Returns the (possibly modified) body as a string.
// Whether the body is treated as JSON is determined by sniffing the body itself
// (see looksLikeJSONObject), not by the client-controlled Content-Type header.
//
// Supported defaults (controlled via Caddyfile directives):
//   - default_think      → sets top-level "think" field  (e.g. "low")
//   - default_num_predict → sets "options.num_predict"   (e.g. 512)
func injectRequestDefaults(body, defaultThink string, defaultNumPredict int) string {
	if body == "" {
		return body
	}
	if !looksLikeJSONObject(body) {
		return body
	}
	if defaultThink == "" && defaultNumPredict == 0 {
		return body
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return body // not valid JSON — leave untouched
	}

	modified := false

	// Inject "think" only when absent.
	// "off" injects boolean false (disables thinking); any other value is injected as-is.
	if defaultThink != "" {
		if _, exists := parsed["think"]; !exists {
			if defaultThink == "off" {
				parsed["think"] = false
			} else {
				parsed["think"] = defaultThink
			}
			modified = true
		}
	}

	// Inject "options.num_predict" only when absent
	if defaultNumPredict != 0 {
		opts, _ := parsed["options"].(map[string]interface{})
		if opts == nil {
			opts = make(map[string]interface{})
		}
		if _, exists := opts["num_predict"]; !exists {
			opts["num_predict"] = defaultNumPredict
			parsed["options"] = opts
			modified = true
		}
	}

	if !modified {
		return body
	}

	result, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return string(result)
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
//
//	the actual parsing to UnmarshalCaddyfile.
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
		logger.Debug("Validator cache cleaned up")
	}

	// Enhanced metering components handle their own cleanup

	// x402 cleanup — portal client has no background goroutines to stop

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
