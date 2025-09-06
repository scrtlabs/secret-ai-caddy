package util

import (
	"os"
	"testing"
)

// Test helper to create a temporary file with content
func CreateTempFile(t *testing.T, content string) string {
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
func CleanupTempFile(t *testing.T, filename string) {
	if err := os.Remove(filename); err != nil {
		t.Logf("Warning: Failed to remove temp file %s: %v", filename, err)
	}
}
