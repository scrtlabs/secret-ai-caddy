package secret_reverse_proxy

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// MockQueryContract replaces the real contract query functionality for testing
type MockQueryContract struct {
	ValidHashes map[string]bool
	ShouldFail  bool
	FailError   error
}

var mockContract *MockQueryContract

// Mock the querycontract.QueryContract function
func mockQueryContractFunc(contractAddress string, query map[string]any) (map[string]any, error) {
	if mockContract.ShouldFail {
		return nil, mockContract.FailError
	}

	// Return mock API keys based on what we want to test
	apiKeys := make([]any, 0)
	for hash := range mockContract.ValidHashes {
		apiKeys = append(apiKeys, map[string]any{
			"hashed_key": hash,
		})
	}

	return map[string]any{
		"api_keys": apiKeys,
	}, nil
}

// Test helper to create a temporary file with content
func createTempFile(t *testing.T, content string) string {
	tmpFile, err := os.CreateTemp("", "test_keys_*.txt")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}
	
	return tmpFile.Name()
}

// Test helper to clean up temp files
func cleanupTempFile(t *testing.T, filename string) {
	if err := os.Remove(filename); err != nil {
		t.Logf("Warning: Failed to remove temp file %s: %v", filename, err)
	}
}

func TestDefaultConfig(t *testing.T) {
	config := defaultConfig()
	
	if config.MasterKeysFile != "master_keys.txt" {
		t.Errorf("Expected MasterKeysFile to be 'master_keys.txt', got %s", config.MasterKeysFile)
	}
	
	if config.ContractAddress != "secret1ttm9axv8hqwjv3qxvxseecppsrw4cd68getrvr" {
		t.Errorf("Expected default contract address, got %s", config.ContractAddress)
	}
	
	if config.CacheTTL != 30*time.Minute {
		t.Errorf("Expected CacheTTL to be 30 minutes, got %v", config.CacheTTL)
	}
}

func TestNewAPIKeyValidator(t *testing.T) {
	config := &Config{
		APIKey:          "test-key",
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}
	
	validator := NewAPIKeyValidator(config)
	
	if validator.config != config {
		t.Error("Expected validator config to match input config")
	}
	
	if validator.cache == nil {
		t.Error("Expected validator cache to be initialized")
	}
	
	if len(validator.cache) != 0 {
		t.Error("Expected validator cache to be empty initially")
	}
}

func TestMiddleware_CaddyModule(t *testing.T) {
	m := Middleware{}
	moduleInfo := m.CaddyModule()
	
	expectedID := caddy.ModuleID("http.handlers.secret_reverse_proxy")
	if moduleInfo.ID != expectedID {
		t.Errorf("Expected module ID %s, got %s", expectedID, moduleInfo.ID)
	}
	
	if moduleInfo.New == nil {
		t.Error("Expected New function to be defined")
	}
	
	// Test that New() returns a new Middleware instance
	newModule := moduleInfo.New()
	if _, ok := newModule.(*Middleware); !ok {
		t.Error("Expected New() to return a *Middleware")
	}
}

func TestMiddleware_Provision(t *testing.T) {
	tests := []struct {
		name           string
		initialConfig  *Config
		expectError    bool
		expectDefaults bool
	}{
		{
			name:           "nil config uses defaults",
			initialConfig:  nil,
			expectError:    false,
			expectDefaults: true,
		},
		{
			name: "existing config preserved",
			initialConfig: &Config{
				APIKey:          "custom-key",
				ContractAddress: "custom-contract",
				CacheTTL:        time.Hour * 2,
			},
			expectError:    false,
			expectDefaults: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Middleware{
				Config: tt.initialConfig,
			}
			
			ctx := caddy.Context{}
			err := m.Provision(ctx)
			
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			
			if tt.expectDefaults {
				if m.Config.ContractAddress != "secret1ttm9axv8hqwjv3qxvxseecppsrw4cd68getrvr" {
					t.Error("Expected default config to be applied")
				}
			}
			
			if m.validator == nil {
				t.Error("Expected validator to be created")
			}
		})
	}
}

