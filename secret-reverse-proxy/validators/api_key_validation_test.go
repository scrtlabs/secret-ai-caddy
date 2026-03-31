package validators

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
	utils "github.com/scrtlabs/secret-reverse-proxy/util"
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
			expectError: true, // Falls through to contract query which fails without permit config
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
	tmpFile := utils.CreateTempFile(t, fileContent)
	defer utils.CleanupTempFile(t, tmpFile)
	
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
		expectError bool
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
			expectError: true, // Falls through to contract query which fails without permit config
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

func TestAPIKeyValidator_ValidateAPIKey_Cache(t *testing.T) {
	t.Setenv("SECRETAI_PERMIT_TYPE", "tendermint/PubKeySecp256k1")
	t.Setenv("SECRETAI_PERMIT_PUBKEY", "TestPubKeyValue")
	t.Setenv("SECRETAI_PERMIT_SIG", "TestSigValue")

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
	
	// Test cache miss (key not in cache) — triggers contract query which fails in test env
	missingKey := "not-in-cache"

	valid, err = validator.ValidateAPIKey(missingKey)
	if err == nil {
		t.Error("Expected error from contract query in test environment")
	}
	if valid {
		t.Error("Expected valid=false for key not in contract")
	}
}

func TestAPIKeyValidator_ValidateAPIKey_StaleCache(t *testing.T) {
	t.Setenv("SECRETAI_PERMIT_TYPE", "tendermint/PubKeySecp256k1")
	t.Setenv("SECRETAI_PERMIT_PUBKEY", "TestPubKeyValue")
	t.Setenv("SECRETAI_PERMIT_SIG", "TestSigValue")

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

	// Sleep to ensure cache is stale
	time.Sleep(time.Millisecond * 20)

	// Stale cache triggers contract query which fails in test env
	valid, err := validator.ValidateAPIKey(testKey)
	if err == nil {
		t.Error("Expected error from contract query in test environment")
	}
	if valid {
		t.Error("Expected valid=false when contract query fails")
	}
}

func TestGetDefaultPermit(t *testing.T) {
	// Set required env vars
	t.Setenv("SECRETAI_PERMIT_TYPE", "tendermint/PubKeySecp256k1")
	t.Setenv("SECRETAI_PERMIT_PUBKEY", "TestPubKeyValue")
	t.Setenv("SECRETAI_PERMIT_SIG", "TestSigValue")

	config := &Config{
		ContractAddress: "secret1abc123",
		SecretChainID:   "secret-4",
	}
	permit, err := GetDefaultPermit(config)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

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

	if params["chain_id"] != "secret-4" {
		t.Error("Expected chain_id to be 'secret-4'")
	}

	pubKey, ok := signature["pub_key"].(map[string]any)
	if !ok {
		t.Fatal("Expected pub_key to be map[string]any")
	}

	if pubKey["type"] != "tendermint/PubKeySecp256k1" {
		t.Error("Expected pub_key type to be 'tendermint/PubKeySecp256k1'")
	}

	if pubKey["value"] != "TestPubKeyValue" {
		t.Error("Expected pub_key value to be 'TestPubKeyValue'")
	}

	if signature["signature"] != "TestSigValue" {
		t.Error("Expected signature to be 'TestSigValue'")
	}
}

func TestGetDefaultPermitMissingEnvVars(t *testing.T) {
	t.Setenv("SECRETAI_PERMIT_TYPE", "")
	t.Setenv("SECRETAI_PERMIT_PUBKEY", "")
	t.Setenv("SECRETAI_PERMIT_SIG", "")

	config := &Config{
		ContractAddress: "secret1abc123",
		SecretChainID:   "secret-4",
	}
	permit, err := GetDefaultPermit(config)
	if err == nil {
		t.Fatal("Expected error when env vars are missing")
	}
	if permit != nil {
		t.Fatal("Expected nil permit when env vars are missing")
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
	
	tmpFile := utils.CreateTempFile(t, permitJSON)
	defer utils.CleanupTempFile(t, tmpFile)
	
	permit, err := ReadPermitFromFile(tmpFile)
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
	invalidFile := utils.CreateTempFile(t, invalidJSON)
	defer utils.CleanupTempFile(t, invalidFile)
	
	_, err = ReadPermitFromFile(invalidFile)
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
	
	// Test non-existent file
	_, err = ReadPermitFromFile("/non/existent/file.json")
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

