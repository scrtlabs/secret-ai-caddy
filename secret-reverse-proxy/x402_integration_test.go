package secret_reverse_proxy

import (
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	userBalances map[string]float64 // api key hash → USD balance (legacy path)
	usageReports []x402.UsageReportRequest
	balanceCheck int // number of GetBalance/GetUserBalance calls received
	reportUsage  int // number of ReportUsage calls received
	serviceKey   string
	mu           sync.Mutex
	server       *httptest.Server
	// reportSignal fires once per ReportUsage call so tests can wait
	// deterministically for the async report goroutine instead of sleeping.
	reportSignal chan struct{}
}

func newTestPortalServer(serviceKey string) *testPortalServer {
	p := &testPortalServer{
		balances:     make(map[string]float64),
		userBalances: make(map[string]float64),
		serviceKey:   serviceKey,
		reportSignal: make(chan struct{}, 16),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/balance", p.handleBalance)
	mux.HandleFunc("/api/user/balance", p.handleUserBalance)
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

// handleUserBalance backs the DevPortal /api/user/balance endpoint used by
// the legacy (non-x402-agent) pre-request balance check, keyed by API key
// hash rather than wallet address.
func (p *testPortalServer) handleUserBalance(w http.ResponseWriter, r *http.Request) {
	svcKey := r.Header.Get("X-Agent-Service-Key")
	if svcKey == "" {
		http.Error(w, "missing service key", http.StatusUnauthorized)
		return
	}
	apiKeyHash := r.URL.Query().Get("api_key_hash")
	if apiKeyHash == "" {
		http.Error(w, "missing api key hash", http.StatusBadRequest)
		return
	}

	p.mu.Lock()
	p.balanceCheck++
	balance, known := p.userBalances[apiKeyHash]
	p.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if !known {
		json.NewEncoder(w).Encode(map[string]string{"status": "not_found"})
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "success",
		"user":   map[string]float64{"balance": balance},
	})
}

func (p *testPortalServer) handleReportUsage(w http.ResponseWriter, r *http.Request) {
	var report x402.UsageReportRequest
	json.NewDecoder(r.Body).Decode(&report)

	p.mu.Lock()
	p.usageReports = append(p.usageReports, report)
	p.reportUsage++
	p.mu.Unlock()

	select {
	case p.reportSignal <- struct{}{}:
	default:
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (p *testPortalServer) setBalance(walletAddr string, usd float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.balances[strings.ToLower(walletAddr)] = usd
}

func (p *testPortalServer) setUserBalance(apiKeyHash string, usd float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.userBalances[apiKeyHash] = usd
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

func (p *testPortalServer) getReportUsageCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reportUsage
}

func (p *testPortalServer) close() { p.server.Close() }

// --- Mock LLM Backend ---

type mockLLMHandler struct {
	called       bool
	responseBody string
	// statusCode, when non-zero, is written via WriteHeader before the body
	// so tests can simulate an upstream failure (e.g. 500).
	statusCode int
	// receivedBody captures the exact bytes the upstream saw, so tests can
	// assert that forwarding did not mutate/re-encode the request body.
	receivedBody []byte
	// receivedContentEncoding captures the Content-Encoding header as seen
	// by the upstream.
	receivedContentEncoding string
	// receivedContentLengthHeader captures the literal Content-Length header
	// value as seen by the upstream (empty if never set).
	receivedContentLengthHeader string
	// receivedContentLength captures r.ContentLength as seen by the upstream.
	receivedContentLength int64
}

func (h *mockLLMHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	h.called = true
	h.receivedContentEncoding = r.Header.Get("Content-Encoding")
	h.receivedContentLengthHeader = r.Header.Get("Content-Length")
	h.receivedContentLength = r.ContentLength
	if r.Body != nil {
		b, _ := io.ReadAll(r.Body)
		h.receivedBody = b
	}
	w.Header().Set("Content-Type", "application/json")
	if h.statusCode != 0 {
		w.WriteHeader(h.statusCode)
	}
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
	return buildX402MiddlewareWithMaxBodySize(t, portalURL, minBalanceUSD, serviceKey, 10*1024*1024)
}

// buildX402MiddlewareWithMaxBodySize is like buildX402Middleware but lets tests
// pin a small MaxBodySize so oversized-body tests don't need multi-MB payloads.
func buildX402MiddlewareWithMaxBodySize(t *testing.T, portalURL string, minBalanceUSD float64, serviceKey string, maxBodySize int64) *Middleware {
	t.Helper()
	config := &proxyconfig.Config{
		APIKey:              "master-key-123",
		CacheTTL:            time.Hour,
		X402Enabled:         true,
		DevPortalURL:        portalURL,
		DevPortalServiceKey: serviceKey,
		X402MinBalanceUSD:   minBalanceUSD,
		MaxBodySize:         maxBodySize,
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

// gzipCompress gzip-encodes s, for building gzip-encoded agent request bodies.
func gzipCompress(t *testing.T, s string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(s)); err != nil {
		t.Fatalf("failed to gzip-compress test body: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return buf.Bytes()
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

	// The usage report is sent from a goroutine; wait for the mock portal to
	// signal it received the call instead of sleeping a fixed duration.
	select {
	case <-portal.reportSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async usage report")
	}

	if got := portal.getReportUsageCount(); got != 1 {
		t.Fatalf("expected 1 usage report, got %d", got)
	}

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

func TestX402_UpstreamError_SkipsUsageReport(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.02) // $0.02 — plenty of funds

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{
		statusCode:   http.StatusInternalServerError,
		responseBody: `{"error":"upstream exploded"}`,
	}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected upstream 500 to pass through, got %d", w.Code)
	}
	if !next.called {
		t.Error("expected next handler to be called")
	}

	// Give the (would-be) async report goroutine a bounded window to fire;
	// it must never do so for a failed upstream response.
	select {
	case <-portal.reportSignal:
		t.Fatal("expected no usage report for a failed upstream request")
	case <-time.After(150 * time.Millisecond):
	}

	if got := portal.getReportUsageCount(); got != 0 {
		t.Errorf("expected 0 usage reports for failed upstream request, got %d", got)
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

func TestX402_EmbeddingsPathWithoutModel_Returns400(t *testing.T) {
	// Residual bypass being closed: /v1/embeddings is not a "known inference
	// path" under the old allowlist, so a model-less POST here used to be
	// proxied through for free. Under the free-pass inversion it must be
	// rejected like any other unrecognized billable route.
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"input":"hello world"}` // no "model" field
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/embeddings", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for embeddings path without model")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_TrailingSlashInferencePathWithoutModel_Returns400(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"messages":[{"role":"user","content":"hi"}]}` // no "model" field
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions/", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for trailing-slash inference path without model")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_ApiShowWithoutModel_ProxiedFreeNoBalanceCheck(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.0) // no funds — must not matter for this path

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"name":"llama3"}` // non-model JSON body
	req := agentReq(t, privKey, walletAddr, "POST", "/api/show", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !next.called {
		t.Error("expected next handler to be called for /api/show")
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

func TestLegacy_UpstreamError_SkipsTokenAccumulator(t *testing.T) {
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
	m.tokenCounter = factories.CreateTokenCounter()
	m.tokenAccumulator = NewTokenAccumulator()

	next := &mockLLMHandler{
		statusCode: http.StatusInternalServerError,
		// No "usage" object, so the tokenizer would otherwise count this
		// error body itself as output tokens.
		responseBody: `{"error":"upstream exploded, this message is deliberately long enough to tokenize to a nonzero count"}`,
	}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer master-key-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected upstream 500 to pass through, got %d", w.Code)
	}
	if !next.called {
		t.Error("expected next handler to be called")
	}

	usage := m.tokenAccumulator.PeekUsage()
	if len(usage) != 0 {
		t.Errorf("expected no accumulated usage for a failed upstream request, got %#v", usage)
	}
}

func TestX402_GzipBody_FundedWallet_ForwardsCompressedBytesUnchanged(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.02) // $0.02

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	plainBody := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	compressed := gzipCompress(t, plainBody)
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", string(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !next.called {
		t.Fatal("expected next handler to be called")
	}
	if portal.getBalanceCheckCount() != 1 {
		t.Errorf("expected exactly 1 balance check, got %d", portal.getBalanceCheckCount())
	}
	if next.receivedContentEncoding != "gzip" {
		t.Errorf("expected upstream to see Content-Encoding: gzip, got %q", next.receivedContentEncoding)
	}
	if !bytes.Equal(next.receivedBody, compressed) {
		t.Errorf("expected upstream to receive the original compressed bytes unchanged")
	}

	// Usage is reported from a goroutine; wait for it so it can't race with
	// portal.close() and leak a connection-refused log into later tests.
	select {
	case <-portal.reportSignal:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for async usage report")
	}
}

func TestX402_GzipBody_ZeroBalance_Returns402(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 0.0) // no funds

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	plainBody := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	compressed := gzipCompress(t, plainBody)
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", string(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402 (model detected through compression), got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called")
	}
	if portal.getBalanceCheckCount() != 1 {
		t.Errorf("expected exactly 1 balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_GzipContentEncodingWithGarbageBody_Returns400(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	garbage := "this is not gzip data"
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", garbage)
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for undecodable gzip body")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_GzipDecompressedBodyTooLarge_Returns413(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402MiddlewareWithMaxBodySize(t, portal.server.URL, 0.01, "test-service-key", 1024)
	next := &mockLLMHandler{}

	// The compressed bytes stay well under the cap, but the decompressed
	// content does not — this is a legitimate gzip stream, not garbage.
	plainBody := `{"model":"llama-3","padding":"` + strings.Repeat("x", 4096) + `"}`
	compressed := gzipCompress(t, plainBody)
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", string(compressed))
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for oversized decompressed body")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

func TestX402_OversizedBody_Returns413(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402MiddlewareWithMaxBodySize(t, portal.server.URL, 0.01, "test-service-key", 1024)
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"` + strings.Repeat("x", 2000) + `"}]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for oversized body")
	}
	if portal.getBalanceCheckCount() != 0 {
		t.Errorf("expected no balance check, got %d", portal.getBalanceCheckCount())
	}
}

// TestX402_ReadRequestBodyTruncated_Returns413 exercises the step 7
// defense-in-depth guard directly: step 1 already caps rawBody at
// effectiveMaxBodySize(m.Config.MaxBodySize), so readRequestBody normally
// can never report truncation for a request that reached step 7. To force
// that condition anyway, this test points m.bodyHandler at a cap smaller
// than the step-1 check, simulating any future drift between the two, and
// asserts the guard 413s instead of silently billing/proxying.
func TestX402_ReadRequestBodyTruncated_Returns413(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	privKey, walletAddr := generateTestWallet(t)
	portal.setBalance(walletAddr, 1.0) // ample balance, should never be checked

	m := buildX402MiddlewareWithMaxBodySize(t, portal.server.URL, 0.01, "test-service-key", 4096)
	// Force readRequestBody (step 7) to see a stricter cap than step 1 used.
	m.bodyHandler = factories.CreateBodyHandler(16)
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	req := agentReq(t, privKey, walletAddr, "POST", "/v1/chat/completions", body)
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called when readRequestBody reports truncation")
	}
	if portal.getReportUsageCount() != 0 {
		t.Errorf("expected no usage report, got %d", portal.getReportUsageCount())
	}
}

func TestLegacy_OversizedBody_BillingEnabled_Returns413(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()

	m := buildX402MiddlewareWithMaxBodySize(t, portal.server.URL, 0.01, "test-service-key", 1024)
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"` + strings.Repeat("x", 2000) + `"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer master-key-123")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status 413, got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called for oversized body")
	}
}

func TestLegacy_OversizedBody_BillingDisabled_ProxiesThrough(t *testing.T) {
	// With x402/billing disabled, an oversized body is proxied through unchanged —
	// today's behavior, pinned so the new 413 gate stays scoped to billing paths.
	config := &proxyconfig.Config{
		APIKey:      "master-key-123",
		CacheTTL:    time.Hour,
		X402Enabled: false,
		MaxBodySize: 1024,
	}

	m := &Middleware{
		Config:    config,
		validator: validators.NewAPIKeyValidator(config),
	}
	m.bodyHandler = factories.CreateBodyHandler(config.MaxBodySize)

	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"` + strings.Repeat("x", 2000) + `"}]}`
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

// legacyAPIKeyHash reproduces the sha256(apiKey) hex hash that the legacy
// pre-request balance check sends to DevPortal as api_key_hash, so tests can
// pre-seed testPortalServer.userBalances for a given Bearer API key.
func legacyAPIKeyHash(apiKey string) string {
	hasher := sha256.New()
	hasher.Write([]byte(apiKey))
	return hex.EncodeToString(hasher.Sum(nil))
}

func TestLegacy_GzipBody_FundedBalance_ForwardsDecompressedBody(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()
	portal.setUserBalance(legacyAPIKeyHash("master-key-123"), 1.0) // ample balance

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	plainBody := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	compressed := gzipCompress(t, plainBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(compressed))
	req.Header.Set("Authorization", "Bearer master-key-123")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
	if !next.called {
		t.Fatal("expected next handler to be called")
	}
	if portal.getBalanceCheckCount() != 1 {
		t.Errorf("expected exactly 1 balance check, got %d", portal.getBalanceCheckCount())
	}
	if next.receivedContentEncoding != "" {
		t.Errorf("expected upstream to see no Content-Encoding header, got %q", next.receivedContentEncoding)
	}
	if string(next.receivedBody) != plainBody {
		t.Errorf("expected upstream to receive the decompressed JSON %q, got %q", plainBody, string(next.receivedBody))
	}
	wantCL := strconv.Itoa(len(plainBody))
	if next.receivedContentLengthHeader != wantCL {
		t.Errorf("expected upstream Content-Length header %q, got %q", wantCL, next.receivedContentLengthHeader)
	}
	if next.receivedContentLength != int64(len(plainBody)) {
		t.Errorf("expected upstream r.ContentLength %d, got %d", len(plainBody), next.receivedContentLength)
	}
}

func TestLegacy_GzipBody_ZeroBalance_Returns402(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()
	portal.setUserBalance(legacyAPIKeyHash("master-key-123"), 0.0) // no funds

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	plainBody := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
	compressed := gzipCompress(t, plainBody)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(compressed))
	req.Header.Set("Authorization", "Bearer master-key-123")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()

	err := m.ServeHTTP(w, req, next)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.Code != http.StatusPaymentRequired {
		t.Errorf("expected status 402 (model detected through compression), got %d", w.Code)
	}
	if next.called {
		t.Error("expected next handler NOT to be called")
	}
	if portal.getBalanceCheckCount() != 1 {
		t.Errorf("expected exactly 1 balance check, got %d", portal.getBalanceCheckCount())
	}
}

// TestLegacy_NonGzipBody_ForwardsUnchanged is a regression pin: a legacy
// request with no Content-Encoding must be forwarded exactly as before the
// gzip-coherence fix — the fix must only engage when the body was actually
// decompressed.
func TestLegacy_NonGzipBody_ForwardsUnchanged(t *testing.T) {
	portal := newTestPortalServer("test-service-key")
	defer portal.close()
	portal.setUserBalance(legacyAPIKeyHash("master-key-123"), 1.0) // ample balance

	m := buildX402Middleware(t, portal.server.URL, 0.01, "test-service-key")
	next := &mockLLMHandler{}

	body := `{"model":"llama-3","messages":[{"role":"user","content":"hi"}]}`
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
		t.Fatal("expected next handler to be called")
	}
	if next.receivedContentEncoding != "" {
		t.Errorf("expected no Content-Encoding header, got %q", next.receivedContentEncoding)
	}
	if next.receivedContentLengthHeader != "" {
		t.Errorf("expected Content-Length header to remain unset (today's behavior), got %q", next.receivedContentLengthHeader)
	}
	if string(next.receivedBody) != body {
		t.Errorf("expected upstream to receive the original body unchanged, got %q", string(next.receivedBody))
	}
}
