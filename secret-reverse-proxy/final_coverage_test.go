package secret_reverse_proxy

import (
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
)

// TestUnmarshalCaddyfile_AdditionalCoverage tests edge cases in UnmarshalCaddyfile
func TestUnmarshalCaddyfile_AdditionalCoverage(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "cache_ttl with invalid duration",
			input:       "secret_reverse_proxy\ncache_ttl invalid-duration",
			expectError: true,
			errorMsg:    "invalid cache TTL",
		},
		{
			name:        "cache_ttl with valid duration",
			input:       "secret_reverse_proxy\ncache_ttl 45m",
			expectError: false,
		},
		{
			name:        "cache_ttl missing value",
			input:       "secret_reverse_proxy\ncache_ttl",
			expectError: true,
			errorMsg:    "cache_ttl requires exactly one argument",
		},
		{
			name:        "cache_ttl with multiple arguments",
			input:       "secret_reverse_proxy\ncache_ttl 30m 45m",
			expectError: true,
			errorMsg:    "cache_ttl requires exactly one argument",
		},
		{
			name:        "all directives together",
			input:       "secret_reverse_proxy\napi_key test-key\nmaster_keys_file keys.txt\npermit_file permit.json\ncontract_address test-contract\ncache_ttl 1h",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := caddyfile.NewTestDispenser(tt.input)
			middleware := &Middleware{}

			err := middleware.UnmarshalCaddyfile(d)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

// TestValidate_EdgeCases tests additional validation edge cases
func TestValidate_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		middleware  *Middleware
		expectError bool
		errorMsg    string
	}{
		{
			name: "negative cache TTL",
			middleware: &Middleware{
				Config: &Config{
					ContractAddress: "test-contract", 
					CacheTTL:        -1 * time.Hour,
				},
			},
			expectError: true,
			errorMsg:    "cache TTL must be positive",
		},
		{
			name: "very long contract address",
			middleware: &Middleware{
				Config: &Config{
					ContractAddress: strings.Repeat("a", 1000),
					CacheTTL:        time.Hour,
				},
			},
			expectError: false, // Should be valid
		},
		{
			name: "empty contract address after trim",
			middleware: &Middleware{
				Config: &Config{
					ContractAddress: "   \n\t   ",
					CacheTTL:        time.Hour,
				},
			},
			expectError: true,
			errorMsg:    "contract address is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.middleware.Validate()

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				} else if !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error to contain '%s', got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
			}
		})
	}
}

