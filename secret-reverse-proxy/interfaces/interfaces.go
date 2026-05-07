package interfaces

import (
	"net/http"
	"time"
)

// TokenCounter provides intelligent token counting functionality
type TokenCounter interface {
	CountTokens(content, contentType string) int
	CountTokensWithModel(content, contentType, model string) int
	ValidateTokenCount(tokens int, contentLength int) int
}

// RequestBodyInfo contains extracted information from request body
type RequestBodyInfo struct {
	Content      string
	Size         int64
	ContentType  string
	IsComplete   bool
	IsTruncated  bool
	ParsedJSON   map[string]any
	ExtractedText []string
	Error        error
}

// ResponseBodyInfo contains extracted information from response body
type ResponseBodyInfo struct {
	Content     string
	Size        int64
	IsComplete  bool
	IsTruncated bool
	StatusCode  int
	ParsedJSON  map[string]any
	ExtractedText []string
}

// BodyHandler provides safe request/response body handling
type BodyHandler interface {
	SafeReadRequestBody(r *http.Request) (*RequestBodyInfo, error)
	GetContentType(r *http.Request) string
	IsTokenCountableContent(contentType string) bool
	ValidateRequestSize(r *http.Request) error
}

// EnhancedResponseWriter provides advanced response writing capabilities
type EnhancedResponseWriter interface {
	http.ResponseWriter
	Status() int
	Body() []byte
	IsComplete() bool
	IsTruncated() bool
	GetResponseInfo() *ResponseBodyInfo
}

// TokenUsage stores cumulative token stats for a single API key
type TokenUsage struct {
	InputTokens   int
	OutputTokens  int
	LastUpdatedAt time.Time
}

// TokenAccumulator tracks usage per hashed API key
type TokenAccumulator interface {
	RecordUsage(apiKeyHash string, inputTokens, outputTokens int)
	FlushUsage() map[string]TokenUsage
	PeekUsage() map[string]TokenUsage
}

// ResilientReporter provides robust usage reporting with retry logic
type ResilientReporter interface {
	StartReportingLoop(interval time.Duration)
	GetFailedReportsCount() int
}

// MetricsCollector handles comprehensive metrics collection
type MetricsCollector interface {
	// Request tracking
	RecordRequest()
	RecordAuthorized()
	RecordRejected()
	RecordRateLimited()
	
	// Token tracking
	RecordTokens(inputTokens, outputTokens int64)
	
	// Performance tracking
	RecordProcessingTime(duration time.Duration)
	RecordTokenCountTime(duration time.Duration)
	
	// Error tracking
	RecordValidationError()
	RecordTokenCountError()
	RecordReportingError()
	
	// Cache tracking
	RecordCacheHit()
	RecordCacheMiss()
	
	// Reporting tracking
	RecordSuccessfulReport()
	RecordFailedReport()
	UpdatePendingReports(count int64)
	
	// Metrics retrieval
	GetMetrics() map[string]any
	ServeMetrics(w http.ResponseWriter, r *http.Request)
}

// Config interface for configuration access (to avoid importing main package)
type Config interface {
	GetMeteringURL() string
	GetMeteringInterval() time.Duration
	GetMaxBodySize() int64
	GetTokenCountingMode() string
	GetMaxRetries() int
	GetRetryBackoff() time.Duration
	IsMetricsEnabled() bool
	GetMetricsPath() string
}

// x402 interfaces (portal-based)

// PortalClient communicates with DevPortal for balance checks and usage reporting.
type PortalClient interface {
	GetBalance(apiKey string) (balance int64, err error)
	ReportUsage(apiKey, model string, inputTokens, outputTokens int) error
}

// ChallengeBuilder constructs 402 responses.
type ChallengeBuilder interface {
	Build402Response(w http.ResponseWriter, balanceMinor, requiredMinor int64) error
}