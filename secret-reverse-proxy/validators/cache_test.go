package validators

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
	"time"

	mocktests "github.com/scrtlabs/secret-reverse-proxy/tests"
	utils "github.com/scrtlabs/secret-reverse-proxy/util"
)

var mockContract *mocktests.MockQueryContract

// setPermitEnvVars sets the required permit env vars for tests that call UpdateAPIKeyCache without a PermitFile
func setPermitEnvVars(t *testing.T) {
	t.Helper()
	t.Setenv("SECRETAI_PERMIT_TYPE", "tendermint/PubKeySecp256k1")
	t.Setenv("SECRETAI_PERMIT_PUBKEY", "TestPubKeyValue")
	t.Setenv("SECRETAI_PERMIT_SIG", "TestSigValue")
}

func TestAPIKeyValidator_UpdateAPICache_Success(t *testing.T) {
	setPermitEnvVars(t)
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Mock successful contract response
	testHashes := map[string]bool{
		"hash1": true,
		"hash2": true,
		"hash3": true,
	}
	mocktests.SetupMockContract(testHashes)
	
	// Replace the contract query function for testing
	originalQuery := queryContractFunc
	queryContractFunc = mocktests.MockQueryContractFunc
	defer func() { queryContractFunc = originalQuery }()
	
	err := validator.UpdateAPIKeyCache()
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
	
	// Verify cache contents
	if validator.CacheSize() != len(testHashes) {
		t.Errorf("Expected cache size %d, got %d", len(testHashes), len(validator.cache))
	}
		
	// Verify lastUpdate was set
	if validator.LastUpdate().IsZero() {
		t.Error("Expected lastUpdate to be set")
	}
}

func TestAPIKeyValidator_UpdateAPICache_ContractFailure(t *testing.T) {
	setPermitEnvVars(t)
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Mock contract failure
	mocktests.SetupFailingMockContract(fmt.Errorf("contract query failed"))
	
	// Replace the contract query function for testing
	originalQuery := queryContractFunc
	queryContractFunc = mocktests.MockQueryContractFunc
	defer func() { queryContractFunc = originalQuery }()
	
	err := validator.UpdateAPIKeyCache()
	if err == nil {
		t.Error("Expected error but got none")
	}
	
	if !strings.Contains(fmt.Sprintf("%v", err), "contract query failed") {
		t.Errorf("Expected error to contain 'contract query failed', got: %v", err)
	}
}

func TestAPIKeyValidator_UpdateAPICache_InvalidResponse(t *testing.T) {
	setPermitEnvVars(t)
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Mock contract with invalid response structure
	mockContract = &mocktests.MockQueryContract{
		ValidHashes: nil,
		ShouldFail:  false,
	}
	
	// Replace the contract query function to return invalid structure
	originalQuery := queryContractFunc
	queryContractFunc = func(contractAddress string, query map[string]any) (map[string]any, error) {
		return map[string]any{
			"invalid_field": "no api_keys field",
		}, nil
	}
	defer func() { queryContractFunc = originalQuery }()
	
	err := validator.UpdateAPIKeyCache()
	if err == nil {
		t.Error("Expected error but got none")
	}
	
	if !strings.Contains(fmt.Sprintf("%v", err), "unexpected response format") {
		t.Errorf("Expected error to contain 'unexpected response format', got: %v", err)
	}
}

