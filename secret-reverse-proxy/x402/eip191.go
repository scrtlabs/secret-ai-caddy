package x402

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// VerifyAgentSignature verifies an EIP-191 (personal_sign) signature from an agent request.
// Matches DevPortal's verifySignature.ts exactly:
//
//	payload = method + path + body + timestamp  (no separators, raw concatenation)
//	hash    = sha256(payload)                   (32 bytes)
//	sig     = personal_sign(hash, privateKey)   = ethers.signMessage(hashBytes, key)
//
// Two verification paths are attempted (matching the DevPortal fallback logic):
//  1. Sign the raw hash bytes  (ethers.verifyMessage(hashBytes, sig))
//  2. Sign the hex hash string (ethers.verifyMessage(hashHexString, sig))
func VerifyAgentSignature(walletAddr, signature, method, path, body, timestamp string) (bool, error) {
	payload := method + path + body + timestamp
	hashBytes := sha256.Sum256([]byte(payload))

	sigHex := strings.TrimSpace(strings.TrimPrefix(signature, "0x"))
	sig, err := hex.DecodeString(sigHex)
	if err != nil {
		return false, fmt.Errorf("invalid signature hex: %w", err)
	}
	if len(sig) != 65 {
		return false, fmt.Errorf("invalid signature length %d, expected 65", len(sig))
	}

	// Normalize recovery id: Ethereum encodes v as 27/28, secp256k1 needs 0/1.
	sigNorm := make([]byte, 65)
	copy(sigNorm, sig)
	if sigNorm[64] >= 27 {
		sigNorm[64] -= 27
	}

	expected := common.HexToAddress(walletAddr)

	// Path 1: personal_sign over raw 32 hash bytes.
	// Equivalent to: ethers.verifyMessage(ethers.getBytes("0x"+hashHex), sig)
	personalHash := accounts.TextHash(hashBytes[:])
	if pubKey, err := crypto.SigToPub(personalHash, sigNorm); err == nil {
		if crypto.PubkeyToAddress(*pubKey) == expected {
			return true, nil
		}
	}

	// Path 2: personal_sign over the 64-char hex string.
	// Equivalent to: ethers.verifyMessage(hashHex, sig)  (DevPortal fallback)
	hashHex := hex.EncodeToString(hashBytes[:])
	personalHashFallback := accounts.TextHash([]byte(hashHex))
	if pubKey, err := crypto.SigToPub(personalHashFallback, sigNorm); err == nil {
		if crypto.PubkeyToAddress(*pubKey) == expected {
			return true, nil
		}
	}

	return false, nil
}

// ValidateTimestamp checks that a Unix timestamp string is within the allowed skew window.
// Accepts both second-precision (≤10 digits) and millisecond-precision (>10 digits) timestamps,
// matching the DevPortal agent-auth.ts parseTimestamp logic.
func ValidateTimestamp(timestamp string, maxSkew time.Duration) error {
	ts := strings.TrimSpace(timestamp)
	if ts == "" {
		return fmt.Errorf("missing timestamp")
	}
	for _, c := range ts {
		if c < '0' || c > '9' {
			return fmt.Errorf("non-numeric timestamp")
		}
	}

	var parsed int64
	if _, err := fmt.Sscanf(ts, "%d", &parsed); err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}

	var tsMs int64
	if len(ts) > 10 {
		tsMs = parsed // already milliseconds
	} else {
		tsMs = parsed * 1000 // seconds → ms
	}

	nowMs := time.Now().UnixMilli()
	skewMs := nowMs - tsMs
	if skewMs < 0 {
		skewMs = -skewMs
	}
	if skewMs > maxSkew.Milliseconds() {
		return fmt.Errorf("timestamp skew %dms exceeds allowed %dms", skewMs, maxSkew.Milliseconds())
	}
	return nil
}
