package secret_reverse_proxy

import (
	"bytes"
	"net/http"
	"testing"
	
	"github.com/scrtlabs/secret-reverse-proxy/factories"
)

func TestEnhancedTokenCounting(t *testing.T) {
	// Test TokenCounter functionality
	tokenCounter := factories.CreateTokenCounter()
	if tokenCounter == nil {
		t.Fatal("TokenCounter should not be nil")
	}

	// Test basic token counting
	content := `{"messages": [{"role": "user", "content": "Hello, world!"}]}`
	tokens := tokenCounter.CountTokens(content, "application/json")
	
	if tokens <= 0 {
		t.Errorf("Expected positive token count, got: %d", tokens)
	}
	
	// Test token validation
	validatedTokens := tokenCounter.ValidateTokenCount(tokens, len(content))
	if validatedTokens != tokens {
		t.Errorf("Expected validated tokens %d to equal original tokens %d", validatedTokens, tokens)
	}
	
	t.Logf("✅ Token counting test passed: %d tokens for content length %d", tokens, len(content))
}

func TestEnhancedBodyHandler(t *testing.T) {
	// Test BodyHandler functionality
	bodyHandler := factories.CreateBodyHandler(1024 * 1024) // 1MB
	if bodyHandler == nil {
		t.Fatal("BodyHandler should not be nil")
	}

	// Create a test request with JSON body
	jsonBody := `{"test": "message", "data": {"key": "value"}}`
	req, err := http.NewRequest("POST", "/test", bytes.NewBufferString(jsonBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Test content type detection
	contentType := bodyHandler.GetContentType(req)
	if contentType != "application/json" {
		t.Errorf("Expected content type 'application/json', got: %s", contentType)
	}

	// Test if content is token countable
	isCountable := bodyHandler.IsTokenCountableContent(contentType)
	if !isCountable {
		t.Errorf("Expected JSON content to be token countable")
	}

	// Test request size validation
	err = bodyHandler.ValidateRequestSize(req)
	if err != nil {
		t.Errorf("Expected request size to be valid, got error: %v", err)
	}

	// Test safe body reading
	bodyInfo, err := bodyHandler.SafeReadRequestBody(req)
	if err != nil {
		t.Errorf("Failed to read request body: %v", err)
	}

	if bodyInfo == nil {
		t.Fatal("BodyInfo should not be nil")
	}

	if bodyInfo.Content != jsonBody {
		t.Errorf("Expected body content '%s', got: '%s'", jsonBody, bodyInfo.Content)
	}

	if bodyInfo.ContentType != "application/json" {
		t.Errorf("Expected content type 'application/json', got: %s", bodyInfo.ContentType)
	}

	if !bodyInfo.IsComplete {
		t.Errorf("Expected body to be complete")
	}

	if len(bodyInfo.ParsedJSON) == 0 {
		t.Errorf("Expected parsed JSON to be populated")
	}

	t.Logf("✅ Body handler test passed: read %d bytes of %s content", bodyInfo.Size, bodyInfo.ContentType)
}

func TestEnhancedTokenCountingIntegration(t *testing.T) {
	// Test integration between TokenCounter and BodyHandler
	tokenCounter := factories.CreateTokenCounter()
	bodyHandler := factories.CreateBodyHandler(1024 * 1024)
	
	if tokenCounter == nil || bodyHandler == nil {
		t.Fatal("Enhanced components should not be nil")
	}

	// Create a test request with a realistic AI prompt
	aiPrompt := `{
		"model": "gpt-3.5-turbo",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "Please explain the concept of machine learning in simple terms."}
		],
		"max_tokens": 150,
		"temperature": 0.7
	}`

	req, err := http.NewRequest("POST", "/v1/chat/completions", bytes.NewBufferString(aiPrompt))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Read body using enhanced handler
	bodyInfo, err := bodyHandler.SafeReadRequestBody(req)
	if err != nil {
		t.Fatalf("Failed to read request body: %v", err)
	}

	// Count tokens using enhanced counter
	tokens := tokenCounter.CountTokens(bodyInfo.Content, bodyInfo.ContentType)
	
	if tokens <= 0 {
		t.Errorf("Expected positive token count, got: %d", tokens)
	}

	// Verify that enhanced counting gives reasonable results
	// For this content, we should expect roughly 50-100 tokens
	if tokens < 30 || tokens > 200 {
		t.Logf("⚠️  Token count seems unusual: %d tokens (expected roughly 30-200)", tokens)
	}

	t.Logf("✅ Integration test passed: %d input tokens counted from %d byte request", tokens, bodyInfo.Size)
	
	// Test with various content types
	testCases := []struct {
		name        string
		content     string
		contentType string
		expectTokens bool
	}{
		{"JSON API request", `{"query": "test"}`, "application/json", true},
		{"Plain text", "Hello world", "text/plain", true},
		{"Binary data", "\x00\x01\x02", "application/octet-stream", false},
		{"Empty content", "", "application/json", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tokens := tokenCounter.CountTokens(tc.content, tc.contentType)
			isCountable := bodyHandler.IsTokenCountableContent(tc.contentType)
			
			if tc.expectTokens && tokens == 0 && len(tc.content) > 0 {
				t.Errorf("Expected tokens > 0 for countable content, got: %d", tokens)
			}
			
			if tc.expectTokens != isCountable {
				t.Errorf("Expected countable=%v, got: %v", tc.expectTokens, isCountable)
			}
			
			t.Logf("Content type %s: %d tokens (countable=%v)", tc.contentType, tokens, isCountable)
		})
	}
}