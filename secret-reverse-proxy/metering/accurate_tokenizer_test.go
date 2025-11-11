package metering

import (
	"testing"
)

func TestAccurateTokenizer_FallbackCount(t *testing.T) {
	tokenizer, err := NewAccurateTokenizer("/tmp/test_tokenizers")
	if err != nil {
		t.Fatalf("Failed to create tokenizer: %v", err)
	}

	tests := []struct {
		name     string
		text     string
		model    string
		wantMin  int
		wantMax  int
	}{
		{
			name:    "Simple text with unknown model",
			text:    "Hello, world!",
			model:   "unknown-model",
			wantMin: 3,  // chars/4 ≈ 13/4 = 3
			wantMax: 4,
		},
		{
			name:    "Empty text",
			text:    "",
			model:   "unknown-model",
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "Longer text",
			text:    "This is a longer text to test token counting with fallback",
			model:   "unknown-model",
			wantMin: 14, // chars/4 ≈ 59/4 = 14
			wantMax: 15,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens, err := tokenizer.CountTokens(tt.text, tt.model)
			if err != nil {
				t.Errorf("CountTokens() error = %v", err)
				return
			}

			if tokens < tt.wantMin || tokens > tt.wantMax {
				t.Errorf("CountTokens() = %v, want between %v and %v", tokens, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestAccurateTokenizer_ModelNormalization(t *testing.T) {
	tokenizer, err := NewAccurateTokenizer("/tmp/test_tokenizers")
	if err != nil {
		t.Fatalf("Failed to create tokenizer: %v", err)
	}

	tests := []struct {
		name           string
		inputModel     string
		expectedNorm   string
	}{
		{
			name:         "Llama with port notation",
			inputModel:   "llama3.3:70b",
			expectedNorm: "llama3.3",
		},
		{
			name:         "Mistral simple",
			inputModel:   "mistral",
			expectedNorm: "mistral",
		},
		{
			name:         "Llama-2 variant",
			inputModel:   "llama-2-7b-chat",
			expectedNorm: "llama-2",
		},
		{
			name:         "Mixed case",
			inputModel:   "LLAMA-2",
			expectedNorm: "llama-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalized := tokenizer.normalizeModelName(tt.inputModel)
			if normalized != tt.expectedNorm {
				t.Errorf("normalizeModelName(%v) = %v, want %v", tt.inputModel, normalized, tt.expectedNorm)
			}
		})
	}
}

func TestAccurateTokenizer_CachedModels(t *testing.T) {
	tokenizer, err := NewAccurateTokenizer("/tmp/test_tokenizers")
	if err != nil {
		t.Fatalf("Failed to create tokenizer: %v", err)
	}

	cached := tokenizer.GetCachedModels()
	if cached["total"] != 0 {
		t.Errorf("Expected 0 cached models initially, got %v", cached["total"])
	}

	// Count tokens for an unknown model (should use fallback, not cache a tokenizer)
	_, _ = tokenizer.CountTokens("test", "unknown-model")

	cached = tokenizer.GetCachedModels()
	if cached["total"] != 0 {
		t.Errorf("Expected 0 cached models after fallback, got %v", cached["total"])
	}
}

func TestTokenCounter_CountTokensWithModel(t *testing.T) {
	counter := NewTokenCounter()

	tests := []struct {
		name        string
		content     string
		contentType string
		model       string
		wantMin     int
		wantMax     int
	}{
		{
			name:        "Simple text with unknown model",
			content:     "Hello, world!",
			contentType: "text/plain",
			model:       "unknown",
			wantMin:     3,
			wantMax:     4,
		},
		{
			name:        "JSON content with unknown model",
			content:     `{"prompt": "Hello, world!"}`,
			contentType: "application/json",
			model:       "unknown",
			wantMin:     3,
			wantMax:     8,
		},
		{
			name:        "Empty content",
			content:     "",
			contentType: "text/plain",
			model:       "unknown",
			wantMin:     0,
			wantMax:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := counter.CountTokensWithModel(tt.content, tt.contentType, tt.model)

			if tokens < tt.wantMin || tokens > tt.wantMax {
				t.Errorf("CountTokensWithModel() = %v, want between %v and %v", tokens, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestTokenCounter_ImprovedFallbackCount(t *testing.T) {
	counter := NewTokenCounter()

	tests := []struct {
		name        string
		content     string
		contentType string
		oldApprox   int  // Old heuristic would give approximately this
		newMax      int  // New method should give less than this
	}{
		{
			name:        "Simple text",
			content:     "Hello, world!",  // 13 chars
			contentType: "text/plain",
			oldApprox:   7,  // (13/4 + 2*1.33)/2 ≈ 7
			newMax:      4,  // 13/4 = 3 (should be less than old)
		},
		{
			name:        "Longer text",
			content:     "This is a test of the emergency broadcast system",  // 49 chars, 9 words
			contentType: "text/plain",
			oldApprox:   12, // (49/4 + 9*1.33)/2 ≈ 12
			newMax:      13, // 49/4 = 12 (should be less than or equal to old)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tokens := counter.improvedFallbackCount(tt.content, tt.contentType)

			if tokens > tt.newMax {
				t.Errorf("improvedFallbackCount() = %v, expected <= %v (old was ~%v)",
					tokens, tt.newMax, tt.oldApprox)
			}

			t.Logf("Content: %q, Old: ~%d, New: %d (%.1f%% reduction)",
				tt.content, tt.oldApprox, tokens,
				100.0*(float64(tt.oldApprox-tokens)/float64(tt.oldApprox)))
		})
	}
}
