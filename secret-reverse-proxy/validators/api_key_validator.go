package validators

import (
	"bufio"
	querycontract "github.com/scrtlabs/secret-reverse-proxy/query-contract"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"encoding/json"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
	
	proxyconfig "github.com/scrtlabs/secret-reverse-proxy/config"
)

type Config = proxyconfig.Config

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

func (v *APIKeyValidator) LastUpdate() time.Time {
	return v.lastUpdate
}

func (v *APIKeyValidator) CacheSize() int {
	v.cacheMutex.RLock()
	defer v.cacheMutex.RUnlock()
	return len(v.cache)
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
	isMasterKey, err := v.CheckMasterKeys(apiKey)
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
	if err := v.UpdateAPIKeyCache(); err != nil {
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

// Clear the cache to free memory
func (v *APIKeyValidator) CleanupCache() {
	v.cacheMutex.Lock()
	v.cache = make(map[string]bool)
	v.cacheMutex.Unlock()
}

// CheckMasterKeys checks if the provided API key exists in the master keys file
// or in the SECRETAI_MASTER_KEYS environment variable (comma-separated list).
// The file is checked first; if no file is configured, the env var is checked.
// IsMasterKey checks only the master key (tier 1) and master keys file (tier 2).
// Unlike ValidateAPIKey, this skips the cache and contract query, making it fast.
func (v *APIKeyValidator) IsMasterKey(apiKey string) bool {
	if strings.TrimSpace(apiKey) == "" {
		return false
	}
	if v.config.APIKey != "" && apiKey == v.config.APIKey {
		return true
	}
	isMaster, err := v.CheckMasterKeys(apiKey)
	if err != nil {
		return false
	}
	return isMaster
}

func (v *APIKeyValidator) CheckMasterKeys(apiKey string) (bool, error) {
	logger := caddy.Log()

	// Check master keys file if configured
	if v.config.MasterKeysFile != "" {
		logger.Debug("Checking master keys file", zap.String("file", v.config.MasterKeysFile))

		file, err := os.Open(v.config.MasterKeysFile)
		if err != nil {
			// If file doesn't exist, that's not an error — fall through to env var check
			if !os.IsNotExist(err) {
				logger.Error("Failed to open master keys file",
					zap.String("file", v.config.MasterKeysFile),
					zap.Error(err))
				return false, fmt.Errorf("failed to open master keys file: %w", err)
			}
			logger.Debug("Master keys file does not exist", zap.String("file", v.config.MasterKeysFile))
		} else {
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
		}
	}

	// Check SECRETAI_MASTER_KEYS env var (comma-separated list)
	envKeys := os.Getenv("SECRETAI_MASTER_KEYS")
	if envKeys != "" {
		logger.Debug("Checking SECRETAI_MASTER_KEYS env var")
		for _, key := range strings.Split(envKeys, ",") {
			key = strings.TrimSpace(key)
			if key != "" && key == apiKey {
				logger.Debug("API key found in SECRETAI_MASTER_KEYS env var")
				return true, nil
			}
		}
		logger.Debug("API key not found in SECRETAI_MASTER_KEYS env var")
	}

	if v.config.MasterKeysFile == "" && envKeys == "" {
		logger.Debug("No master keys file configured and SECRETAI_MASTER_KEYS env var not set")
	}

	return false, nil
}

// UpdateAPIKeyCache queries the contract and updates the in-memory cache
func (v *APIKeyValidator) UpdateAPIKeyCache() error {
	logger := caddy.Log()
	start := time.Now()
	
	logger.Debug("Starting cache update", zap.String("contract_address", v.config.ContractAddress))
	
	// Load permit from file or use default
	var permit map[string]any
	var err error
	
	if v.config.PermitFile != "" {
		logger.Debug("Loading permit from file", zap.String("file", v.config.PermitFile))
		permit, err = ReadPermitFromFile(v.config.PermitFile)
		if err != nil {
			logger.Error("Failed to read permit file",
				zap.String("file", v.config.PermitFile),
				zap.Error(err))
			return fmt.Errorf("failed to read permit file: %w", err)
		}
	} else {
		logger.Debug("Using default permit configuration")
		// Use default permit if no file specified
		permit, err = GetDefaultPermit(v.config)
		if err != nil {
			logger.Error("Failed to get default permit", zap.Error(err))
			return fmt.Errorf("failed to get default permit: %w", err)
		}
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
// or when UpdateAPIKeyCache() is called.
func NewAPIKeyValidator(config *Config) *APIKeyValidator {
	return &APIKeyValidator{
		// Store reference to configuration
		config: config,
		
		// Initialize empty cache - will be populated from contract on first use
		cache: make(map[string]bool),
		
		// lastUpdate will be set to zero time initially, forcing immediate cache update
	}
}



// GetDefaultPermit returns the default permit configuration using environment variables.
// Required env vars: SECRETAI_PERMIT_TYPE, SECRETAI_PERMIT_PUBKEY, SECRETAI_PERMIT_SIG.
// Returns an error if any of the required env vars are not set.
func GetDefaultPermit(config *Config) (map[string]any, error) {
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
		return nil, fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	return map[string]any{
		"params": map[string]any{
			"permit_name":    "api_keys_permit",
			"allowed_tokens": []any{config.ContractAddress},
			"chain_id":       config.SecretChainID,
			"permissions":    []any{},
		},
		"signature": map[string]any{
			"pub_key": map[string]any{
				"type":  permitType,
				"value": permitPubKey,
			},
			"signature": permitSig,
		},
	}, nil
}

// readPermitFromFile reads the permit from a JSON file
func ReadPermitFromFile(filePath string) (map[string]any, error) {
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

// getResponseKeys extracts the keys from a response map for debugging
func getResponseKeys(response map[string]any) []string {
	keys := make([]string, 0, len(response))
	for k := range response {
		keys = append(keys, k)
	}
	return keys
}