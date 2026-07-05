package secret_reverse_proxy

import (
	"strings"
	"testing"
)

// TestLooksLikeJSONObject exercises the body-sniffing helper directly.
func TestLooksLikeJSONObject(t *testing.T) {
	testCases := []struct {
		name     string
		body     string
		expected bool
	}{
		{"starts with brace", `{"model":"llama"}`, true},
		{"leading whitespace before brace", "   \n\t{\"model\":\"llama\"}", true},
		{"json array", "[1,2,3]", false},
		{"plain text", "hello world", false},
		{"empty body", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeJSONObject(tc.body); got != tc.expected {
				t.Errorf("looksLikeJSONObject(%q) = %v, want %v", tc.body, got, tc.expected)
			}
		})
	}
}

// TestDetectModelFromRequestBody_IgnoresContentType verifies that model
// detection is driven by sniffing the body itself rather than trusting the
// client-controlled Content-Type header, so billing cannot be bypassed by
// mislabeling the request.
func TestDetectModelFromRequestBody_IgnoresContentType(t *testing.T) {
	chatBody := `{"model":"llama3","messages":[{"role":"user","content":"hi"}]}`

	testCases := []struct {
		name          string
		requestBody   string
		expectedModel string
	}{
		{
			// Content-Type is no longer consulted at all — detection is driven
			// purely by sniffing the body — so there is nothing left to vary
			// across a text/plain vs. empty vs. application/json header here.
			name:          "chat body detected regardless of content type",
			requestBody:   chatBody,
			expectedModel: "llama3",
		},
		{
			name:          "chat body with leading whitespace before brace",
			requestBody:   "   " + chatBody,
			expectedModel: "llama3",
		},
		{
			name:          "non-JSON body",
			requestBody:   "hello world",
			expectedModel: "unknown",
		},
		{
			name:          "json array body",
			requestBody:   "[1,2,3]",
			expectedModel: "unknown",
		},
		{
			name:          "empty body",
			requestBody:   "",
			expectedModel: "unknown",
		},
		{
			name:          "json object without model field",
			requestBody:   `{"prompt":"hello"}`,
			expectedModel: "unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectModelFromRequestBody(tc.requestBody); got != tc.expectedModel {
				t.Errorf("detectModelFromRequestBody(%q) = %q, want %q", tc.requestBody, got, tc.expectedModel)
			}
		})
	}
}

// TestInjectStreamUsageOption_IgnoresContentType verifies stream_options
// injection is driven by sniffing the body rather than the Content-Type
// header, matching the billing-relevant body-sniffing behavior above.
func TestInjectStreamUsageOption_IgnoresContentType(t *testing.T) {
	body := `{"model":"llama3","stream":true}`

	result := injectStreamUsageOption(body)

	if result == body {
		t.Fatalf("expected stream_options.include_usage to be injected, body unchanged: %q", result)
	}
	if !strings.Contains(result, `"include_usage":true`) {
		t.Errorf("expected injected body to contain include_usage:true, got %q", result)
	}
}

// TestIsInferencePath exercises the known-inference-endpoint matcher used to
// decide whether a model-less POST should be fail-closed rejected.
func TestIsInferencePath(t *testing.T) {
	testCases := []struct {
		name     string
		path     string
		expected bool
	}{
		{"chat completions", "/v1/chat/completions", true},
		{"completions", "/v1/completions", true},
		{"ollama chat", "/api/chat", true},
		{"ollama generate", "/api/generate", true},
		{"base-path prefixed", "/proxy/v1/chat/completions", true},
		{"trailing slash not matched", "/v1/chat/completions/", false},
		{"unrelated path", "/v1/models", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isInferencePath(tc.path); got != tc.expected {
				t.Errorf("isInferencePath(%q) = %v, want %v", tc.path, got, tc.expected)
			}
		})
	}
}
