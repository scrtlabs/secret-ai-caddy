package metering

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
	
	"github.com/caddyserver/caddy/v2"
	"go.uber.org/zap"
)

// EnhancedConfig extends the basic Config with additional features
type EnhancedConfig struct {
	*Config
	
	// Rate limiting
	RateLimit RateLimitConfig `json:"rate_limit,omitempty"`
	
	// Token counting
	MaxBodySize        int64 `json:"max_body_size,omitempty"`         // Max body size for token counting
	TokenCountingMode  string `json:"token_counting_mode,omitempty"`   // "accurate", "fast", "disabled"
	
	// Reporting
	FailedReportsDir   string        `json:"failed_reports_dir,omitempty"`
	MaxRetries         int           `json:"max_retries,omitempty"`
	RetryBackoff       time.Duration `json:"retry_backoff,omitempty"`
	
	// Monitoring
	EnableMetrics      bool   `json:"enable_metrics,omitempty"`
	MetricsPath        string `json:"metrics_path,omitempty"`
	
	// Security
	EnableRequestLogging bool `json:"enable_request_logging,omitempty"`
	LogSampleRate       float64 `json:"log_sample_rate,omitempty"` // 0.0-1.0
}

// Metrics tracks operational metrics
type Metrics struct {
	// Request metrics
	TotalRequests      int64 `json:"total_requests"`
	AuthorizedRequests int64 `json:"authorized_requests"`
	RejectedRequests   int64 `json:"rejected_requests"`
	RateLimitedReqs    int64 `json:"rate_limited_requests"`
	
	// Token metrics
	TotalInputTokens   int64 `json:"total_input_tokens"`
	TotalOutputTokens  int64 `json:"total_output_tokens"`
	
	// Performance metrics
	AvgProcessingTime  float64 `json:"avg_processing_time_ms"`
	AvgTokenCountTime  float64 `json:"avg_token_count_time_ms"`
	
	// Error metrics
	ValidationErrors   int64 `json:"validation_errors"`
	TokenCountErrors   int64 `json:"token_count_errors"`
	ReportingErrors    int64 `json:"reporting_errors"`
	
	// Cache metrics
	CacheHits          int64 `json:"cache_hits"`
	CacheMisses        int64 `json:"cache_misses"`
	
	// Reporting metrics
	SuccessfulReports  int64 `json:"successful_reports"`
	FailedReports      int64 `json:"failed_reports"`
	PendingReports     int64 `json:"pending_reports"`
	
	// Last update timestamp
	LastUpdated        time.Time `json:"last_updated"`
}

// MetricsCollector handles metrics collection and reporting
type MetricsCollector struct {
	metrics *Metrics
	logger  *zap.Logger
	config  *EnhancedConfig
}

func NewMetricsCollector(config *EnhancedConfig) *MetricsCollector {
	return &MetricsCollector{
		metrics: &Metrics{
			LastUpdated: time.Now(),
		},
		logger: caddy.Log(),
		config: config,
	}
}

// Request tracking methods
func (mc *MetricsCollector) RecordRequest() {
	atomic.AddInt64(&mc.metrics.TotalRequests, 1)
}

func (mc *MetricsCollector) RecordAuthorized() {
	atomic.AddInt64(&mc.metrics.AuthorizedRequests, 1)
}

func (mc *MetricsCollector) RecordRejected() {
	atomic.AddInt64(&mc.metrics.RejectedRequests, 1)
}

func (mc *MetricsCollector) RecordRateLimited() {
	atomic.AddInt64(&mc.metrics.RateLimitedReqs, 1)
}

// Token tracking methods
func (mc *MetricsCollector) RecordTokens(inputTokens, outputTokens int64) {
	atomic.AddInt64(&mc.metrics.TotalInputTokens, inputTokens)
	atomic.AddInt64(&mc.metrics.TotalOutputTokens, outputTokens)
}

// Performance tracking methods
func (mc *MetricsCollector) RecordProcessingTime(duration time.Duration) {
	// Simple moving average (could be enhanced with proper statistics)
	currentAvg := mc.metrics.AvgProcessingTime
	newValue := float64(duration.Nanoseconds()) / 1000000 // Convert to milliseconds
	mc.metrics.AvgProcessingTime = (currentAvg + newValue) / 2
}

func (mc *MetricsCollector) RecordTokenCountTime(duration time.Duration) {
	currentAvg := mc.metrics.AvgTokenCountTime
	newValue := float64(duration.Nanoseconds()) / 1000000
	mc.metrics.AvgTokenCountTime = (currentAvg + newValue) / 2
}

// Error tracking methods
func (mc *MetricsCollector) RecordValidationError() {
	atomic.AddInt64(&mc.metrics.ValidationErrors, 1)
}

func (mc *MetricsCollector) RecordTokenCountError() {
	atomic.AddInt64(&mc.metrics.TokenCountErrors, 1)
}

func (mc *MetricsCollector) RecordReportingError() {
	atomic.AddInt64(&mc.metrics.ReportingErrors, 1)
}

