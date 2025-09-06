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
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"

	apikeyval "github.com/scrtlabs/secret-reverse-proxy/validators"
	validators "github.com/scrtlabs/secret-reverse-proxy/validators"
)

// TestParseCaddyfileDirectly tests the parseCaddyfile function directly
func TestParseCaddyfileDirectly(t *testing.T) {
	// Create a test dispenser with proper format
	d := caddyfile.NewTestDispenser("secret_reverse_proxy {\nAPI_MASTER_KEY test-key\n}")

	// Create the helper struct that matches the expected signature
	helper := httpcaddyfile.Helper{
		Dispenser: d,
	}

	// Call parseCaddyfile
	result, err := parseCaddyfile(helper)
	if err != nil {
		t.Errorf("parseCaddyfile failed: %v", err)
		return
	}

	// Verify the result
	if result == nil {
		t.Error("Expected non-nil result from parseCaddyfile")
		return
	}

	// The function returns Middleware (value), not *Middleware (pointer)
	middleware, ok := result.(Middleware)
	if !ok {
		t.Errorf("Expected result to be Middleware, got %T", result)
		return
	}

	if middleware.Config == nil {
		t.Error("Expected config to be set")
		return
	}

	if middleware.Config.APIKey != "test-key" {
		t.Errorf("Expected APIKey to be 'test-key', got '%s'", middleware.Config.APIKey)
	}
}

// TestUpdateAPIKeyCache_PermitFileErrors tests error handling in updateAPIKeyCache
func TestUpdateAPIKeyCache_PermitFileErrors(t *testing.T) {
	// Test with invalid permit file
	invalidPermitFile := createTempFileHelper2(t, "invalid json content")
	defer cleanupTempFileHelper2(t, invalidPermitFile)

	config := &Config{
		APIKey:          "master-key",
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
		PermitFile:      invalidPermitFile,
	}

	validator := apikeyval.NewAPIKeyValidator(config)

	// Try to update cache with invalid permit file
	err := validator.UpdateAPIKeyCache()
	if err == nil {
		t.Error("Expected error with invalid permit file")
	}
	if !strings.Contains(err.Error(), "failed to read permit file") {
		t.Errorf("Expected specific error message, got: %v", err)
	}

	// Test with nonexistent permit file
	config.PermitFile = "/nonexistent/file.json"
	validator = validators.NewAPIKeyValidator(config)

	err = validator.UpdateAPIKeyCache()
	if err == nil {
		t.Error("Expected error with nonexistent permit file")
	}
}

// TestServeHTTP_ValidationErrorPaths tests specific validation error paths in ServeHTTP
func TestServeHTTP_ValidationErrorPaths(t *testing.T) {
	// Create a validator that will always fail validation due to file errors
	config := &Config{
		APIKey:          "master-key",
		MasterKeysFile:  "/nonexistent/path/keys.txt", // Will cause file read errors
		ContractAddress: "test-contract",
		CacheTTL:        time.Hour,
	}

	middleware := &Middleware{
		Config:    config,
		validator: apikeyval.NewAPIKeyValidator(config),
	}

	mockNext := &MockHandler{}

	// Test that validation errors in ServeHTTP are handled properly
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer some-non-master-key")
	w := httptest.NewRecorder()

	err := middleware.ServeHTTP(w, req, mockNext)
	if err != nil {
		t.Errorf("ServeHTTP should not return error: %v", err)
	}

	// Should return 500 due to validation error
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
	}

	if mockNext.called {
		t.Error("Next handler should not be called on validation error")
	}

	// Test with empty auth header value after extraction
	req2 := httptest.NewRequest("GET", "/test", nil)
	req2.Header.Set("Authorization", "Bearer ")
	w2 := httptest.NewRecorder()

	err = middleware.ServeHTTP(w2, req2, mockNext)
	if err != nil {
		t.Errorf("ServeHTTP should not return error: %v", err)
	}

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("Expected status %d for empty extracted key, got %d", http.StatusUnauthorized, w2.Code)
	}
}

// TestCheckMasterKeys_ErrorConditions tests error conditions in checkMasterKeys
func TestCheckMasterKeys_ErrorConditions(t *testing.T) {
	// Test with directory instead of file (should cause read error)
	tmpDir, err := ioutil.TempDir("", "test-dir")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := &Config{
		MasterKeysFile: tmpDir, // Directory, not file
	}

	validator := apikeyval.NewAPIKeyValidator(config)

	// This should cause an error when trying to read the directory as a file
	found, err := validator.CheckMasterKeys("any-key")
	if err == nil {
		t.Error("Expected error when reading directory as file")
	}
	if found {
		t.Error("Should not find key when there's a read error")
	}
}

// TestUnmarshalCaddyfile_CompleteEdgeCases tests remaining edge cases
func TestUnmarshalCaddyfile_CompleteEdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		expectError bool
		validate    func(*testing.T, *Middleware)
	}{
		{
			name:        "empty directive block",
			input:       "secret_reverse_proxy",
			expectError: false,
			validate: func(t *testing.T, m *Middleware) {
				// Should have default config
				if m.Config == nil {
					t.Error("Expected config to be set with defaults")
				}
			},
		},
		{
			name:        "directive with just opening brace",
			input:       "secret_reverse_proxy {",
			expectError: true, // Should fail to parse
		},
		{
			name:        "cache_ttl with zero duration",
			input:       "secret_reverse_proxy\ncache_ttl 0s",
			expectError: false,
			validate: func(t *testing.T, m *Middleware) {
				if m.Config.CacheTTL != 0 {
					t.Errorf("Expected CacheTTL to be 0, got %v", m.Config.CacheTTL)
				}
			},
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
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				if tt.validate != nil {
					tt.validate(t, middleware)
				}
			}
		})
	}
}

// TestProvision_LoggerEdgeCases tests logger-related provision scenarios
func TestProvision_LoggerEdgeCases(t *testing.T) {
	middleware := &Middleware{
		Config: &Config{
			APIKey:          "test-key",
			ContractAddress: "test-contract",
			CacheTTL:        time.Hour,
		},
	}

	// Test provision with various context scenarios
	ctx := caddy.Context{}

	err := middleware.Provision(ctx)
	if err != nil {
		t.Errorf("Provision failed: %v", err)
	}

	// Note: logger is internal to the middleware, can't access it directly
	// But we can verify the provision completed successfully

	// Verify validator was created
	if middleware.validator == nil {
		t.Error("Expected validator to be initialized")
	}
}

// Helper functions for this file
func createTempFileHelper2(t *testing.T, content string) string {
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

func cleanupTempFileHelper2(t *testing.T, filename string) {
	if err := os.Remove(filename); err != nil {
		t.Errorf("Failed to remove temp file %s: %v", filename, err)
	}
}