// TestServeHTTP_AdditionalCoverage tests more ServeHTTP edge cases
func TestServeHTTP_AdditionalCoverage(t *testing.T) {
	// Create a temporary keys file
	tmpFile := createTempFileHelper(t, "file-key-1\nfile-key-2\n")
	defer cleanupTempFileHelper(t, tmpFile)

	middleware := &Middleware{
		Config: &Config{
			APIKey:          "master-key",
			MasterKeysFile:  tmpFile,
			ContractAddress: "test-contract",
			CacheTTL:        time.Hour,
		},
	}

	// Provision the middleware
	ctx := caddy.Context{}
	err := middleware.Provision(ctx)
	if err != nil {
		t.Fatalf("Provision failed: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		authHeader     string
		expectedStatus int
		expectNextCall bool
	}{
		{
			name:           "POST with master key",
			method:         "POST",
			authHeader:     "Bearer master-key",
			expectedStatus: http.StatusOK,
			expectNextCall: true,
		},
		{
			name:           "PUT with file key",
			method:         "PUT",
			authHeader:     "Bearer file-key-1",
			expectedStatus: http.StatusOK,
			expectNextCall: true,
		},
		{
			name:           "DELETE with invalid key",
			method:         "DELETE",
			authHeader:     "Bearer invalid-key",
			expectedStatus: http.StatusUnauthorized,
			expectNextCall: false,
		},
		{
			name:           "PATCH with no auth",
			method:         "PATCH",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
			expectNextCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNext := &MockHandler{}

			req := httptest.NewRequest(tt.method, "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()

			err := middleware.ServeHTTP(w, req, mockNext)
			if err != nil {
				t.Errorf("ServeHTTP returned error: %v", err)
			}

			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if mockNext.called != tt.expectNextCall {
				t.Errorf("Expected next handler called=%v, got called=%v", tt.expectNextCall, mockNext.called)
			}
		})
	}
}

// TestUpdateAPIKeyCache_EdgeCases tests updateAPIKeyCache edge cases
func TestUpdateAPIKeyCache_EdgeCases(t *testing.T) {
	// Test with permit file
	permitContent := `{
		"params": {
			"permit_name": "test",
			"allowed_tokens": [],
			"chain_id": "secret-4",
			"permissions": []
		},
		"signature": {
			"pub_key": {
				"type": "tendermint/PubKeySecp256k1",
				"value": "test"
			},
			"signature": "test"
		}
	}`
	
	permitFile := createTempFileHelper(t, permitContent)
	defer cleanupTempFileHelper(t, permitFile)

	config := &Config{
		APIKey:          "master-key",
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
		PermitFile:      permitFile,
	}

	validator := NewAPIKeyValidator(config)

	// Mock the contract query function to test permit file usage
	originalQuery := queryContractFunc
	defer func() { queryContractFunc = originalQuery }()

	queryContractFunc = func(contractAddress string, query map[string]any) (map[string]any, error) {
		// Verify that the permit from file is being used
		if permitParams, exists := query["api_keys_with_permit"]; exists {
			if permitMap, ok := permitParams.(map[string]any); ok {
				if permit, ok := permitMap["permit"].(map[string]any); ok {
					if params, ok := permit["params"].(map[string]any); ok {
						if permitName, ok := params["permit_name"].(string); ok && permitName == "test" {
							// Return valid response
							return map[string]any{
								"api_keys": []any{
									map[string]any{"hashed_key": "test-hash-1"},
									map[string]any{"hashed_key": "test-hash-2"},
								},
							}, nil
						}
					}
				}
			}
		}
		return map[string]any{"api_keys": []any{}}, nil
	}

	// Test cache update with permit file
	err := validator.updateAPIKeyCache()
	if err != nil {
		t.Errorf("Expected no error but got: %v", err)
	}

	// Test that cache was updated
	if len(validator.cache) == 0 {
		t.Error("Expected cache to be populated")
	}
}

// TestProvision_EdgeCases tests additional provision edge cases
func TestProvision_EdgeCases(t *testing.T) {
	tests := []struct {
		name           string
		initialConfig  *Config
		expectError    bool
		expectNonNil   bool
	}{
		{
			name:          "completely nil config",
			initialConfig: nil,
			expectError:   false,
			expectNonNil:  true,
		},
		{
			name: "partial config with missing fields",
			initialConfig: &Config{
				APIKey: "test-key",
				// Missing other fields
			},
			expectError:  false,
			expectNonNil: true,
		},
		{
			name: "config with zero values",
			initialConfig: &Config{
				APIKey:          "",
				ContractAddress: "",
				CacheTTL:        0,
				MasterKeysFile:  "",
				PermitFile:      "",
			},
			expectError:  false,
			expectNonNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			middleware := &Middleware{Config: tt.initialConfig}
			ctx := caddy.Context{}

			err := middleware.Provision(ctx)

			if tt.expectError && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			if tt.expectNonNil {
				if middleware.Config == nil {
					t.Error("Expected config to be non-nil after provision")
				}
				if middleware.validator == nil {
					t.Error("Expected validator to be non-nil after provision")
				}
			}
		})
	}
}

// Helper functions
func createTempFileHelper(t *testing.T, content string) string {
	tmpFile, err := ioutil.TempFile("", "test-*.txt")
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

func cleanupTempFileHelper(t *testing.T, filename string) {
	if err := os.Remove(filename); err != nil {
		t.Errorf("Failed to remove temp file %s: %v", filename, err)
	}
}