package metering

import (
	"encoding/json"
	"regexp"
	"strings"
	"unicode/utf8"
	
	"go.uber.org/zap"
	"github.com/caddyserver/caddy/v2"
)

// TokenCounter provides accurate token counting for different content types
type TokenCounter struct {
	logger *zap.Logger
}

func NewTokenCounter() *TokenCounter {
	return &TokenCounter{
		logger: caddy.Log(),
	}
}

// CountTokens intelligently counts tokens based on content type and structure
func (tc *TokenCounter) CountTokens(content string, contentType string) int {
	if content == "" {
		return 0
	}

	// Try to parse as JSON first (most AI APIs use JSON)
	if tokens := tc.countJSONTokens(content); tokens > 0 {
		return tokens
	}

	// Fallback to content-type specific counting
	switch {
	case strings.Contains(contentType, "application/json"):
		return tc.countJSONLikeTokens(content)
	case strings.Contains(contentType, "text/"):
		return tc.countTextTokens(content)
	default:
		return tc.countGenericTokens(content)
	}
}

// countJSONTokens extracts text from common AI API JSON structures
func (tc *TokenCounter) countJSONTokens(content string) int {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return 0 // Not valid JSON
	}

	totalTokens := 0
	
	// Extract text from common AI API fields
	textFields := tc.extractTextFromJSON(data)
	for _, text := range textFields {
		totalTokens += tc.countTextTokens(text)
	}

	return totalTokens
}

// extractTextFromJSON recursively extracts text content from JSON
func (tc *TokenCounter) extractTextFromJSON(data interface{}) []string {
	var texts []string
	
	switch v := data.(type) {
	case map[string]interface{}:
		for key, value := range v {
			// Common AI API text fields
			if tc.isTextualField(key) {
				if str, ok := value.(string); ok && str != "" {
					texts = append(texts, str)
				}
			}
			// Recurse into nested objects
			texts = append(texts, tc.extractTextFromJSON(value)...)
		}
	case []interface{}:
		for _, item := range v {
			texts = append(texts, tc.extractTextFromJSON(item)...)
		}
	case string:
		if v != "" {
			texts = append(texts, v)
		}
	}
	
	return texts
}

// isTextualField identifies fields likely to contain token-countable text
func (tc *TokenCounter) isTextualField(fieldName string) bool {
	textualFields := map[string]bool{
		"prompt":      true,
		"content":     true,
		"message":     true,
		"text":        true,
		"input":       true,
		"query":       true,
		"instruction": true,
		"system":      true,
		"user":        true,
		"assistant":   true,
		"completion":  true,
		"response":    true,
		"output":      true,
		"choices":     true,
	}
	
	return textualFields[strings.ToLower(fieldName)]
}

// countJSONLikeTokens handles malformed JSON that might still contain text
func (tc *TokenCounter) countJSONLikeTokens(content string) int {
	// Extract quoted strings that likely contain text content
	re := regexp.MustCompile(`"([^"\\]*(\\.[^"\\]*)*)"`)
	matches := re.FindAllStringSubmatch(content, -1)
	
	totalTokens := 0
	for _, match := range matches {
		if len(match) > 1 {
			totalTokens += tc.countTextTokens(match[1])
		}
	}
	
	return totalTokens
}

// countTextTokens implements improved token counting for plain text
func (tc *TokenCounter) countTextTokens(text string) int {
	if text == "" {
		return 0
	}
	
	// Clean and normalize text
	cleaned := strings.TrimSpace(text)
	cleaned = regexp.MustCompile(`\s+`).ReplaceAllString(cleaned, " ")
	
	// Use multiple estimation methods and take average
	charBasedTokens := utf8.RuneCountInString(cleaned) / 4
	wordBasedTokens := int(float64(len(strings.Fields(cleaned))) * 1.33)
	
	// Weight towards the more conservative estimate
	averageTokens := (charBasedTokens + wordBasedTokens) / 2
	
	// Apply minimum threshold
	if averageTokens < 1 && len(cleaned) > 0 {
		return 1
	}
	
	return averageTokens
}

// countGenericTokens fallback for unknown content types
func (tc *TokenCounter) countGenericTokens(content string) int {
	return utf8.RuneCountInString(content) / 4
}

// ValidateTokenCount performs sanity checks on token counts
func (tc *TokenCounter) ValidateTokenCount(tokens int, contentLength int) int {
	// Sanity checks
	if tokens < 0 {
		tc.logger.Warn("Negative token count detected", zap.Int("tokens", tokens))
		return 0
	}
	
	// Token count shouldn't exceed content length
	if tokens > contentLength {
		tc.logger.Warn("Token count exceeds content length", 
			zap.Int("tokens", tokens), 
			zap.Int("content_length", contentLength))
		return contentLength / 2 // Conservative fallback
	}
	
	// Extremely high token counts might indicate an error
	if tokens > contentLength*2 {
		tc.logger.Warn("Suspiciously high token count", 
			zap.Int("tokens", tokens), 
			zap.Int("content_length", contentLength))
		return contentLength / 3 // Very conservative fallback
	}
	
	return tokens
}