func TestMiddleware_Validate(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectError bool
		errorMsg    string
	}{
		{
			name:        "nil config",
			config:      nil,
			expectError: true,
			errorMsg:    "configuration is nil",
		},
		{
			name: "missing contract address",
			config: &Config{
				CacheTTL: time.Hour,
			},
			expectError: true,
			errorMsg:    "contract address is required",
		},
		{
			name: "zero cache TTL",
			config: &Config{
				ContractAddress: "test-contract",
				CacheTTL:        0,
			},
			expectError: true,
			errorMsg:    "cache TTL must be positive",
		},
		{
			name: "negative cache TTL",
			config: &Config{
				ContractAddress: "test-contract",
				CacheTTL:        -time.Hour,
			},
			expectError: true,
			errorMsg:    "cache TTL must be positive",
		},
		{
			name: "valid config",
			config: &Config{
				ContractAddress: "test-contract",
				CacheTTL:        time.Hour,
			},
			expectError: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Middleware{
				Config: tt.config,
			}
			
			err := m.Validate()
			
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error message to contain '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

func TestExtractAPIKey(t *testing.T) {
	tests := []struct {
		name       string
		authHeader string
		expected   string
	}{
		{
			name:       "Basic auth",
			authHeader: "Basic abc123",
			expected:   "abc123",
		},
		{
			name:       "Bearer auth",
			authHeader: "Bearer xyz789",
			expected:   "xyz789",
		},
		{
			name:       "No prefix",
			authHeader: "plainkey",
			expected:   "plainkey",
		},
		{
			name:       "Empty string",
			authHeader: "",
			expected:   "",
		},
		{
			name:       "Basic with extra spaces",
			authHeader: "Basic  key-with-spaces",
			expected:   " key-with-spaces",
		},
		{
			name:       "Case sensitive",
			authHeader: "basic abc123",
			expected:   "basic abc123",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractAPIKey(tt.authHeader)
			if result != tt.expected {
				t.Errorf("Expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestMin(t *testing.T) {
	tests := []struct {
		name     string
		a, b     int
		expected int
	}{
		{"a smaller", 5, 10, 5},
		{"b smaller", 10, 5, 5},
		{"equal", 7, 7, 7},
		{"negative numbers", -5, -3, -5},
		{"mixed signs", -2, 3, -2},
		{"zero", 0, 5, 0},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := min(tt.a, tt.b)
			if result != tt.expected {
				t.Errorf("min(%d, %d) = %d, expected %d", tt.a, tt.b, result, tt.expected)
			}
		})
	}
}

func TestAPIKeyValidator_checkMasterKeys(t *testing.T) {
	tests := []struct {
		name         string
		fileContent  string
		apiKey       string
		expectFound  bool
		expectError  bool
		noFile       bool
	}{
		{
			name:        "key found in file",
			fileContent: "key1\nkey2\nkey3\n",
			apiKey:      "key2",
			expectFound: true,
		},
		{
			name:        "key not found",
			fileContent: "key1\nkey2\nkey3\n",
			apiKey:      "key4",
			expectFound: false,
		},
		{
			name:        "empty file",
			fileContent: "",
			apiKey:      "key1",
			expectFound: false,
		},
		{
			name:        "file with empty lines",
			fileContent: "key1\n\nkey3\n  \n",
			apiKey:      "key3",
			expectFound: true,
		},
		{
			name:        "file with whitespace",
			fileContent: "  key1  \n key2 \n",
			apiKey:      "key1",
			expectFound: true,
		},
		{
			name:        "no file configured",
			noFile:      true,
			apiKey:      "any-key",
			expectFound: false,
		},
		{
			name:        "file does not exist",
			fileContent: "", // Will be deleted to simulate non-existence
			apiKey:      "any-key",
			expectFound: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{}
			
			if !tt.noFile {
				tmpFile := createTempFile(t, tt.fileContent)
				defer cleanupTempFile(t, tmpFile)
				
				if tt.name == "file does not exist" {
					// Delete the file to simulate non-existence
					os.Remove(tmpFile)
				}
				
				config.MasterKeysFile = tmpFile
			}
			
			validator := NewAPIKeyValidator(config)
			
			found, err := validator.checkMasterKeys(tt.apiKey)
			
			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
			
			if found != tt.expectFound {
				t.Errorf("Expected found=%v, got found=%v", tt.expectFound, found)
			}
		})
	}
}

func TestUnmarshalCaddyfile(t *testing.T) {
	tests := []struct {
		name        string
		caddyfile   string
		expectError bool
		validate    func(*testing.T, *Middleware)
	}{
		{
			name: "API master key",
			caddyfile: `secret_reverse_proxy {
				API_MASTER_KEY "test-key-123"
			}`,
			validate: func(t *testing.T, m *Middleware) {
				if m.Config.APIKey != "test-key-123" {
					t.Errorf("Expected API key 'test-key-123', got %s", m.Config.APIKey)
				}
			},
		},
		{
			name: "master keys file",
			caddyfile: `secret_reverse_proxy {
				master_keys_file "/path/to/keys.txt"
			}`,
			validate: func(t *testing.T, m *Middleware) {
				if m.Config.MasterKeysFile != "/path/to/keys.txt" {
					t.Errorf("Expected master keys file '/path/to/keys.txt', got %s", m.Config.MasterKeysFile)
				}
			},
		},
		{
			name: "permit file",
			caddyfile: `secret_reverse_proxy {
				permit_file "/path/to/permit.json"
			}`,
			validate: func(t *testing.T, m *Middleware) {
				if m.Config.PermitFile != "/path/to/permit.json" {
					t.Errorf("Expected permit file '/path/to/permit.json', got %s", m.Config.PermitFile)
				}
			},
		},
		{
			name: "contract address",
			caddyfile: `secret_reverse_proxy {
				contract_address "secret1abc123def456"
			}`,
			validate: func(t *testing.T, m *Middleware) {
				if m.Config.ContractAddress != "secret1abc123def456" {
					t.Errorf("Expected contract address 'secret1abc123def456', got %s", m.Config.ContractAddress)
				}
			},
		},
		{
			name: "multiple directives",
			caddyfile: `secret_reverse_proxy {
				API_MASTER_KEY "master-key"
				master_keys_file "/keys.txt"
				permit_file "/permit.json"
				contract_address "secret1contract"
			}`,
			validate: func(t *testing.T, m *Middleware) {
				if m.Config.APIKey != "master-key" {
					t.Errorf("Expected API key 'master-key', got %s", m.Config.APIKey)
				}
				if m.Config.MasterKeysFile != "/keys.txt" {
					t.Errorf("Expected master keys file '/keys.txt', got %s", m.Config.MasterKeysFile)
				}
				if m.Config.PermitFile != "/permit.json" {
					t.Errorf("Expected permit file '/permit.json', got %s", m.Config.PermitFile)
				}
				if m.Config.ContractAddress != "secret1contract" {
					t.Errorf("Expected contract address 'secret1contract', got %s", m.Config.ContractAddress)
				}
			},
		},
		{
			name: "unknown directive",
			caddyfile: `secret_reverse_proxy {
				unknown_directive "value"
			}`,
			expectError: true,
		},
		{
			name: "missing argument",
			caddyfile: `secret_reverse_proxy {
				API_MASTER_KEY
			}`,
			expectError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := caddyfile.NewTestDispenser(tt.caddyfile)
			
			m := &Middleware{}
			err := m.UnmarshalCaddyfile(d)
			
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
			
			if tt.validate != nil {
				tt.validate(t, m)
			}
		})
	}
}