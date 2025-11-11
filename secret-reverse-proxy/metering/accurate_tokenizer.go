package metering

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/caddyserver/caddy/v2"
	sentencepiece "github.com/eliben/go-sentencepiece"
	"github.com/sugarme/tokenizer"
	"github.com/sugarme/tokenizer/pretrained"
	"go.uber.org/zap"
)

// AccurateTokenizer provides model-specific accurate token counting
type AccurateTokenizer struct {
	logger       *zap.Logger
	cacheDir     string
	mu           sync.RWMutex
	hfTokenizers map[string]*tokenizer.Tokenizer // HuggingFace tokenizers
	spTokenizers map[string]*sentencepiece.Processor // SentencePiece tokenizers
	modelMap     map[string]TokenizerConfig // Model name to tokenizer mapping
}

// TokenizerConfig defines how to load a tokenizer for a specific model
type TokenizerConfig struct {
	Type       string // "huggingface", "sentencepiece", "fallback"
	Identifier string // HF model name or path to tokenizer file
}

// NewAccurateTokenizer creates a new accurate tokenizer with caching
func NewAccurateTokenizer(cacheDir string) (*AccurateTokenizer, error) {
	if cacheDir == "" {
		cacheDir = "/tmp/tokenizers"
	}

	// Ensure cache directory exists
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	at := &AccurateTokenizer{
		logger:       caddy.Log(),
		cacheDir:     cacheDir,
		hfTokenizers: make(map[string]*tokenizer.Tokenizer),
		spTokenizers: make(map[string]*sentencepiece.Processor),
		modelMap:     make(map[string]TokenizerConfig),
	}

	// Initialize model mappings
	at.initializeModelMappings()

	return at, nil
}

// initializeModelMappings sets up the mapping from model names to tokenizer configs
func (at *AccurateTokenizer) initializeModelMappings() {
	// Llama models
	at.modelMap["llama"] = TokenizerConfig{Type: "huggingface", Identifier: "meta-llama/Llama-2-7b-hf"}
	at.modelMap["llama-2"] = TokenizerConfig{Type: "huggingface", Identifier: "meta-llama/Llama-2-7b-hf"}
	at.modelMap["llama-3"] = TokenizerConfig{Type: "huggingface", Identifier: "meta-llama/Meta-Llama-3-8B"}
	at.modelMap["llama2"] = TokenizerConfig{Type: "huggingface", Identifier: "meta-llama/Llama-2-7b-hf"}
	at.modelMap["llama3"] = TokenizerConfig{Type: "huggingface", Identifier: "meta-llama/Meta-Llama-3-8B"}
	at.modelMap["llama3.3"] = TokenizerConfig{Type: "huggingface", Identifier: "meta-llama/Llama-3.3-70B-Instruct"}

	// Mistral models
	at.modelMap["mistral"] = TokenizerConfig{Type: "huggingface", Identifier: "mistralai/Mistral-7B-v0.1"}
	at.modelMap["mistral-7b"] = TokenizerConfig{Type: "huggingface", Identifier: "mistralai/Mistral-7B-v0.1"}
	at.modelMap["mixtral"] = TokenizerConfig{Type: "huggingface", Identifier: "mistralai/Mixtral-8x7B-v0.1"}

	// Falcon models
	at.modelMap["falcon"] = TokenizerConfig{Type: "huggingface", Identifier: "tiiuae/falcon-7b"}
	at.modelMap["falcon-7b"] = TokenizerConfig{Type: "huggingface", Identifier: "tiiuae/falcon-7b"}

	// BERT models
	at.modelMap["bert"] = TokenizerConfig{Type: "huggingface", Identifier: "bert-base-uncased"}
	at.modelMap["bert-base"] = TokenizerConfig{Type: "huggingface", Identifier: "bert-base-uncased"}

	at.logger.Info("Initialized model mappings", zap.Int("count", len(at.modelMap)))
}

