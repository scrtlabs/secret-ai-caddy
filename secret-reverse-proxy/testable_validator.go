package secret_reverse_proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ContractQuerier interface for dependency injection in tests
type ContractQuerier interface {
	QueryContract(contractAddress string, query map[string]any) (map[string]any, error)
}

// TestableAPIKeyValidator is a version of APIKeyValidator that accepts a ContractQuerier
type TestableAPIKeyValidator struct {
	config         *Config
	cache          map[string]bool
	cacheMutex     sync.RWMutex
	lastUpdate     time.Time
	contractQuery  ContractQuerier
}

// NewTestableAPIKeyValidator creates a new testable validator with dependency injection
func NewTestableAPIKeyValidator(config *Config, querier ContractQuerier) *TestableAPIKeyValidator {
	return &TestableAPIKeyValidator{
		config:        config,
		cache:         make(map[string]bool),
		contractQuery: querier,
	}
}

// ValidateAPIKey performs API key validation using the injected contract querier
func (v *TestableAPIKeyValidator) ValidateAPIKey(apiKey string) (bool, error) {
	// Validate input
	if strings.TrimSpace(apiKey) == "" {
		return false, fmt.Errorf("empty API key")
	}
	
	// Check against configured master key
	if v.config.APIKey != "" && apiKey == v.config.APIKey {
		return true, nil
	}

	// Check against the master keys file
	isMasterKey, err := v.checkMasterKeys(apiKey)
	if err != nil {
		return false, fmt.Errorf("master key check failed: %w", err)
	}
	if isMasterKey {
		return true, nil
	}

	// Hash the API key for cache lookup
	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	apiKeyHash := hex.EncodeToString(hasher.Sum(nil))

	// Check cache
	v.cacheMutex.RLock()
	cached, found := v.cache[apiKeyHash]
	isStale := time.Since(v.lastUpdate) > v.config.CacheTTL
	v.cacheMutex.RUnlock()

	if found && !isStale {
		return cached, nil
	}

	// Update cache from contract
	if err := v.updateAPIKeyCache(); err != nil {
		return false, fmt.Errorf("cache update failed: %w", err)
	}

	// Check cache again after update
	v.cacheMutex.RLock()
	defer v.cacheMutex.RUnlock()
	return v.cache[apiKeyHash], nil
}

// checkMasterKeys checks if the provided API key exists in the master keys file
func (v *TestableAPIKeyValidator) checkMasterKeys(apiKey string) (bool, error) {
	// Use the same logic as the original validator
	validator := &APIKeyValidator{config: v.config}
	return validator.checkMasterKeys(apiKey)
}

// updateAPIKeyCache queries the contract and updates the in-memory cache
func (v *TestableAPIKeyValidator) updateAPIKeyCache() error {
	// Load permit from file or use default
	var permit map[string]any
	var err error
	
	if v.config.PermitFile != "" {
		permit, err = readPermitFromFile(v.config.PermitFile)
		if err != nil {
			return fmt.Errorf("failed to read permit file: %w", err)
		}
	} else {
		// Use default permit if no file specified
		permit = getDefaultPermit()
	}

	query := map[string]any{
		"api_keys_with_permit": map[string]any{
			"permit": permit,
		},
	}

	result, err := v.contractQuery.QueryContract(v.config.ContractAddress, query)
	if err != nil {
		return fmt.Errorf("contract query failed: %w", err)
	}

	apiKeys, ok := result["api_keys"].([]any)
	if !ok {
		return fmt.Errorf("unexpected response format: api_keys not found")
	}

	newCache := make(map[string]bool)
	for _, entry := range apiKeys {
		entryMap, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		hashedKey, ok := entryMap["hashed_key"].(string)
		if ok && hashedKey != "" {
			newCache[hashedKey] = true
		}
	}

	v.cacheMutex.Lock()
	defer v.cacheMutex.Unlock()
	v.cache = newCache
	v.lastUpdate = time.Now()

	return nil
}