package x402

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// PortalClient is the HTTP client for DevPortal balance and usage APIs.
type PortalClient struct {
	baseURL    string
	serviceKey string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewPortalClient creates a new DevPortal API client.
func NewPortalClient(baseURL, serviceKey string, logger *zap.Logger) *PortalClient {
	return &PortalClient{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		logger: logger,
	}
}

// GetBalance fetches the agent's balance from DevPortal, identified by API key.
// Returns the balance in USDC minor units (6 decimals), e.g. 20000 = $0.02.
func (c *PortalClient) GetBalance(apiKey string) (int64, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/agent/balance", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to build balance request: %w", err)
	}
	req.Header.Set(HeaderServiceKey, c.serviceKey)
	req.Header.Set(HeaderAPIKey, apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Portal balance request failed", zap.Error(err))
		return 0, ErrPortalUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("portal balance returned status %d", resp.StatusCode)
	}

	var result BalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode balance response: %w", err)
	}

	var balance int64
	if _, err := fmt.Sscanf(result.Balance, "%d", &balance); err != nil {
		return 0, fmt.Errorf("failed to parse balance %q: %w", result.Balance, err)
	}

	return balance, nil
}

// ReportUsage reports token usage to DevPortal after a successful LLM response.
func (c *PortalClient) ReportUsage(apiKey, model string, inputTokens, outputTokens int) error {
	report := UsageReport{
		APIKey:       apiKey,
		Model:        model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal usage report: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/user/report-usage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build report-usage request: %w", err)
	}
	req.Header.Set(HeaderServiceKey, c.serviceKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("report-usage request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("report-usage returned status %d", resp.StatusCode)
	}

	return nil
}
