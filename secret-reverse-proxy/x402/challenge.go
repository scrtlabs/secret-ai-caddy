package x402

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// ChallengeBuilder constructs 402 Payment Required responses.
type ChallengeBuilder struct {
	topupURL string
}

// NewChallengeBuilder creates a new ChallengeBuilder.
func NewChallengeBuilder(topupURL string) *ChallengeBuilder {
	return &ChallengeBuilder{
		topupURL: topupURL,
	}
}

// Build402Response writes a 402 Payment Required response.
// balance and required are USD amounts as float64, e.g. 0.02 = $0.02.
func (cb *ChallengeBuilder) Build402Response(w http.ResponseWriter, balance, required float64) error {
	deficit := required - balance
	if deficit < 0 {
		deficit = 0
	}

	challenge := Challenge{
		Error:          "Insufficient balance",
		BalanceUSD:     formatUSD(balance),
		RequiredUSD:    formatUSD(required),
		TopupURL:       cb.topupURL,
		TopupAmountUSD: formatUSD(deficit),
	}

	body, err := json.Marshal(challenge)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return fmt.Errorf("failed to marshal 402 challenge: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Payment-Required", "x402")
	w.WriteHeader(http.StatusPaymentRequired)
	_, err = w.Write(body)
	return err
}

// formatUSD formats a USD float64 to a 6-decimal string, e.g. 0.02 → "0.020000".
func formatUSD(v float64) string {
	return strconv.FormatFloat(v, 'f', 6, 64)
}
