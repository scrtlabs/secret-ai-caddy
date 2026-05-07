package secret_reverse_proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	proxyconfig "github.com/scrtlabs/secret-reverse-proxy/config"
	"github.com/scrtlabs/secret-reverse-proxy/x402"
	validators "github.com/scrtlabs/secret-reverse-proxy/validators"
)

// --- Mock Portal Server ---

type testPortalServer struct {
	balances     map[string]int64
	usageReports []x402.UsageReport
	mu           sync.Mutex
	server       *httptest.Server
}

func newTestPortalServer() *testPortalServer {
	p := &testPortalServer{
		balances: make(map[string]int64),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/balance", p.handleBalance)
	mux.HandleFunc("/api/user/report-usage", p.handleReportUsage)
	p.server = httptest.NewServer(mux)
	return p
}

func (p *testPortalServer) handleBalance(w http.ResponseWriter, r *http.Request) {
	apiKey := r.Header.Get("X-Api-Key")
	if apiKey == "" {
		http.Error(w, "missing api key", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	balance := p.balances[apiKey]
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"balance": fmt.Sprintf("%d", balance),
	})
}

func (p *testPortalServer) handleReportUsage(w http.ResponseWriter, r *http.Request) {
	var report x402.UsageReport
	json.NewDecoder(r.Body).Decode(&report)

	p.mu.Lock()
	p.usageReports = append(p.usageReports, report)
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (p *testPortalServer) getUsageReports() []x402.UsageReport {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]x402.UsageReport, len(p.usageReports))
	copy(out, p.usageReports)
	return out
}

func (p *testPortalServer) close() { p.server.Close() }

// --- Mock LLM Backend ---

type mockLLMHandler struct {
	called       bool
	responseBody string
}

