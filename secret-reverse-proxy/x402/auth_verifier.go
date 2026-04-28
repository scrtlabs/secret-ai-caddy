package x402

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// authVerifierImpl validates x-agent-* headers on incoming requests.
type AuthVerifierImpl struct {
	agentKey      []byte
	timestampSkew time.Duration
	logger        *zap.Logger
}

// NewAuthVerifier creates a new AuthVerifier with the given agent key and timestamp skew.
func NewAuthVerifier(agentKeyHex string, timestampSkew time.Duration, logger *zap.Logger) *AuthVerifierImpl {
	return &AuthVerifierImpl{
		agentKey:      []byte(agentKeyHex),
		timestampSkew: timestampSkew,
		logger:        logger,
	}
}

// IsAgentRequest returns true if the request carries x-agent-* headers.
func (v *AuthVerifierImpl) IsAgentRequest(r *http.Request) bool {
	return r.Header.Get(HeaderAgentAddress) != ""
}

// Verify validates the agent signature and returns the agent address.
func (v *AuthVerifierImpl) Verify(r *http.Request, body []byte) (string, error) {
	agentAddress := r.Header.Get(HeaderAgentAddress)
	signature := r.Header.Get(HeaderAgentSignature)
	timestamp := r.Header.Get(HeaderAgentTimestamp)

	if agentAddress == "" || signature == "" || timestamp == "" {
		return "", fmt.Errorf("%w: missing required x-agent-* headers", ErrInvalidSignature)
	}

	// Parse and validate timestamp
	ts, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return "", fmt.Errorf("%w: invalid timestamp format: %v", ErrStaleTimestamp, err)
	}

	drift := time.Since(ts)
	if math.Abs(float64(drift)) > float64(v.timestampSkew) {
		return "", fmt.Errorf("%w: drift=%v, max=%v", ErrStaleTimestamp, drift, v.timestampSkew)
	}

	// Reconstruct canonical signing payload:
	// METHOD + "\n" + PATH_ONLY + "\n" + BODY + "\n" + TIMESTAMP
	canonical := r.Method + "\n" + r.URL.Path + "\n" + string(body) + "\n" + timestamp

	// Compute HMAC-SHA256
	mac := hmac.New(sha256.New, v.agentKey)
	mac.Write([]byte(canonical))
	expectedSig := hex.EncodeToString(mac.Sum(nil))

	// Constant-time comparison
	sigBytes, err := hex.DecodeString(signature)
	if err != nil {
		return "", fmt.Errorf("%w: invalid signature encoding", ErrInvalidSignature)
	}
	expectedBytes, _ := hex.DecodeString(expectedSig)

	if !hmac.Equal(sigBytes, expectedBytes) {
		v.logger.Debug("Signature verification failed",
			zap.String("agent", agentAddress),
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path))
		return "", ErrInvalidSignature
	}

	return agentAddress, nil
}
