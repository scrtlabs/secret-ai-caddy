package x402

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// SecretVMClientImpl is the authenticated HTTP client for SecretVM agent APIs.
type SecretVMClientImpl struct {
	baseURL      string
	agentKey     string
	agentAddress string
	httpClient   *http.Client
	logger       *zap.Logger
}

// NewSecretVMClient creates a new SecretVM API client.
func NewSecretVMClient(baseURL, agentKey, agentAddress string, logger *zap.Logger) *SecretVMClientImpl {
	return &SecretVMClientImpl{
		baseURL:      baseURL,
		agentKey:     agentKey,
		agentAddress: agentAddress,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		logger: logger,
	}
}

// buildSignedRequest creates an HTTP request with agent authentication headers.
func (c *SecretVMClientImpl) buildSignedRequest(method, path string, body []byte) (*http.Request, error) {
	timestamp := time.Now().UTC().Format(time.RFC3339)

	// Canonical payload: METHOD + "\n" + PATH_ONLY + "\n" + BODY + "\n" + TIMESTAMP
	canonical := method + "\n" + path + "\n" + string(body) + "\n" + timestamp

	mac := hmac.New(sha256.New, []byte(c.agentKey))
	mac.Write([]byte(canonical))
	signature := hex.EncodeToString(mac.Sum(nil))

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}

	req.Header.Set(HeaderAgentAddress, c.agentAddress)
	req.Header.Set(HeaderAgentSignature, signature)
	req.Header.Set(HeaderAgentTimestamp, timestamp)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}

	return req, nil
}

// GetBalance fetches the given agent's balance from SecretVM.
// The caddy proxy authenticates as itself, and passes the queried agent as a query parameter.
func (c *SecretVMClientImpl) GetBalance(agentAddress string) (int64, error) {
	req, err := c.buildSignedRequest("GET", "/api/agent/balance", nil)
	if err != nil {
		return 0, fmt.Errorf("failed to build balance request: %w", err)
	}
	q := req.URL.Query()
	q.Set("address", agentAddress)
	req.URL.RawQuery = q.Encode()

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("balance request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("balance request returned status %d", resp.StatusCode)
	}

	var result struct {
		Balance int64 `json:"balance"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, fmt.Errorf("failed to decode balance response: %w", err)
	}

	return result.Balance, nil
}

// AddFunds adds funds to the agent's balance via SecretVM, handling 402 retry flow.
func (c *SecretVMClientImpl) AddFunds(agentAddress string, amount int64) error {
	body, _ := json.Marshal(map[string]any{
		"agent_address": agentAddress,
		"amount":        amount,
	})

	req, err := c.buildSignedRequest("POST", "/api/agent/add-funds", body)
	if err != nil {
		return fmt.Errorf("failed to build add-funds request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("add-funds request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPaymentRequired {
		// TODO: Handle 402 retry with payment-signature when payment flow is implemented
		return fmt.Errorf("add-funds requires payment (402)")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("add-funds returned status %d", resp.StatusCode)
	}

	return nil
}

// GetVMStatus fetches VM status from SecretVM.
func (c *SecretVMClientImpl) GetVMStatus(vmID string) (string, error) {
	path := "/api/agent/vm/" + vmID
	req, err := c.buildSignedRequest("GET", path, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build vm-status request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("vm-status request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("vm-status returned status %d", resp.StatusCode)
	}

	var result struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode vm-status response: %w", err)
	}

	return result.Status, nil
}

// ListAgents returns all agent addresses with funded balances.
func (c *SecretVMClientImpl) ListAgents() ([]string, error) {
	req, err := c.buildSignedRequest("GET", "/api/agent/list", nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build list-agents request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list-agents request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("list-agents returned status %d", resp.StatusCode)
	}

	var result struct {
		Agents []string `json:"agents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode list-agents response: %w", err)
	}

	return result.Agents, nil
}