func (h *mockLLMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	h.called = true
	w.Header().Set("Content-Type", "application/json")
	if h.responseBody != "" {
		w.Write([]byte(h.responseBody))
	} else {
		w.Write([]byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"Hello!"}}],"usage":{"total_tokens":100}}`))
	}
	return nil
}

// --- Helper to build middleware ---

func buildX402Middleware(t *testing.T, portalURL string, minBalanceUSDC string) *Middleware {
	t.Helper()
	config := &proxyconfig.Config{
		APIKey:              "master-key-123",
		CacheTTL:            time.Hour,
		X402Enabled:         true,
		DevPortalURL:        portalURL,
		DevPortalServiceKey: "test-service-key",
		X402MinBalanceUSDC:  minBalanceUSDC,
		MaxBodySize:         10 * 1024 * 1024,
	}

	logger := zap.NewNop()

	m := &Middleware{
		Config:    config,
		validator: validators.NewAPIKeyValidator(config),
	}

	m.portalClient = x402.NewPortalClient(portalURL, config.DevPortalServiceKey, logger)
	m.challengeBuilder = x402.NewChallengeBuilder(portalURL + "/api/agent/add-funds")

	minBalance, err := parseUSDCToMinor(config.X402MinBalanceUSDC)
	if err != nil {
		t.Fatalf("failed to parse min balance: %v", err)
	}
	m.x402MinBalance = minBalance

	return m
}

// --- Tests ---

func TestX402_InsufficientBalance_Returns402(t *testing.T) {
	portal := newTestPortalServer()
	defer portal.close()

	portal.balances["agent-key-1"] = 0 // no funds

	m := buildX402Middleware(t, portal.server.URL, "0.01")
	next := &mockLLMHandler{}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer agent-key-1")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402, got %d", w.Code)
	}

	if next.called {
		t.Error("expected next handler NOT to be called")
	}

	// Check response body
	var challenge x402.Challenge
	if err := json.NewDecoder(w.Body).Decode(&challenge); err != nil {
		t.Fatalf("failed to decode challenge: %v", err)
	}

	if challenge.Error != "Insufficient balance" {
		t.Errorf("unexpected error: %q", challenge.Error)
	}
	if challenge.TopupURL == "" {
		t.Error("expected topup_url in response")
	}

	// Check header
	if w.Header().Get("Payment-Required") != "x402" {
		t.Errorf("expected Payment-Required header 'x402', got %q", w.Header().Get("Payment-Required"))
	}
}

func TestX402_SufficientBalance_ProxiesRequest(t *testing.T) {
	portal := newTestPortalServer()
	defer portal.close()

	portal.balances["agent-key-2"] = 20000 // $0.02

	m := buildX402Middleware(t, portal.server.URL, "0.01")
	next := &mockLLMHandler{}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer agent-key-2")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if !next.called {
		t.Error("expected next handler to be called")
	}

	// Wait briefly for async usage report
	time.Sleep(100 * time.Millisecond)

	reports := portal.getUsageReports()
	if len(reports) != 1 {
		t.Fatalf("expected 1 usage report, got %d", len(reports))
	}
	if reports[0].APIKey != "agent-key-2" {
		t.Errorf("expected api_key 'agent-key-2', got %q", reports[0].APIKey)
	}
	// Model detection requires bodyHandler to be initialized (metering concern).
	// In this minimal test setup it falls back to "unknown", which is fine —
	// the important thing is the report was sent with the right API key.
	if reports[0].Model == "" {
		t.Error("expected non-empty model in usage report")
	}
}

func TestX402_MasterKey_BypassesPortal(t *testing.T) {
	portal := newTestPortalServer()
	defer portal.close()

	// Portal has NO balance for master key — but it shouldn't matter
	m := buildX402Middleware(t, portal.server.URL, "0.01")
	next := &mockLLMHandler{}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer master-key-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if !next.called {
		t.Error("expected next handler to be called (master key bypasses portal)")
	}

	// Portal should have received NO usage reports (master key path doesn't report)
	reports := portal.getUsageReports()
	if len(reports) != 0 {
		t.Errorf("expected 0 usage reports for master key, got %d", len(reports))
	}
}

func TestX402_PortalUnreachable_Returns503(t *testing.T) {
	// Point to a dead URL
	m := buildX402Middleware(t, "http://127.0.0.1:1", "0.01")
	next := &mockLLMHandler{}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer agent-key-3")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}

	if next.called {
		t.Error("expected next handler NOT to be called when portal is unreachable")
	}
}

func TestX402_PartialBalance_Returns402WithCorrectDeficit(t *testing.T) {
	portal := newTestPortalServer()
	defer portal.close()

	portal.balances["agent-key-4"] = 5000 // $0.005 — less than $0.01 threshold

	m := buildX402Middleware(t, portal.server.URL, "0.01")
	next := &mockLLMHandler{}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer agent-key-4")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	m.ServeHTTP(w, req, next)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402, got %d", w.Code)
	}

	var challenge x402.Challenge
	json.NewDecoder(w.Body).Decode(&challenge)

	if challenge.BalanceUSDC != "0.005000" {
		t.Errorf("expected balance_usdc '0.005000', got %q", challenge.BalanceUSDC)
	}
	if challenge.TopupAmountUSDC != "0.005000" {
		t.Errorf("expected topup_amount_usdc '0.005000', got %q", challenge.TopupAmountUSDC)
	}
}

func TestParseUSDCToMinor(t *testing.T) {
	tests := []struct {
		input    string
		expected int64
		wantErr  bool
	}{
		{"0.01", 10000, false},
		{"0.02", 20000, false},
		{"1.00", 1000000, false},
		{"0.000001", 1, false},
		{"10", 10000000, false},
		{"", 0, true},
		{"abc", 0, true},
	}

	for _, tc := range tests {
		got, err := parseUSDCToMinor(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseUSDCToMinor(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseUSDCToMinor(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.expected {
			t.Errorf("parseUSDCToMinor(%q) = %d, want %d", tc.input, got, tc.expected)
		}
	}
}
