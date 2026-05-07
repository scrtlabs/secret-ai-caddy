package x402

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.uber.org/zap"
)

// mockPortalServer creates a test HTTP server that simulates DevPortal.
type mockPortalServer struct {
	// balances maps API key -> balance in minor units
	balances map[string]int64
	// usageReports records all usage reports received
	usageReports []UsageReport
	mu           sync.Mutex
	server       *httptest.Server
}

func newMockPortalServer() *mockPortalServer {
	m := &mockPortalServer{
		balances: make(map[string]int64),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/agent/balance", m.handleBalance)
	mux.HandleFunc("/api/user/report-usage", m.handleReportUsage)
	m.server = httptest.NewServer(mux)
	return m
}

func (m *mockPortalServer) handleBalance(w http.ResponseWriter, r *http.Request) {
	serviceKey := r.Header.Get(HeaderServiceKey)
	if serviceKey == "" {
		http.Error(w, "missing service key", http.StatusUnauthorized)
		return
	}

	apiKey := r.Header.Get(HeaderAPIKey)
	if apiKey == "" {
		http.Error(w, "missing api key", http.StatusBadRequest)
		return
	}

	m.mu.Lock()
	balance, exists := m.balances[apiKey]
	if !exists {
		// Auto-create with 0 balance
		m.balances[apiKey] = 0
		balance = 0
	}
	m.mu.Unlock()

	resp := BalanceResponse{
		Balance: formatMinor(balance),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (m *mockPortalServer) handleReportUsage(w http.ResponseWriter, r *http.Request) {
	serviceKey := r.Header.Get(HeaderServiceKey)
	if serviceKey == "" {
		http.Error(w, "missing service key", http.StatusUnauthorized)
		return
	}

	var report UsageReport
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

func (m *mockPortalServer) close() {
	m.server.Close()
}

func (m *mockPortalServer) getUsageReports() []UsageReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	reports := make([]UsageReport, len(m.usageReports))
	copy(reports, m.usageReports)
	return reports
}

func formatMinor(v int64) string {
	return fmt.Sprintf("%d", v)
}

// --- Tests ---

func TestPortalClient_GetBalance_Funded(t *testing.T) {
	mock := newMockPortalServer()
	defer mock.close()

	mock.balances["test-key-1"] = 20000 // $0.02

	client := NewPortalClient(mock.server.URL, "svc-key", zap.NewNop())

	balance, err := client.GetBalance("test-key-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance != 20000 {
		t.Errorf("expected balance 20000, got %d", balance)
	}
}

func TestPortalClient_GetBalance_Zero(t *testing.T) {
	mock := newMockPortalServer()
	defer mock.close()

	client := NewPortalClient(mock.server.URL, "svc-key", zap.NewNop())

	balance, err := client.GetBalance("unknown-key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if balance != 0 {
		t.Errorf("expected balance 0, got %d", balance)
	}
}

func TestPortalClient_GetBalance_Unreachable(t *testing.T) {
	client := NewPortalClient("http://127.0.0.1:1", "svc-key", zap.NewNop())

	_, err := client.GetBalance("any-key")
	if err == nil {
		t.Fatal("expected error for unreachable portal")
	}
	if err != ErrPortalUnreachable {
		t.Errorf("expected ErrPortalUnreachable, got %v", err)
	}
}

func TestPortalClient_ReportUsage(t *testing.T) {
	mock := newMockPortalServer()
	defer mock.close()

	client := NewPortalClient(mock.server.URL, "svc-key", zap.NewNop())

	err := client.ReportUsage("test-key-1", "llama-3", 500, 1000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reports := mock.getUsageReports()
	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}
	if reports[0].APIKey != "test-key-1" {
		t.Errorf("expected api_key 'test-key-1', got %q", reports[0].APIKey)
	}
	if reports[0].Model != "llama-3" {
		t.Errorf("expected model 'llama-3', got %q", reports[0].Model)
	}
	if reports[0].InputTokens != 500 {
		t.Errorf("expected input_tokens 500, got %d", reports[0].InputTokens)
	}
	if reports[0].OutputTokens != 1000 {
		t.Errorf("expected output_tokens 1000, got %d", reports[0].OutputTokens)
	}
}

func TestPortalClient_ServiceKeyHeader(t *testing.T) {
	var receivedServiceKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedServiceKey = r.Header.Get(HeaderServiceKey)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(BalanceResponse{Balance: "0"})
	}))
	defer srv.Close()

	client := NewPortalClient(srv.URL, "my-secret-key", zap.NewNop())
	_, _ = client.GetBalance("any")

	if receivedServiceKey != "my-secret-key" {
		t.Errorf("expected service key 'my-secret-key', got %q", receivedServiceKey)
	}
}
