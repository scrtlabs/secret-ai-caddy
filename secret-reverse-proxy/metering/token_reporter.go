package metering

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	
	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

type ResilientReporter struct {
	config           *Config
	accumulator      *TokenAccumulator
	logger           *zap.Logger
	failedReportsDir string
	maxRetries       int
	retryBackoff     time.Duration
}

type FailedReport struct {
	Timestamp time.Time              `json:"timestamp"`
	Records   []map[string]any       `json:"records"`
	Retries   int                    `json:"retries"`
}

func NewResilientReporter(config *Config, accumulator *TokenAccumulator) *ResilientReporter {
	return &ResilientReporter{
		config:           config,
		accumulator:      accumulator,
		logger:           caddy.Log(),
		failedReportsDir: "/tmp/caddy-failed-reports", // Make configurable
		maxRetries:       3,
		retryBackoff:     time.Minute * 5,
	}
}

func (rr *ResilientReporter) StartReportingLoop(interval time.Duration) {
	// Ensure failed reports directory exists
	if err := os.MkdirAll(rr.failedReportsDir, 0755); err != nil {
		rr.logger.Error("Failed to create failed reports directory", zap.Error(err))
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		
		// Process any failed reports on startup
		rr.retryFailedReports()
		
		for range ticker.C {
			rr.processCurrentUsage()
			rr.retryFailedReports()
		}
	}()
}

func (rr *ResilientReporter) processCurrentUsage() {
	usage := rr.accumulator.FlushUsage()
	if len(usage) == 0 {
		rr.logger.Debug("No usage to report this cycle")
		return
	}

	records := rr.buildRecords(usage)
	if err := rr.submitWithRetry(records, 0); err != nil {
		rr.logger.Error("Failed to submit usage after retries, persisting to disk", zap.Error(err))
		rr.persistFailedReport(records)
	}
}

func (rr *ResilientReporter) buildRecords(usage map[string]TokenUsage) []map[string]any {
	records := make([]map[string]any, 0, len(usage))
	for apiKeyHash, usageStats := range usage {
		records = append(records, map[string]any{
			"api_key_hash":  apiKeyHash,
			"input_tokens":  usageStats.InputTokens,
			"output_tokens": usageStats.OutputTokens,
			"timestamp":     usageStats.LastUpdatedAt.Unix(),
		})
	}
	return records
}

func (rr *ResilientReporter) submitWithRetry(records []map[string]any, attempt int) error {
	if len(records) == 0 {
		return nil
	}

	// Convert records to the expected format for the metering endpoint
	usageData := make(map[string]map[string]any)
	for _, record := range records {
		if apiKeyHash, ok := record["api_key_hash"].(string); ok {
			usageData[apiKeyHash] = map[string]any{
				"input_tokens":  record["input_tokens"],
				"output_tokens": record["output_tokens"],
				"timestamp":     record["timestamp"],
			}
		}
	}

	payload := map[string]any{
		"usage_data": usageData,
	}

	// Construct the full URL with the endpoint path
	baseURL := strings.TrimRight(rr.config.MeteringURL, "/")
	fullURL := baseURL + "/api/user/report-usage"

	// Marshal the payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal usage payload: %w", err)
	}

	rr.logger.Info("📩  Sending POST request to metering endpoint",
		zap.String("url", fullURL),
		zap.String("payload", string(jsonData)),
		zap.Int("attempt", attempt+1))

	// Create HTTP request
	req, err := http.NewRequest("POST", fullURL, bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Caddy-Secret-Reverse-Proxy-Enhanced/1.0")

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		if attempt < rr.maxRetries-1 {
			rr.logger.Warn("HTTP request failed, will retry", 
				zap.Error(err), 
				zap.Int("attempt", attempt+1),
				zap.Int("max_retries", rr.maxRetries))
			time.Sleep(rr.retryBackoff * time.Duration(attempt+1)) // Exponential backoff
			return rr.submitWithRetry(records, attempt+1)
		}
		return fmt.Errorf("🔴  failed HTTP request %s after %d attempts: %w", fullURL, attempt+1, err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if attempt < rr.maxRetries-1 {
			rr.logger.Warn("Metering endpoint returned non-2xx status, will retry", 
				zap.Int("status_code", resp.StatusCode),
				zap.String("status", resp.Status),
				zap.Int("attempt", attempt+1))
			time.Sleep(rr.retryBackoff * time.Duration(attempt+1)) // Exponential backoff
			return rr.submitWithRetry(records, attempt+1)
		}
		return fmt.Errorf("🔴  metering endpoint returned non-2xx status after %d attempts: %d %s", 
			attempt+1, resp.StatusCode, resp.Status)
	}

	rr.logger.Info("✅ Successfully reported usage to metering endpoint",
		zap.String("url", fullURL),
		zap.Int("status_code", resp.StatusCode),
		zap.Int("records", len(records)))

	return nil
}

