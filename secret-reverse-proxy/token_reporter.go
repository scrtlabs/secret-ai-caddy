package secret_reverse_proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

// StartTokenReportingLoop runs a goroutine to flush usage every interval and call ReportUsageFn.
func (m *Middleware) StartTokenReportingLoop(interval time.Duration) {
	logger := caddy.Log()
	logger.Info("Starting token usage reporting loop", zap.Duration("interval", interval))

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		
		for {
			select {
			case <-ticker.C:
				usage := m.accumulator.FlushUsage()
				if len(usage) == 0 {
					logger.Debug("No usage to report this cycle")
					continue
				}

				// Convert usage to the required format
				usageData := make(map[string]map[string]any)
				for apiKeyHash, usageStats := range usage {
					usageData[apiKeyHash] = map[string]any{
						"input_tokens":  usageStats.InputTokens,
						"output_tokens": usageStats.OutputTokens,
						"timestamp":     usageStats.LastUpdatedAt.Unix(),
					}
				}

				payload := map[string]any{
					"usage_data": usageData,
				}

				logger.Info("Reporting usage to metering endpoint", zap.Int("api_key_count", len(usageData)))
				err := m.reportUsageToEndpoint(payload)
				if err != nil {
					logger.Error("❌ Failed to report usage to metering endpoint", zap.Error(err))
					// TODO: optionally re-add records back to accumulator for retry
				} else {
					logger.Info("✅ Usage reported successfully to metering endpoint")
				}
				
			case <-m.quitChan:
				logger.Info("Token reporting loop shutting down")
				m.meteringRunning = false
				return
			}
		}
	}()
}

// reportUsageToEndpoint sends usage data to the metering URL endpoint via HTTP POST
func (m *Middleware) reportUsageToEndpoint(payload map[string]any) error {
	logger := caddy.Log()

	// Construct the full URL with the endpoint path
	baseURL := strings.TrimRight(m.Config.MeteringURL, "/")
	fullURL := baseURL + "/api/user/report-usage"

	// Marshal the payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal usage payload: %w", err)
	}

	logger.Info("📩  Sending POST request to metering endpoint",
		zap.String("url", fullURL),
		zap.String("payload", string(jsonData)))

	// Create HTTP request
	req, err := http.NewRequest("POST", fullURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Caddy-Secret-Reverse-Proxy/1.0")

	// Send the request
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	
	if logger.Level().Enabled(zap.DebugLevel) {
		LogRequest(req, 1024)
	}
	
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("🔴  failed HTTP request %s: %w", fullURL, err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("🔴  metering endpoint returned non-2xx status: %d %s", resp.StatusCode, resp.Status)
	}

	logger.Info("✅ Successfully reported usage to metering endpoint",
		zap.String("url", fullURL),
		zap.Int("status_code", resp.StatusCode))

	return nil
}
