package x402

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func makeSignedRequest(method, path, body, agentKey string) *http.Request {
	timestamp := time.Now().UTC().Format(time.RFC3339)
	canonical := method + "\n" + path + "\n" + body + "\n" + timestamp

	mac := hmac.New(sha256.New, []byte(agentKey))
	mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set(HeaderAgentAddress, "agent-123")
	req.Header.Set(HeaderAgentSignature, signature)
	req.Header.Set(HeaderAgentTimestamp, timestamp)
	return req
}

func TestAuthVerifier_IsAgentRequest(t *testing.T) {
	v := NewAuthVerifier("testkey", 5*time.Minute, testLogger())

	// With header
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set(HeaderAgentAddress, "agent-123")
	if !v.IsAgentRequest(req) {
		t.Fatal("expected IsAgentRequest to return true")
	}

	// Without header
	req2 := httptest.NewRequest("GET", "/test", nil)
	if v.IsAgentRequest(req2) {
		t.Fatal("expected IsAgentRequest to return false")
	}
}

func TestAuthVerifier_ValidSignature(t *testing.T) {
	key := "test-secret-key"
	v := NewAuthVerifier(key, 5*time.Minute, testLogger())

	req := makeSignedRequest("POST", "/api/chat", `{"model":"llama"}`, key)
	addr, err := v.Verify(req, []byte(`{"model":"llama"}`))
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}
	if addr != "agent-123" {
		t.Fatalf("expected agent-123, got %s", addr)
	}
}

func TestAuthVerifier_InvalidSignature(t *testing.T) {
	v := NewAuthVerifier("correct-key", 5*time.Minute, testLogger())

	req := makeSignedRequest("POST", "/api/chat", `{"model":"llama"}`, "wrong-key")
	_, err := v.Verify(req, []byte(`{"model":"llama"}`))
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature, got %v", err)
	}
}

func TestAuthVerifier_StaleTimestamp(t *testing.T) {
	key := "test-key"
	v := NewAuthVerifier(key, 1*time.Second, testLogger())

	// Create a request with an old timestamp
	oldTime := time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339)
	body := `{"model":"llama"}`
	canonical := "POST\n/test\n" + body + "\n" + oldTime

	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	req := httptest.NewRequest("POST", "/test", strings.NewReader(body))
	req.Header.Set(HeaderAgentAddress, "agent-123")
	req.Header.Set(HeaderAgentSignature, signature)
	req.Header.Set(HeaderAgentTimestamp, oldTime)

	_, err := v.Verify(req, []byte(body))
	if !errors.Is(err, ErrStaleTimestamp) {
		t.Fatalf("expected ErrStaleTimestamp, got %v", err)
	}
}

func TestAuthVerifier_MissingHeaders(t *testing.T) {
	v := NewAuthVerifier("key", 5*time.Minute, testLogger())

	req := httptest.NewRequest("GET", "/test", nil)
	// No headers set
	_, err := v.Verify(req, nil)
	if !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("expected ErrInvalidSignature for missing headers, got %v", err)
	}
}
