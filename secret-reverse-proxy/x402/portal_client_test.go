package x402

import (
	"crypto/hmac"
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

	"go.uber.org/zap"
)

// mockPortalServer simulates DevPortal for portal client tests.
type mockPortalServer struct {
	balances     map[string]float64 // lowercase wallet → USD balance
	usageReports []UsageReportRequest
	serviceKey   string // expected HMAC secret
	mu           sync.Mutex
	server       *httptest.Server
}

func newMockPortalServer(serviceKey string) *mockPortalServer {
	m := &mockPortalServer{
		balances:   make(map[string]float64),
		serviceKey: serviceKey,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/balance", m.handleBalance)
	mux.HandleFunc("/api/user/report-usage", m.handleReportUsage)
	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockPortalServer) handleBalance(w http.ResponseWriter, r *http.Request) {
	svcKey := r.Header.Get(HeaderServiceKey)
	if svcKey == "" {
		http.Error(w, "missing service key", http.StatusUnauthorized)
		return
	}

	walletAddr := r.Header.Get(HeaderWalletAddress)
	if walletAddr == "" {
		walletAddr = r.URL.Query().Get("wallet_address")
	}
	if walletAddr == "" {
		http.Error(w, "missing wallet address", http.StatusBadRequest)
		return
	}

	// Verify HMAC(serviceKey, wallet.toLowerCase()) == svcKey header
	mac := hmac.New(sha256.New, []byte(m.serviceKey))
	mac.Write([]byte(strings.ToLower(walletAddr)))
	expected := hex.EncodeToString(mac.Sum(nil))
	if svcKey != expected {
		http.Error(w, "invalid service key", http.StatusUnauthorized)
		return
	}

	m.mu.Lock()
	balance := m.balances[strings.ToLower(walletAddr)]
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(BalanceResponse{
		Balance: strconv.FormatFloat(balance, 'f', 6, 64),
	})
}

func (m *mockPortalServer) handleReportUsage(w http.ResponseWriter, r *http.Request) {
	svcKey := r.Header.Get(HeaderServiceKey)
	if svcKey == "" {
		http.Error(w, "missing service key", http.StatusUnauthorized)
		return
	}

	var report UsageReportRequest
	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	m.usageReports = append(m.usageReports, report)
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"success": true})
}

func (m *mockPortalServer) close() { m.server.Close() }

func (m *mockPortalServer) getUsageReports() []UsageReportRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]UsageReportRequest, len(m.usageReports))
	copy(out, m.usageReports)
	return out
}

// --- Tests ---

func TestPortalClient_GetBalance_Funded(t *testing.T) {
	mock := newMockPortalServer("svc-key")
	defer mock.close()

	wallet := "0xAbCd1234000000000000000000000000000000Ab"
	mock.balances[strings.ToLower(wallet)] = 0.02

	client := NewPortalClient(mock.server.URL, "svc-key", zap.NewNop())

	balance, err := client.GetBalance(wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fmt.Sprintf("%.6f", balance) != "0.020000" {
		t.Errorf("expected balance 0.02, got %f", balance)
	}
}

func TestPortalClient_GetBalance_Zero(t *testing.T) {
	mock := newMockPortalServer("svc-key")
	defer mock.close()

	wallet := "0xDeAdBeEf000000000000000000000000DeAdBeEf"
	// no entry in balances → default 0

	client := NewPortalClient(mock.server.URL, "svc-key", zap.NewNop())

	balance, err := client.GetBalance(wallet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance != 0.0 {
		t.Errorf("expected balance 0, got %f", balance)
	}
}

func TestPortalClient_GetBalance_Unreachable(t *testing.T) {
	client := NewPortalClient("http://127.0.0.1:1", "svc-key", zap.NewNop())

	_, err := client.GetBalance("0x1234000000000000000000000000000000001234")
	if err == nil {
		t.Fatal("expected error for unreachable portal")
	}
	if err != ErrPortalUnreachable {
		t.Errorf("expected ErrPortalUnreachable, got %v", err)
	}
}

func TestPortalClient_GetBalance_HMACVerified(t *testing.T) {
	mock := newMockPortalServer("my-secret")
	defer mock.close()

	wallet := "0x1111000000000000000000000000000000001111"
	mock.balances[strings.ToLower(wallet)] = 0.5

	// Correct HMAC key → should succeed
	client := NewPortalClient(mock.server.URL, "my-secret", zap.NewNop())
	balance, err := client.GetBalance(wallet)
	if err != nil {
		t.Fatalf("correct HMAC should succeed: %v", err)
	}
	if balance != 0.5 {
		t.Errorf("expected 0.5, got %f", balance)
	}

	// Wrong HMAC key → should fail with non-200
	badClient := NewPortalClient(mock.server.URL, "wrong-key", zap.NewNop())
	_, err = badClient.GetBalance(wallet)
	if err == nil {
		t.Error("wrong service key should return error")
	}
}

func TestPortalClient_ReportUsage(t *testing.T) {
	mock := newMockPortalServer("svc-key")
	defer mock.close()

	client := NewPortalClient(mock.server.URL, "svc-key", zap.NewNop())
	wallet := "0xAAAA000000000000000000000000000000AAAA00"

	err := client.ReportUsage(wallet, "llama-3", 500, 1000, 1234567890)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reports := mock.getUsageReports()
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].WalletAddress != wallet {
		t.Errorf("expected wallet %q, got %q", wallet, reports[0].WalletAddress)
	}
	if len(reports[0].UsageData) != 1 {
		t.Fatalf("expected 1 usage_data entry, got %d", len(reports[0].UsageData))
	}
	entry := reports[0].UsageData[0]
	if entry.Model != "llama-3" {
		t.Errorf("expected model 'llama-3', got %q", entry.Model)
	}
	if entry.InputTokens != 500 {
		t.Errorf("expected input_tokens 500, got %d", entry.InputTokens)
	}
	if entry.OutputTokens != 1000 {
		t.Errorf("expected output_tokens 1000, got %d", entry.OutputTokens)
	}
	if entry.Timestamp != 1234567890 {
		t.Errorf("expected timestamp 1234567890, got %d", entry.Timestamp)
	}
}
