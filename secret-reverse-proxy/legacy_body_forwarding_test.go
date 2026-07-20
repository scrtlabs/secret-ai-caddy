package secret_reverse_proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caddyserver/caddy/v2"
)

// legacyMockHandler is a minimal caddyhttp.Handler stand-in for the legacy
// (non-x402) ServeHTTP path: it records exactly what bytes it received on
// r.Body and returns a canned backend response.
type legacyMockHandler struct {
	called       bool
	receivedBody []byte
	responseBody string
}

func (h *legacyMockHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	h.called = true
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		h.receivedBody = b
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(h.responseBody))
	return nil
}

// TestLegacyServeHTTP_CachedTokensExceedPromptTokens_InputTokensClampedToZero
// mirrors the x402 path's clamp test: on the legacy (Bearer-token) path, a
// backend reporting cached_tokens greater than prompt_tokens must never
// produce a negative non-cached input-token count (prompt_tokens -
// cached_tokens). A negative value would subtract from accumulated billable
// usage in metering deployments.
func TestLegacyServeHTTP_CachedTokensExceedPromptTokens_InputTokensClampedToZero(t *testing.T) {
	middleware := &Middleware{
		Config: &Config{
			APIKey:           "legacy-master-key",
			ContractAddress:  "test-contract",
			CacheTTL:         time.Hour,
			Metering:         true,
			MeteringInterval: time.Hour, // long enough to never fire during the test
			MeteringURL:      "http://test.example.com",
		},
	}

	ctx := caddy.Context{}
	if err := middleware.Provision(ctx); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	t.Cleanup(func() { middleware.Cleanup() })

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer legacy-master-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	next := &legacyMockHandler{
		// cached_tokens (50) exceeds prompt_tokens (10): a naive
		// prompt_tokens - cached_tokens would go negative.
		responseBody: `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"hi"}}],` +
			`"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":50}}}`,
	}

	err := middleware.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	if !next.called {
		t.Fatal("expected next handler to be called")
	}

	hasher := sha256.New()
	hasher.Write([]byte("legacy-master-key"))
	apiKeyHash := hex.EncodeToString(hasher.Sum(nil))

	usage := middleware.tokenAccumulator.FlushUsage()
	stats, ok := usage[apiKeyHash]
	if !ok {
		t.Fatalf("expected usage recorded for api key hash, got %+v", usage)
	}
	if stats.InputTokens != 0 {
		t.Errorf("expected input_tokens clamped to 0, got %d", stats.InputTokens)
	}
	if stats.OutputTokens != 5 {
		t.Errorf("expected output_tokens 5, got %d", stats.OutputTokens)
	}
}

// TestLegacyServeHTTP_TruncatedBodyBillingDisabled_ForwardsCompleteBody pins
// the complete-forward guarantee for a truncated request body when billing
// is off: readRequestBody/SafeReadRequestBody restores r.Body as the
// buffered prefix chained with the unread remainder specifically so this
// path never forwards a silently truncated body. The stream_options/defaults
// injection block must not undo that by re-deriving a forwarded body from
// just the (possibly-JSON-complete-looking) prefix, which would drop the
// remainder.
func TestLegacyServeHTTP_TruncatedBodyBillingDisabled_ForwardsCompleteBody(t *testing.T) {
	// head is a small, complete, valid JSON object with "stream": true so
	// that injectStreamUsageOption would successfully parse and rewrite it
	// if ever invoked on head alone.
	head := `{"model":"llama-3","stream":true}`
	tail := "TAIL-DATA-THAT-MUST-NOT-BE-DROPPED"
	fullBody := head + tail

	middleware := &Middleware{
		Config: &Config{
			APIKey:          "legacy-master-key",
			ContractAddress: "test-contract",
			CacheTTL:        time.Hour,
			// Billing disabled (Metering false, X402 disabled): this
			// middleware runs as a pure auth proxy and must forward the
			// complete body even when it exceeds MaxBodySize.
			MaxBodySize: int64(len(head) - 1),
		},
	}

	ctx := caddy.Context{}
	if err := middleware.Provision(ctx); err != nil {
		t.Fatalf("Provision failed: %v", err)
	}
	t.Cleanup(func() { middleware.Cleanup() })

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(fullBody))
	req.Header.Set("Authorization", "Bearer legacy-master-key")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	next := &legacyMockHandler{responseBody: `{}`}

	err := middleware.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.called {
		t.Fatal("expected next handler to be called")
	}

	got := string(next.receivedBody)
	if got != fullBody {
		t.Errorf("expected next handler to receive the complete unmodified body %q, got %q", fullBody, got)
	}
	if !strings.Contains(got, tail) {
		t.Errorf("expected forwarded body to retain the unread remainder %q, got %q", tail, got)
	}
	if strings.Contains(got, "include_usage") {
		t.Errorf("expected no stream_options injection against a truncated body, got %q", got)
	}
}
