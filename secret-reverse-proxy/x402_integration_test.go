package secret_reverse_proxy

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
	"go.uber.org/zap"

	proxyconfig "github.com/scrtlabs/secret-reverse-proxy/config"
	"github.com/scrtlabs/secret-reverse-proxy/factories"
	validators "github.com/scrtlabs/secret-reverse-proxy/validators"
	"github.com/scrtlabs/secret-reverse-proxy/x402"
)

// --- Test wallet helpers ---

func generateTestWallet(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("failed to generate test wallet: %v", err)
	}
	addr := crypto.PubkeyToAddress(privKey.PublicKey).Hex()
	return privKey, addr
}

// signRequest builds an EIP-191 signature matching DevPortal's verifySignature.ts.
// payload = method + path + body + timestamp (no separators)
// hash    = sha256(payload)
// sig     = personal_sign(hashBytes, privateKey)
func signRequest(t *testing.T, privKey *ecdsa.PrivateKey, method, path, body, timestamp string) string {
	t.Helper()
	payload := method + path + body + timestamp
	hashBytes := sha256.Sum256([]byte(payload))
	personalHash := accounts.TextHash(hashBytes[:])
	sig, err := crypto.Sign(personalHash, privKey)
	if err != nil {
		t.Fatalf("failed to sign request: %v", err)
	}
	sig[64] += 27 // normalize v: 0/1 → 27/28 (Ethereum convention)
	return "0x" + hex.EncodeToString(sig)
}

// --- Mock Portal Server ---

type testPortalServer struct {
	balances     map[string]float64 // wallet → USD balance
	usageReports []x402.UsageReportRequest
	balanceCheck int // number of GetBalance calls received
	serviceKey   string
	mu           sync.Mutex
	server       *httptest.Server
}

func newTestPortalServer(serviceKey string) *testPortalServer {
	p := &testPortalServer{
		balances:   make(map[string]float64),
		serviceKey: serviceKey,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/balance", p.handleBalance)
	mux.HandleFunc("/api/user/report-usage", p.handleReportUsage)
	p.server = httptest.NewServer(mux)
	return p
}

func (p *testPortalServer) handleBalance(w http.ResponseWriter, r *http.Request) {
	svcKey := r.Header.Get("X-Agent-Service-Key")
	if svcKey == "" {
		http.Error(w, "missing service key", http.StatusUnauthorized)
		return
	}
	walletAddr := r.Header.Get("X-Agent-Wallet-Address")
	if walletAddr == "" {
		walletAddr = r.URL.Query().Get("wallet_address")
	}
	if walletAddr == "" {
		http.Error(w, "missing wallet address", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	p.balanceCheck++
	balance := p.balances[strings.ToLower(walletAddr)]
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"balance": strconv.FormatFloat(balance, 'f', 6, 64),
	})
}

func (p *testPortalServer) handleReportUsage(w http.ResponseWriter, r *http.Request) {
	var report x402.UsageReportRequest
	json.NewDecoder(r.Body).Decode(&report)

	p.mu.Lock()
	p.usageReports = append(p.usageReports, report)
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (p *testPortalServer) setBalance(walletAddr string, usd float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.balances[strings.ToLower(walletAddr)] = usd
}

func (p *testPortalServer) getUsageReports() []x402.UsageReportRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]x402.UsageReportRequest, len(p.usageReports))
	copy(out, p.usageReports)
	return out
}

func (p *testPortalServer) getBalanceCheckCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.balanceCheck
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

func buildX402Middleware(t *testing.T, portalURL string, minBalanceUSD float64, serviceKey string) *Middleware {
	t.Helper()
	config := &proxyconfig.Config{
		APIKey:              "master-key-123",
		CacheTTL:            time.Hour,
		X402Enabled:         true,
		DevPortalURL:        portalURL,
		DevPortalServiceKey: serviceKey,
		X402MinBalanceUSD:   minBalanceUSD,
		MaxBodySize:         10 * 1024 * 1024,
	}

	logger := zap.NewNop()

	m := &Middleware{
		Config:    config,
		validator: validators.NewAPIKeyValidator(config),
	}

	m.bodyHandler = factories.CreateBodyHandler(config.MaxBodySize)
	m.portalClient = x402.NewPortalClient(portalURL, config.DevPortalServiceKey, logger)
	m.challengeBuilder = x402.NewChallengeBuilder(portalURL + "/api/agent/add-funds")
	m.x402MinBalance = minBalanceUSD

	return m
}