func TestAPIKeyValidator_UpdateAPICache_WithPermitFile(t *testing.T) {
	// Create a permit file
	permitJSON := `{
		"params": {
			"permit_name": "custom_permit",
			"chain_id": "test-chain"
		},
		"signature": {
			"signature": "custom_signature"
		}
	}`
	
	tmpFile := utils.CreateTempFile(t, permitJSON)
	defer utils.CleanupTempFile(t, tmpFile)
	
	config := &Config{
		ContractAddress: "test-contract",
		PermitFile:      tmpFile,
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Mock successful contract response
	mocktests.SetupMockContract(map[string]bool{"hash1": true})
	
	// Replace the contract query function for testing
	originalQuery := queryContractFunc
	queryContractFunc = mocktests.MockQueryContractFunc
	defer func() { queryContractFunc = originalQuery }()
	
	err := validator.UpdateAPIKeyCache()
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
}

func TestAPIKeyValidator_UpdateAPICache_InvalidPermitFile(t *testing.T) {
	config := &Config{
		ContractAddress: "test-contract",
		PermitFile:      "/non/existent/permit.json",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	err := validator.UpdateAPIKeyCache()
	if err == nil {
		t.Error("Expected error but got none")
	}
	
	if !strings.Contains(fmt.Sprintf("%v", err), "failed to read permit file") {
		t.Errorf("Expected error to contain 'failed to read permit file', got: %v", err)
	}
}

func TestAPIKeyValidator_ConcurrentAccess(t *testing.T) {
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Add some data to cache
	validator.cache["key1"] = true
	validator.cache["key2"] = false
	validator.lastUpdate = time.Now()
	
	// Test concurrent reads
	done := make(chan bool, 10)
	
	for i := 0; i < 10; i++ {
		go func(id int) {
			defer func() { done <- true }()
			
			// Simulate read operations
			validator.cacheMutex.RLock()
			_ = validator.cache["key1"]
			_ = validator.cache["key2"]
			_ = len(validator.cache)
			validator.cacheMutex.RUnlock()
		}(i)
	}
	
	// Wait for all goroutines to complete
	for i := 0; i < 10; i++ {
		<-done
	}
}

func TestAPIKeyValidator_CacheExpiry(t *testing.T) {
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Millisecond * 50, // Very short TTL
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Add data to cache
	validator.cache["test-key"] = true
	validator.lastUpdate = time.Now()
	
	// Check that cache is fresh initially
	validator.cacheMutex.RLock()
	isStale := time.Since(validator.lastUpdate) > validator.config.CacheTTL
	validator.cacheMutex.RUnlock()
	
	if isStale {
		t.Error("Expected cache to be fresh initially")
	}
	
	// Wait for cache to expire
	time.Sleep(time.Millisecond * 100)
	
	// Check that cache is now stale
	validator.cacheMutex.RLock()
	isStale = time.Since(validator.lastUpdate) > validator.config.CacheTTL
	validator.cacheMutex.RUnlock()
	
	if !isStale {
		t.Error("Expected cache to be stale after TTL")
	}
}

func TestAPIKeyValidator_CacheKeyHashing(t *testing.T) {
	// Test that different keys produce different hashes
	key1 := "test-key-1"
	key2 := "test-key-2"
	
	hasher1 := sha256.New()
	hasher1.Write([]byte(key1))
	hash1 := hex.EncodeToString(hasher1.Sum(nil))
	
	hasher2 := sha256.New()
	hasher2.Write([]byte(key2))
	hash2 := hex.EncodeToString(hasher2.Sum(nil))
	
	if hash1 == hash2 {
		t.Error("Expected different keys to produce different hashes")
	}
	
	// Test that same key produces same hash
	hasher3 := sha256.New()
	hasher3.Write([]byte(key1))
	hash3 := hex.EncodeToString(hasher3.Sum(nil))
	
	if hash1 != hash3 {
		t.Error("Expected same key to produce same hash")
	}
}

func TestAPIKeyValidator_EmptyContractResponse(t *testing.T) {
	setPermitEnvVars(t)
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Mock contract response with empty api_keys
	originalQuery := queryContractFunc
	queryContractFunc = func(contractAddress string, query map[string]any) (map[string]any, error) {
		return map[string]any{
			"api_keys": []any{}, // Empty array
		}, nil
	}
	defer func() { queryContractFunc = originalQuery }()
	
	err := validator.UpdateAPIKeyCache()
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
	
	// Cache should be empty
	if len(validator.cache) != 0 {
		t.Errorf("Expected empty cache, got %d entries", len(validator.cache))
	}
}

func TestAPIKeyValidator_MalformedCacheEntries(t *testing.T) {
	setPermitEnvVars(t)
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Mock contract response with malformed entries
	originalQuery := queryContractFunc
	queryContractFunc = func(contractAddress string, query map[string]any) (map[string]any, error) {
		return map[string]any{
			"api_keys": []any{
				"invalid-entry-string",                    // Invalid: string instead of map
				map[string]any{"no_hash_field": "value"}, // Invalid: missing hashed_key
				map[string]any{"hashed_key": ""},         // Invalid: empty hash
				map[string]any{"hashed_key": 123},        // Invalid: non-string hash
				map[string]any{"hashed_key": "valid_hash"}, // Valid entry
			},
		}, nil
	}
	defer func() { queryContractFunc = originalQuery }()
	
	err := validator.UpdateAPIKeyCache()
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
	
	// Only the valid entry should be in cache
	if len(validator.cache) != 1 {
		t.Errorf("Expected 1 cache entry, got %d", len(validator.cache))
	}
	
	if !validator.cache["valid_hash"] {
		t.Error("Expected valid_hash to be in cache")
	}
}

// Global variable to hold the original query function
var queryContractFunc = func(contractAddress string, query map[string]any) (map[string]any, error) {
	// This should be replaced by the actual querycontract.QueryContract in production
	return nil, fmt.Errorf("mock function not initialized")
}