// PreloadCommonModels pre-caches tokenizers for common models to improve startup performance
func (at *AccurateTokenizer) PreloadCommonModels() {
	commonModels := []string{"llama-2", "mistral"}

	at.logger.Info("Preloading common tokenizers", zap.Strings("models", commonModels))

	var wg sync.WaitGroup
	for _, model := range commonModels {
		wg.Add(1)
		go func(m string) {
			defer wg.Done()
			if _, err := at.CountTokens("test", m); err != nil {
				at.logger.Warn("Failed to preload tokenizer",
					zap.String("model", m),
					zap.Error(err))
			} else {
				at.logger.Info("Preloaded tokenizer", zap.String("model", m))
			}
		}(model)
	}

	wg.Wait()
	at.logger.Info("Finished preloading tokenizers")
}

// CountTokens counts tokens accurately based on the model
func (at *AccurateTokenizer) CountTokens(text string, model string) (int, error) {
	if text == "" {
		return 0, nil
	}

	// Normalize model name
	modelKey := at.normalizeModelName(model)

	// Try to get tokenizer config
	config, exists := at.modelMap[modelKey]
	if !exists {
		// Model not found - use fallback
		at.logger.Debug("Model not found in mapping, using fallback",
			zap.String("model", model),
			zap.String("normalized", modelKey))
		return at.fallbackCount(text), nil
	}

	// Load and use appropriate tokenizer
	switch config.Type {
	case "huggingface":
		return at.countWithHuggingFace(text, config.Identifier)
	case "sentencepiece":
		return at.countWithSentencePiece(text, config.Identifier)
	default:
		return at.fallbackCount(text), nil
	}
}

// normalizeModelName extracts the base model name from variations
func (at *AccurateTokenizer) normalizeModelName(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))

	// Extract base model name from common patterns
	// e.g., "llama3.3:70b" -> "llama3.3"
	if idx := strings.Index(model, ":"); idx != -1 {
		model = model[:idx]
	}

	// Remove version suffixes for exact matching
	// e.g., "mistral-7b-v0.1" -> "mistral-7b"
	parts := strings.Split(model, "-")
	if len(parts) > 0 {
		// Check if we have an exact match first
		if _, exists := at.modelMap[model]; exists {
			return model
		}

		// Try progressively shorter names
		// "llama-2-7b-chat" -> "llama-2-7b" -> "llama-2" -> "llama"
		for i := len(parts); i > 0; i-- {
			testName := strings.Join(parts[:i], "-")
			if _, exists := at.modelMap[testName]; exists {
				return testName
			}
		}
	}

	// Try first word (e.g., "llama" from "llama-custom")
	if idx := strings.Index(model, "-"); idx != -1 {
		firstWord := model[:idx]
		if _, exists := at.modelMap[firstWord]; exists {
			return firstWord
		}
	}

	return model
}

// countWithHuggingFace uses HuggingFace tokenizer
func (at *AccurateTokenizer) countWithHuggingFace(text string, modelIdentifier string) (int, error) {
	tk, err := at.getOrLoadHFTokenizer(modelIdentifier)
	if err != nil {
		at.logger.Warn("Failed to load HuggingFace tokenizer, using fallback",
			zap.String("model", modelIdentifier),
			zap.Error(err))
		return at.fallbackCount(text), nil
	}

	// Encode the text
	encoding, err := tk.EncodeSingle(text)
	if err != nil {
		at.logger.Warn("Failed to encode text with HuggingFace tokenizer, using fallback",
			zap.String("model", modelIdentifier),
			zap.Error(err))
		return at.fallbackCount(text), nil
	}

	return len(encoding.Ids), nil
}

// countWithSentencePiece uses SentencePiece tokenizer
func (at *AccurateTokenizer) countWithSentencePiece(text string, modelPath string) (int, error) {
	sp, err := at.getOrLoadSPTokenizer(modelPath)
	if err != nil {
		at.logger.Warn("Failed to load SentencePiece tokenizer, using fallback",
			zap.String("model", modelPath),
			zap.Error(err))
		return at.fallbackCount(text), nil
	}

	// Encode the text
	tokens := sp.Encode(text)
	return len(tokens), nil
}

