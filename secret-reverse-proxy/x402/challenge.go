package x402

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/rs/xid"
)

// ChallengeBuilderImpl constructs 402 Payment Required responses.
type ChallengeBuilderImpl struct {
	paymentURL string
	currency   string
}

// NewChallengeBuilder creates a new ChallengeBuilder.
func NewChallengeBuilder(paymentURL, currency string) *ChallengeBuilderImpl {
	return &ChallengeBuilderImpl{
		paymentURL: paymentURL,
		currency:   currency,
	}
}

// Build402Response writes a 402 Payment Required response to the writer.
func (cb *ChallengeBuilderImpl) Build402Response(w http.ResponseWriter, agentAddress string, requiredAmount int64) error {
	challenge := Challenge{
		AgentAddress:   agentAddress,
		RequiredAmount: requiredAmount,
		Currency:       cb.currency,
		PaymentURL:     cb.paymentURL,
		ChallengeRef:   xid.New().String(),
		Message:        fmt.Sprintf("Insufficient balance. Please add at least %d %s to continue.", requiredAmount, cb.currency),
	}

	body, err := json.Marshal(challenge)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return fmt.Errorf("failed to marshal 402 challenge: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Payment-Required", "true")
	w.WriteHeader(http.StatusPaymentRequired)
	_, err = w.Write(body)
	return err
}
