package x402

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/scrtlabs/secret-reverse-proxy/utils"
)

// PortalClient is the HTTP client for DevPortal balance and usage APIs.
type PortalClient struct {
	baseURL    string
	serviceKey string
	httpClient *http.Client
	logger     *zap.Logger
}

// NewPortalClient creates a new DevPortal API client.
// Respects SKIP_SSL_VALIDATION env var (via utils.GetHTTPClient) for dev/test environments.
func NewPortalClient(baseURL, serviceKey string, logger *zap.Logger) *PortalClient {
	httpClient := utils.GetHTTPClient()
	httpClient.Timeout = 10 * time.Second
	return &PortalClient{
		baseURL:    baseURL,
		serviceKey: serviceKey,
		httpClient: httpClient,
		logger:     logger,
	}
}

// hmacServiceKey computes HMAC-SHA256(serviceKey, payload) as a hex string.
// DevPortal balance endpoint requires: hmac(AGENT_BALANCE_SERVICE_KEYS[i], wallet.toLowerCase())
func hmacServiceKey(secret, payload string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return hex.EncodeToString(mac.Sum(nil))
}

// GetBalance fetches the agent's balance from DevPortal, identified by wallet address.
// Returns the balance in USD as float64, e.g. 0.02 = $0.02.
//
// DevPortal /api/agent/balance requires:
//   - x-agent-service-key: hmac_sha256(AGENT_BALANCE_SERVICE_KEYS[i], wallet.toLowerCase())
//   - x-agent-wallet-address: 0x... (checksummed EVM address)
func (c *PortalClient) GetBalance(walletAddr string) (float64, error) {
	req, err := http.NewRequest("GET", c.baseURL+"/api/agent/balance", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to build balance request: %w", err)
	}

	// HMAC is over the lowercase wallet address (matches DevPortal's signaturePayload logic)
	serviceKeyHMAC := hmacServiceKey(c.serviceKey, strings.ToLower(walletAddr))
	req.Header.Set(HeaderServiceKey, serviceKeyHMAC)
	req.Header.Set(HeaderWalletAddress, walletAddr)

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

	balance, err := strconv.ParseFloat(strings.TrimSpace(result.Balance), 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse balance %q: %w", result.Balance, err)
	}

	return balance, nil
}

// ReportUsage reports token usage to DevPortal after a successful LLM response.
//   - x-agent-service-key: raw service key (raw key is accepted, no HMAC required)
//   - x-agent-wallet-address: 0x... agent wallet
//   - body: {"usage_data": [{input_tokens, output_tokens, timestamp, model}], "wallet_address": "0x..."}
func (c *PortalClient) ReportUsage(walletAddr, model string, inputTokens, outputTokens int, timestamp int64) error {
	report := UsageReportRequest{
		UsageData: []UsageEntry{
			{
				InputTokens:  inputTokens,
				OutputTokens: outputTokens,
				Timestamp:    timestamp,
				Model:        model,
			},
		},
		WalletAddress: walletAddr,
	}

	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("failed to marshal usage report: %w", err)
	}

	req, err := http.NewRequest("POST", c.baseURL+"/api/user/report-usage", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build report-usage request: %w", err)
	}
	// Raw service key is accepted by report-usage endpoint (no HMAC needed here)
	req.Header.Set(HeaderServiceKey, c.serviceKey)
	req.Header.Set(HeaderWalletAddress, walletAddr)
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

// GetUserBalance fetches the legacy user's balance from DevPortal by API key hash.
// Returns the balance in USD as float64, or -1 if the user is not found (fail-open).
//
// DevPortal /api/user/balance requires:
//   - x-agent-service-key: hmac_sha256(AGENT_BALANCE_SERVICE_KEYS[i], api_key_hash.toLowerCase())
//   - query param: api_key_hash
func (c *PortalClient) GetUserBalance(apiKeyHash string) (float64, error) {
	url := c.baseURL + "/api/user/balance?api_key_hash=" + apiKeyHash
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to build user balance request: %w", err)
	}

	serviceKeyHMAC := hmacServiceKey(c.serviceKey, strings.ToLower(apiKeyHash))
	req.Header.Set(HeaderServiceKey, serviceKeyHMAC)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Error("Portal user balance request failed", zap.Error(err))
		return 0, ErrPortalUnreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("portal user balance returned status %d", resp.StatusCode)
	}

	var result UserBalanceResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode user balance response: %w", err)
	}

	if result.Status == "not_found" || result.User == nil {
		return -1, nil // user unknown — fail-open
	}

	return result.User.Balance, nil
}
