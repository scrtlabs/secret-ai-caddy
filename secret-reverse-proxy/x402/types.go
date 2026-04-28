package x402

import (
	"errors"
	"time"
)

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

// Reservation tracks an in-flight spend hold.
type Reservation struct {
	ID           string
	AgentAddress string
	Amount       int64
	CreatedAt    time.Time
	Model        string
}

// LedgerEntry is the per-agent balance state.
type LedgerEntry struct {
	Balance   int64     // available funds (not including reserved amounts)
	Reserved  int64     // sum of outstanding reservations (read-only, for observability)
	UpdatedAt time.Time
}

// Challenge is the 402 response payload.
type Challenge struct {
	AgentAddress   string `json:"agent_address"`
	RequiredAmount int64  `json:"required_amount"`
	Currency       string `json:"currency"`
	PaymentURL     string `json:"payment_url"`
	ChallengeRef   string `json:"challenge_ref"`
	Message        string `json:"message"`
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

// ModelPricing holds per-model cost rates.
type ModelPricing struct {
	InputCostPer1kTokens  int64 `json:"input_cost_per_1k_tokens"`
	OutputCostPer1kTokens int64 `json:"output_cost_per_1k_tokens"`
}

// PricingConfig is the top-level pricing file structure.
type PricingConfig struct {
	Default ModelPricing            `json:"default"`
	Models  map[string]ModelPricing `json:"models"`
}

// Sentinel errors.
var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrReservationNotFound = errors.New("reservation not found")
	ErrReservationExpired  = errors.New("reservation expired")
	ErrInvalidSignature    = errors.New("invalid agent signature")
	ErrStaleTimestamp      = errors.New("timestamp outside allowed skew")
	ErrUnknownAgent        = errors.New("unknown agent address")
)

// Header constants for agent authentication.
const (
	HeaderAgentAddress   = "X-Agent-Address"
	HeaderAgentSignature = "X-Agent-Signature"
	HeaderAgentTimestamp  = "X-Agent-Timestamp"
)
