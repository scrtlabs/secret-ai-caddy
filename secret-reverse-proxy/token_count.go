package secret_reverse_proxy

import (
	"unicode/utf8"
)

// CountTokensHeuristic estimates token count based on input length.
// Rule of thumb: 1 token ≈ 4 characters
func CountTokensHeuristic(text string) int {
	charCount := utf8.RuneCountInString(text)
	if charCount == 0 {
		return 0
	}
	return charCount / 4
}
