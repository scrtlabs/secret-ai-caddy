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

// x402 interfaces

// AuthVerifier validates x-agent-* headers.
type AuthVerifier interface {
	IsAgentRequest(r *http.Request) bool
	Verify(r *http.Request, body []byte) (agentAddress string, err error)
}

// QuoteEngine estimates request cost.
type QuoteEngine interface {
	Estimate(model string, inputTokens int, maxOutputTokens int) (*Quote, error)
}

// SpendableLedger manages per-agent balances.
type SpendableLedger interface {
	Credit(agentAddress string, amount int64) error
	Reserve(agentAddress string, amount int64) (reservationID string, err error)
	Commit(reservationID string, actualAmount int64) error
	Release(reservationID string) error
	GetBalance(agentAddress string) (*LedgerEntry, error)
	Snapshot() map[string]LedgerEntry
}

// SettlementEngine finalizes reservations.
type SettlementEngine interface {
	Settle(reservationID string, inputTokens, outputTokens int, model string) (*SettlementResult, error)
	Cancel(reservationID string) error
}

// ChallengeBuilder constructs 402 responses.
type ChallengeBuilder interface {
	Build402Response(w http.ResponseWriter, agentAddress string, requiredAmount int64) error
}

// SecretVMClient communicates with SecretVM agent APIs.
type SecretVMClient interface {
	GetBalance(agentAddress string) (balance int64, err error)
	AddFunds(agentAddress string, amount int64) error
	GetVMStatus(vmID string) (status string, err error)
	ListAgents() ([]string, error)
}

// Reconciler syncs balances from billing backend to ledger.
type Reconciler interface {
	Start(interval time.Duration)
	Stop()
	ForceSync(agentAddress string) error
}

// x402 MetricsCollector extensions

// X402MetricsCollector extends MetricsCollector with x402-specific metrics.
type X402MetricsCollector interface {
	MetricsCollector
	RecordReservation()
	RecordReservationDenied()
	RecordSettlement(estimatedCost, actualCost int64)
	RecordChallenge()
	RecordReconciliation(success bool)
}

// x402 types used in interfaces

// Quote is the cost estimate returned by the QuoteEngine.
type Quote struct {
	EstimatedInputTokens  int
	EstimatedOutputTokens int
	InputCost             int64
	OutputCost            int64
	TotalCost             int64
	Model                 string
	Currency              string
}

// LedgerEntry is the per-agent balance state.
type LedgerEntry struct {
	Balance   int64
	Reserved  int64
	UpdatedAt time.Time
}

// SettlementResult is the outcome of finalizing a reservation.
type SettlementResult struct {
	ReservationID string
	AgentAddress  string
	EstimatedCost int64
	ActualCost    int64
	Refunded      int64
	InputTokens   int
	OutputTokens  int
	Model         string
}