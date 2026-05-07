package x402

import "errors"

// BalanceResponse is the response from the DevPortal balance endpoint.
type BalanceResponse struct {
	Balance string `json:"balance"` // USDC minor units as string (e.g. "20000" = $0.02)
}

// UsageReport is the request body sent to the DevPortal report-usage endpoint.
type UsageReport struct {
	APIKey       string `json:"api_key"`
	Model        string `json:"model"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
}

// Challenge is the 402 response payload returned to agents.
type Challenge struct {
	Error          string `json:"error"`
	BalanceUSDC    string `json:"balance_usdc"`
	RequiredUSDC   string `json:"required_usdc"`
	TopupURL       string `json:"topup_url"`
	TopupAmountUSDC string `json:"topup_amount_usdc"`
}

// Header constants.
const (
	HeaderServiceKey = "X-Agent-Service-Key"
	HeaderAPIKey     = "X-Api-Key"
)

// Sentinel errors.
var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrPortalUnreachable   = errors.New("portal unreachable")
)
