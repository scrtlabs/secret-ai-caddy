package x402

import (
	"encoding/json"
	"fmt"
	"net/http"
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
// balanceMinor and requiredMinor are in USDC minor units (6 decimals).
func (cb *ChallengeBuilder) Build402Response(w http.ResponseWriter, balanceMinor, requiredMinor int64) error {
	deficit := requiredMinor - balanceMinor
	if deficit < 0 {
		deficit = 0
	}

	challenge := Challenge{
		Error:           "Insufficient balance",
		BalanceUSDC:     minorToUSDC(balanceMinor),
		RequiredUSDC:    minorToUSDC(requiredMinor),
		TopupURL:        cb.topupURL,
		TopupAmountUSDC: minorToUSDC(deficit),
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

// minorToUSDC converts USDC minor units (6 decimals) to a human-readable string.
// e.g. 20000 -> "0.02", 10000 -> "0.01", 0 -> "0.00"
func minorToUSDC(minor int64) string {
	whole := minor / 1_000_000
	frac := minor % 1_000_000
	if frac < 0 {
		frac = -frac
	}
	return fmt.Sprintf("%d.%06d", whole, frac)
}
