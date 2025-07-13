package secret_reverse_proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestAPIKeyValidator_ValidateAPIKey_MasterKey(t *testing.T) {
	config := &Config{
		APIKey:          "master-key-123",
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	tests := []struct {
		name        string
		apiKey      string
		expectValid bool
		expectError bool
	}{
		{
			name:        "valid master key",
			apiKey:      "master-key-123",
			expectValid: true,
			expectError: false,
		},
		{
			name:        "invalid master key",
			apiKey:      "wrong-key",
			expectValid: false,
			expectError: false,
		},
		{
			name:        "empty key",
			apiKey:      "",
			expectValid: false,
			expectError: true,
		},
		{
			name:        "whitespace only key",
			apiKey:      "   ",
			expectValid: false,
			expectError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, err := validator.ValidateAPIKey(tt.apiKey)
			
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}
			
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}
			
			if valid != tt.expectValid {
				t.Errorf("Expected valid=%v, got valid=%v", tt.expectValid, valid)
			}
		})
	}
}

func TestAPIKeyValidator_ValidateAPIKey_MasterKeysFile(t *testing.T) {
	// Create temp file with master keys
	fileContent := "file-key-1\nfile-key-2\nfile-key-3\n"
	tmpFile := createTempFile(t, fileContent)
	defer cleanupTempFile(t, tmpFile)
	
	config := &Config{
		MasterKeysFile:  tmpFile,
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	tests := []struct {
		name        string
		apiKey      string
		expectValid bool
	}{
		{
			name:        "valid file key",
			apiKey:      "file-key-2",
			expectValid: true,
		},
		{
			name:        "invalid file key",
			apiKey:      "not-in-file",
			expectValid: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			valid, err := validator.ValidateAPIKey(tt.apiKey)
			
			if err != nil {
				t.Errorf("Expected no error but got: %v", err)
				return
			}
			
			if valid != tt.expectValid {
				t.Errorf("Expected valid=%v, got valid=%v", tt.expectValid, valid)
			}
		})
	}
}

func TestAPIKeyValidator_ValidateAPIKey_Cache(t *testing.T) {
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Manually populate cache for testing
	testKey := "cache-test-key"
	hasher := sha256.New()
	hasher.Write([]byte(testKey))
	keyHash := hex.EncodeToString(hasher.Sum(nil))
	
	validator.cache[keyHash] = true
	validator.lastUpdate = time.Now()
	
	// Test cache hit
	valid, err := validator.ValidateAPIKey(testKey)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
	if !valid {
		t.Error("Expected valid=true from cache")
	}
	
	// Test cache miss (key not in cache)
	missingKey := "not-in-cache"
	
	// Set up mock contract to simulate contract query
	setupMockContract(map[string]bool{})
	
	valid, err = validator.ValidateAPIKey(missingKey)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
	if valid {
		t.Error("Expected valid=false for key not in contract")
	}
}

func TestAPIKeyValidator_ValidateAPIKey_StaleCache(t *testing.T) {
	config := &Config{
		ContractAddress: "test-contract",
		CacheTTL:        time.Millisecond * 10, // Very short TTL
	}
	
	validator := NewAPIKeyValidator(config)
	
	// Populate cache with old timestamp
	testKey := "stale-test-key"
	hasher := sha256.New()
	hasher.Write([]byte(testKey))
	keyHash := hex.EncodeToString(hasher.Sum(nil))
	
	validator.cache[keyHash] = true
	validator.lastUpdate = time.Now().Add(-time.Hour) // Old timestamp
	
	// Set up mock contract
	setupMockContract(map[string]bool{
		keyHash: true,
	})
	
	// Sleep to ensure cache is stale
	time.Sleep(time.Millisecond * 20)
	
	valid, err := validator.ValidateAPIKey(testKey)
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}
	if !valid {
		t.Error("Expected valid=true after cache refresh")
	}
}

func TestGetDefaultPermit(t *testing.T) {
	permit := getDefaultPermit()
	
	// Check structure
	params, ok := permit["params"].(map[string]any)
	if !ok {
		t.Fatal("Expected params to be map[string]any")
	}
	
	signature, ok := permit["signature"].(map[string]any)
	if !ok {
		t.Fatal("Expected signature to be map[string]any")
	}
	
	// Check required fields
	if params["permit_name"] != "api_keys_permit" {
		t.Error("Expected permit_name to be 'api_keys_permit'")
	}
	
	if params["chain_id"] != "pulsar-3" {
		t.Error("Expected chain_id to be 'pulsar-3'")
	}
	
	pubKey, ok := signature["pub_key"].(map[string]any)
	if !ok {
		t.Fatal("Expected pub_key to be map[string]any")
	}
	
	if pubKey["type"] != "tendermint/PubKeySecp256k1" {
		t.Error("Expected pub_key type to be 'tendermint/PubKeySecp256k1'")
	}
	
	if signature["signature"] == "" {
		t.Error("Expected signature to be non-empty")
	}
}

func TestReadPermitFromFile(t *testing.T) {
	// Create a valid JSON permit file
	permitJSON := `{
		"params": {
			"permit_name": "test_permit",
			"chain_id": "test-chain"
		},
		"signature": {
			"pub_key": {
				"type": "test_type",
				"value": "test_value"
			},
			"signature": "test_signature"
		}
	}`
	
	tmpFile := createTempFile(t, permitJSON)
	defer cleanupTempFile(t, tmpFile)
	
	permit, err := readPermitFromFile(tmpFile)
	if err != nil {
		t.Fatalf("Expected no error but got: %v", err)
	}
	
	params, ok := permit["params"].(map[string]any)
	if !ok {
		t.Fatal("Expected params to be map[string]any")
	}
	
	if params["permit_name"] != "test_permit" {
		t.Error("Expected permit_name to be 'test_permit'")
	}
	
	// Test invalid JSON
	invalidJSON := `{"invalid": json}`
	invalidFile := createTempFile(t, invalidJSON)
	defer cleanupTempFile(t, invalidFile)
	
	_, err = readPermitFromFile(invalidFile)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
	
	// Test non-existent file
	_, err = readPermitFromFile("/non/existent/file.json")
	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestGetResponseKeys(t *testing.T) {
	response := map[string]any{
		"api_keys": []any{},
		"status":   "success",
		"count":    42,
	}
	
	keys := getResponseKeys(response)
	
	expectedKeys := []string{"api_keys", "status", "count"}
	if len(keys) != len(expectedKeys) {
		t.Errorf("Expected %d keys, got %d", len(expectedKeys), len(keys))
	}
	
	// Check that all expected keys are present
	keyMap := make(map[string]bool)
	for _, key := range keys {
		keyMap[key] = true
	}
	
	for _, expectedKey := range expectedKeys {
		if !keyMap[expectedKey] {
			t.Errorf("Expected key '%s' not found in result", expectedKey)
		}
	}
}

// Helper function to set up mock contract for testing
func setupMockContract(validHashes map[string]bool) {
	mockContract = &MockQueryContract{
		ValidHashes: validHashes,
		ShouldFail:  false,
	}
}

// Helper function to set up failing mock contract
func setupFailingMockContract(err error) {
	mockContract = &MockQueryContract{
		ShouldFail: true,
		FailError:  err,
	}
}