// agentReq builds a signed agent request for tests.
func agentReq(t *testing.T, privKey *ecdsa.PrivateKey, walletAddr, method, path, body string) *http.Request {
	t.Helper()
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	sig := signRequest(t, privKey, method, path, body, timestamp)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agent-Address", walletAddr)
	req.Header.Set("X-Agent-Signature", sig)
	req.Header.Set("X-Agent-Timestamp", timestamp)
	return req
}

// --- Tests ---

func TestX402_InsufficientBalance_Returns402(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.0) // no funds

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
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
	if w.Header().Get("Payment-Required") != "x402" {
		t.Errorf("expected Payment-Required header 'x402', got %q", w.Header().Get("Payment-Required"))
	}
}

func TestX402_SufficientBalance_ProxiesRequest(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.02) // $0.02

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
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
	if !strings.EqualFold(reports[0].WalletAddress, walletAddr) {
		t.Errorf("expected wallet_address %q in usage report, got %q", walletAddr, reports[0].WalletAddress)
	}
	if len(reports[0].UsageData) != 1 {
		t.Fatalf("expected 1 usage_data entry, got %d", len(reports[0].UsageData))
	}
}

func TestX402_MasterKey_BypassesPortal(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	// Portal has NO balance — but master key bypasses x402 path entirely (no x-agent-address)
	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
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

	// Portal should have received NO balance checks or usage reports
	reports := portal.getUsageReports()
	if len(reports) != 0 {
		t.Errorf("expected 0 usage reports for master key, got %d", len(reports))
	}
}

func TestX402_PortalUnreachable_Returns503(t *testing.T) {
	privKey, walletAddr := generateTestWallet(t)

	m := buildX402Middleware(t, "http://127.0.0.1:1", 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
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
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.005) // $0.005 — less than $0.01 threshold

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", `{"model":"llama-3","messages":[]}`)
	w := httptest.NewRecorder()

	m.ServeHTTP(w, req, next)

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402, got %d", w.Code)
	}

	var challenge x402.Challenge
	json.NewDecoder(w.Body).Decode(&challenge)

	if challenge.BalanceUSD != "0.005000" {
		t.Errorf("expected balance_usd '0.005000', got %q", challenge.BalanceUSD)
	}
	if challenge.TopupAmountUSD != "0.005000" {
		t.Errorf("expected topup_amount_usd '0.005000', got %q", challenge.TopupAmountUSD)
	}
}

func TestX402_InvalidSignature_Returns401(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	_, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0)

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	// Use a DIFFERENT private key to produce a bad signature
	wrongKey, _ := generateTestWallet(t)
	body := `{"model":"llama-3","messages":[]}`
	req := agentReq(t, wrongKey, walletAddr, "POST", "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	m.ServeHTTP(w, req, next)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called with invalid signature")
	}
}

func TestX402_InferencePathWithoutModel_Returns400(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"messages":[{"role":"user","content":"hi"}]}` // no "model" field
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for inference path without model")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_InferencePathModelViaTextPlain_BalanceStillChecked(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.0) // no funds

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
	req.Header.Set("Content-Type", "text/plain") // client mislabels content-type
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402 (balance checked and found insufficient), got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called when balance is insufficient")
	}
	if portal.getBalanceCheckCount() != 1 {
		t.Errorf("expected exactly 1 balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_NonInferenceGET_ProxiedFreeNoBalanceCheck(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.0) // no funds — must not matter for this path

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	req := agentReq(t, privKey, walletAddr, "GET", "/v1/models", "")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !next.called {
		t.Error("expected next handler to be called for non-inference GET")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestLegacy_InferencePathWithoutModel_Returns400(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"messages":[{"role":"user","content":"hi"}]}` // no "model" field
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer master-key-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for inference path without model")
	}
}

func TestLegacy_InferencePathWithoutModel_BillingDisabled_ProxiesThrough(t *testing.T) {
	// With x402/billing disabled, this middleware runs as a pure auth proxy —
	// a model-less POST to an inference path must be proxied through, not
	// rejected, since some backends legitimately default the model themselves.
	config := &proxyconfig.Config{
		APIKey:      "master-key-123",
		CacheTTL:    time.Hour,
		X402Enabled: false,
		MaxBodySize: 10 * 1024 * 1024,
	}

	m := &Middleware{
		Config:    config,
		validator: validators.NewAPIKeyValidator(config),
	}
	m.bodyHandler = factories.CreateBodyHandler(config.MaxBodySize)

	next := &mockLLMHandler{}

	body := `{"messages":[{"role":"user","content":"hi"}]}` // no "model" field
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
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
		t.Error("expected next handler to be called when billing is disabled")
	}
}
