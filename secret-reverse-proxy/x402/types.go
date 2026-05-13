package x402

import "errors"

// BalanceResponse is the response from the DevPortal agent balance endpoint.
// Balance is a USD float encoded as a string, e.g. "0.02" = $0.02.
type BalanceResponse struct {
	Balance string `json:"balance"`
}

// UserBalanceResponse is the response from the DevPortal /api/user/balance endpoint.
// Status is "success" when a user was found, "not_found" otherwise.
type UserBalanceResponse struct {
	Status string `json:"status"`
	User   *struct {
		Balance float64 `json:"balance"`
	} `json:"user"`
}

// UsageEntry is one item in the usage_data array sent to DevPortal.
type UsageEntry struct {
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	Timestamp    int64  `json:"timestamp"`
	Model        string `json:"model"`
}

// UsageReportRequest is the body sent to /api/user/report-usage.
type UsageReportRequest struct {
	UsageData     []UsageEntry `json:"usage_data"`
	WalletAddress string       `json:"wallet_address"`
}

// Challenge is the 402 response payload returned to agents.
type Challenge struct {
	Error          string `json:"error"`
	BalanceUSD     string `json:"balance_usd"`
	RequiredUSD    string `json:"required_usd"`
	TopupURL       string `json:"topup_url"`
	TopupAmountUSD string `json:"topup_amount_usd"`
}

// Header constants used when calling DevPortal APIs.
const (
	HeaderServiceKey    = "X-Agent-Service-Key"
	HeaderWalletAddress = "X-Agent-Wallet-Address"
)

// Sentinel errors.
var (
	ErrInsufficientBalance = errors.New("insufficient balance")
	ErrPortalUnreachable   = errors.New("portal unreachable")
)