// Cache tracking methods
func (mc *MetricsCollector) RecordCacheHit() {
	atomic.AddInt64(&mc.metrics.CacheHits, 1)
}

func (mc *MetricsCollector) RecordCacheMiss() {
	atomic.AddInt64(&mc.metrics.CacheMisses, 1)
}

// Reporting tracking methods
func (mc *MetricsCollector) RecordSuccessfulReport() {
	atomic.AddInt64(&mc.metrics.SuccessfulReports, 1)
}

func (mc *MetricsCollector) RecordFailedReport() {
	atomic.AddInt64(&mc.metrics.FailedReports, 1)
}

func (mc *MetricsCollector) UpdatePendingReports(count int64) {
	atomic.StoreInt64(&mc.metrics.PendingReports, count)
}

// GetMetrics returns a copy of current metrics
func (mc *MetricsCollector) GetMetrics() Metrics {
	mc.metrics.LastUpdated = time.Now()
	return *mc.metrics
}

// ServeMetrics provides an HTTP endpoint for metrics
func (mc *MetricsCollector) ServeMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	
	metrics := mc.GetMetrics()
	
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		mc.logger.Error("Failed to encode metrics", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
}

// Enhanced middleware with comprehensive monitoring
type EnhancedMiddleware struct {
	*Middleware
	enhancedConfig    *EnhancedConfig
	metricsCollector  *MetricsCollector
	rateLimiter       *TokenRateLimiter
	bodyHandler       *BodyHandler
	tokenCounter      *TokenCounter
	resilientReporter *ResilientReporter
	accumulator       *TokenAccumulator // Local accumulator reference
}

func NewEnhancedMiddleware() *EnhancedMiddleware {
	return &EnhancedMiddleware{}
}

func (em *EnhancedMiddleware) Provision(ctx caddy.Context) error {
	// Call base provision first
	if err := em.Middleware.Provision(ctx); err != nil {
		return err
	}
	
	// Initialize enhanced components
	if em.enhancedConfig == nil {
		em.enhancedConfig = &EnhancedConfig{
			Config:            em.Config,
			MaxBodySize:       1024 * 1024, // 1MB default
			TokenCountingMode: "fast",
			MaxRetries:        3,
			RetryBackoff:      5 * time.Minute,
			EnableMetrics:     true,
			MetricsPath:       "/metrics",
			LogSampleRate:     0.1, // 10% sampling
		}
	}
	
	em.metricsCollector = NewMetricsCollector(em.enhancedConfig)
	em.rateLimiter = NewTokenRateLimiter(em.enhancedConfig.RateLimit)
	em.bodyHandler = NewBodyHandler(em.enhancedConfig.MaxBodySize)
	em.tokenCounter = NewTokenCounter()
	
	// Initialize token accumulator
	if em.accumulator == nil {
		em.accumulator = NewTokenAccumulator()
	}
	em.resilientReporter = NewResilientReporter(em.Config, em.accumulator)
	
	// Start background services
	em.rateLimiter.StartCleanupLoop()
	em.resilientReporter.StartReportingLoop(em.Config.MeteringInterval)
	
	// Register metrics endpoint if enabled
	if em.enhancedConfig.EnableMetrics && em.enhancedConfig.MetricsPath != "" {
		// This would need integration with Caddy's HTTP handling
		em.registerMetricsEndpoint()
	}
	
	return nil
}

func (em *EnhancedMiddleware) registerMetricsEndpoint() {
	// Implementation would depend on Caddy's plugin architecture
	// This is a placeholder for the concept
	logger := caddy.Log()
	logger.Info("Metrics endpoint would be registered", 
		zap.String("path", em.enhancedConfig.MetricsPath))
}

// GetHealthStatus returns the health status of the middleware
func (em *EnhancedMiddleware) GetHealthStatus() map[string]interface{} {
	metrics := em.metricsCollector.GetMetrics()
	
	// Calculate health indicators
	totalRequests := metrics.TotalRequests
	errorRate := float64(0)
	if totalRequests > 0 {
		errors := metrics.ValidationErrors + metrics.TokenCountErrors + metrics.ReportingErrors
		errorRate = float64(errors) / float64(totalRequests)
	}
	
	cacheHitRate := float64(0)
	if metrics.CacheHits+metrics.CacheMisses > 0 {
		cacheHitRate = float64(metrics.CacheHits) / float64(metrics.CacheHits+metrics.CacheMisses)
	}
	
	health := "healthy"
	if errorRate > 0.05 { // More than 5% error rate
		health = "degraded"
	}
	if errorRate > 0.20 { // More than 20% error rate
		health = "unhealthy"
	}
	
	return map[string]interface{}{
		"status":              health,
		"error_rate":          errorRate,
		"cache_hit_rate":      cacheHitRate,
		"pending_reports":     metrics.PendingReports,
		"avg_processing_ms":   metrics.AvgProcessingTime,
		"total_requests":      totalRequests,
		"last_updated":        metrics.LastUpdated,
	}
}