// getOrLoadHFTokenizer gets or loads a HuggingFace tokenizer
func (at *AccurateTokenizer) getOrLoadHFTokenizer(modelIdentifier string) (*tokenizer.Tokenizer, error) {
	// Check if already loaded
	at.mu.RLock()
	if tk, exists := at.hfTokenizers[modelIdentifier]; exists {
		at.mu.RUnlock()
		return tk, nil
	}
	at.mu.RUnlock()

	// Load tokenizer (with write lock)
	at.mu.Lock()
	defer at.mu.Unlock()

	// Double-check after acquiring write lock
	if tk, exists := at.hfTokenizers[modelIdentifier]; exists {
		return tk, nil
	}

	at.logger.Info("Loading HuggingFace tokenizer", zap.String("model", modelIdentifier))

	// Try to get tokenizer.json from HuggingFace
	configFile, err := tokenizer.CachedPath(modelIdentifier, "tokenizer.json")
	if err != nil {
		return nil, fmt.Errorf("failed to get tokenizer config: %w", err)
	}

	// Load tokenizer from file
	tk, err := pretrained.FromFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load tokenizer: %w", err)
	}

	// Cache the tokenizer
	at.hfTokenizers[modelIdentifier] = tk

	at.logger.Info("Successfully loaded HuggingFace tokenizer", zap.String("model", modelIdentifier))

	return tk, nil
}

// getOrLoadSPTokenizer gets or loads a SentencePiece tokenizer
func (at *AccurateTokenizer) getOrLoadSPTokenizer(modelPath string) (*sentencepiece.Processor, error) {
	// Check if already loaded
	at.mu.RLock()
	if sp, exists := at.spTokenizers[modelPath]; exists {
		at.mu.RUnlock()
		return sp, nil
	}
	at.mu.RUnlock()

	// Load tokenizer (with write lock)
	at.mu.Lock()
	defer at.mu.Unlock()

	// Double-check after acquiring write lock
	if sp, exists := at.spTokenizers[modelPath]; exists {
		return sp, nil
	}

	at.logger.Info("Loading SentencePiece tokenizer", zap.String("model", modelPath))

	// Check if model file exists
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tokenizer model file not found: %s", modelPath)
	}

	// Load SentencePiece model
	sp, err := sentencepiece.NewProcessorFromPath(modelPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load SentencePiece model: %w", err)
	}

	// Cache the tokenizer
	at.spTokenizers[modelPath] = sp

	at.logger.Info("Successfully loaded SentencePiece tokenizer", zap.String("model", modelPath))

	return sp, nil
}

// fallbackCount provides a simple character-based token estimation
func (at *AccurateTokenizer) fallbackCount(text string) int {
	// Use a simple chars/4 estimation (more conservative than old heuristic)
	chars := utf8.RuneCountInString(text)
	tokens := chars / 4

	// Ensure at least 1 token for non-empty text
	if tokens == 0 && len(text) > 0 {
		tokens = 1
	}

	return tokens
}

// GetCachedModels returns a list of currently cached model tokenizers
func (at *AccurateTokenizer) GetCachedModels() map[string]int {
	at.mu.RLock()
	defer at.mu.RUnlock()

	cached := make(map[string]int)
	cached["huggingface"] = len(at.hfTokenizers)
	cached["sentencepiece"] = len(at.spTokenizers)
	cached["total"] = len(at.hfTokenizers) + len(at.spTokenizers)

	return cached
}

// SetCustomMapping allows adding custom model-to-tokenizer mappings
func (at *AccurateTokenizer) SetCustomMapping(modelName string, config TokenizerConfig) {
	at.mu.Lock()
	defer at.mu.Unlock()

	at.modelMap[strings.ToLower(modelName)] = config
	at.logger.Info("Added custom model mapping",
		zap.String("model", modelName),
		zap.String("type", config.Type),
		zap.String("identifier", config.Identifier))
}

// LoadTokenizerFromFile loads a tokenizer from a local file
func (at *AccurateTokenizer) LoadTokenizerFromFile(modelName, filePath string) error {
	ext := filepath.Ext(filePath)

	var config TokenizerConfig
	switch ext {
	case ".json":
		config = TokenizerConfig{Type: "huggingface", Identifier: filePath}
	case ".model":
		config = TokenizerConfig{Type: "sentencepiece", Identifier: filePath}
	default:
		return fmt.Errorf("unsupported tokenizer file type: %s", ext)
	}

	at.SetCustomMapping(modelName, config)
	return nil
}
