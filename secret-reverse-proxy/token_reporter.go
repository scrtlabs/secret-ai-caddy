package secret_reverse_proxy

import querycontract "github.com/scrtlabs/secret-reverse-proxy/query-contract"

import (
	"time"
	"github.com/caddyserver/caddy/v2"

	"go.uber.org/zap"
)

// StartTokenReportingLoop runs a goroutine to flush usage every interval and call ReportUsageFn.
func (m *Middleware) StartTokenReportingLoop(interval time.Duration) {
	logger := caddy.Log()
	logger.Info("Starting token usage reporting loop", zap.Duration("interval", interval))

	go func() {
		for {
			time.Sleep(interval)

			usage := m.accumulator.FlushUsage()
			if len(usage) == 0 {
				logger.Debug("No usage to report this cycle")
				continue
			}

			records := make([]map[string]any, 0, len(usage))
			for apiKeyHash, usageStats := range usage {
				records = append(records, map[string]any{
					"api_key_hash":  apiKeyHash,
					"input_tokens":  usageStats.InputTokens,
					"output_tokens": usageStats.OutputTokens,
					"timestamp":     usageStats.LastUpdatedAt.Unix(), // Optional
				})
			}

			payload := map[string]any{
				"report_usage": map[string]any{
					"records": records,
				},
			}

			logger.Info("Reporting usage to contract", zap.Int("record_count", len(records)))
			err := querycontract.ReportUsage(m.Config.MeteringContract, payload)
			if err != nil {
				logger.Error("❌ Failed to report usage to smart contract", zap.Error(err))
				// TODO: optionally re-add records back to accumulator for retry
			} else {
				logger.Info("✅ Usage reported successfully to smart contract")
			}
		}
	}()
}