func (rr *ResilientReporter) persistFailedReport(records []map[string]any) {
	failedReport := FailedReport{
		Timestamp: time.Now(),
		Records:   records,
		Retries:   0,
	}

	data, err := json.Marshal(failedReport)
	if err != nil {
		rr.logger.Error("Failed to marshal failed report", zap.Error(err))
		return
	}

	filename := fmt.Sprintf("failed_report_%d.json", time.Now().Unix())
	filepath := filepath.Join(rr.failedReportsDir, filename)

	if err := ioutil.WriteFile(filepath, data, 0644); err != nil {
		rr.logger.Error("Failed to persist failed report", zap.Error(err))
		return
	}

	rr.logger.Info("Persisted failed report", 
		zap.String("file", filepath),
		zap.Int("records", len(records)))
}

func (rr *ResilientReporter) retryFailedReports() {
	files, err := ioutil.ReadDir(rr.failedReportsDir)
	if err != nil {
		rr.logger.Error("Failed to read failed reports directory", zap.Error(err))
		return
	}

	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			rr.retryFailedReport(filepath.Join(rr.failedReportsDir, file.Name()))
		}
	}
}

func (rr *ResilientReporter) retryFailedReport(filepath string) {
	data, err := ioutil.ReadFile(filepath)
	if err != nil {
		rr.logger.Error("Failed to read failed report", zap.String("file", filepath), zap.Error(err))
		return
	}

	var failedReport FailedReport
	if err := json.Unmarshal(data, &failedReport); err != nil {
		rr.logger.Error("Failed to unmarshal failed report", zap.String("file", filepath), zap.Error(err))
		return
	}

	// Check if report is too old (older than 24 hours)
	if time.Since(failedReport.Timestamp) > 24*time.Hour {
		rr.logger.Warn("Discarding old failed report", 
			zap.String("file", filepath),
			zap.Time("timestamp", failedReport.Timestamp))
		os.Remove(filepath)
		return
	}

	// Check if we've exceeded max retries
	if failedReport.Retries >= rr.maxRetries {
		rr.logger.Warn("Failed report exceeded max retries, discarding", 
			zap.String("file", filepath),
			zap.Int("retries", failedReport.Retries))
		os.Remove(filepath)
		return
	}

	rr.logger.Info("Retrying failed report", 
		zap.String("file", filepath),
		zap.Int("retry_count", failedReport.Retries+1))

	if err := rr.submitWithRetry(failedReport.Records, 0); err != nil {
		// Update retry count and persist again
		failedReport.Retries++
		if data, err := json.Marshal(failedReport); err == nil {
			ioutil.WriteFile(filepath, data, 0644)
		}
		rr.logger.Error("Failed to retry report", zap.String("file", filepath), zap.Error(err))
	} else {
		// Success - remove the file
		os.Remove(filepath)
		rr.logger.Info("Successfully retried failed report", zap.String("file", filepath))
	}
}

// GetFailedReportsCount returns the number of failed reports waiting for retry
func (rr *ResilientReporter) GetFailedReportsCount() int {
	files, err := ioutil.ReadDir(rr.failedReportsDir)
	if err != nil {
		return 0
	}
	
	count := 0
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".json" {
			count++
		}
	}
	return count
}