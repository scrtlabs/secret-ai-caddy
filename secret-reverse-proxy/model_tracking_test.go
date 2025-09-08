package secret_reverse_proxy

import (
	"encoding/json"
	"testing"
)

func TestModelAwareTokenTracking(t *testing.T) {
	// Test the model detection function
	testCases := []struct {
		name          string
		requestBody   string
		contentType   string
		expectedModel string
	}{
		{
			name:          "Valid JSON with model",
			requestBody:   `{"model": "gpt-4", "prompt": "Hello world"}`,
			contentType:   "application/json",
			expectedModel: "gpt-4",
		},
		{
			name:          "Valid JSON with different model",
			requestBody:   `{"model": "gpt-3.5-turbo", "messages": []}`,
			contentType:   "application/json",
			expectedModel: "gpt-3.5-turbo",
		},
		{
			name:          "JSON without model field",
			requestBody:   `{"prompt": "Hello world"}`,
			contentType:   "application/json",
			expectedModel: "unknown",
		},
		{
			name:          "Non-JSON content",
			requestBody:   "plain text",
			contentType:   "text/plain",
			expectedModel: "unknown",
		},
		{
			name:          "Invalid JSON",
			requestBody:   `{"model": "gpt-4", "invalid": }`,
			contentType:   "application/json",
			expectedModel: "unknown",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := detectModelFromRequestBody(tc.requestBody, tc.contentType)
			if result != tc.expectedModel {
				t.Errorf("Expected model %q, got %q", tc.expectedModel, result)
			}
		})
	}
}

func TestTokenAccumulatorWithModel(t *testing.T) {
	ta := NewTokenAccumulator()
	
	// Record usage for different models
	ta.RecordUsageWithModel("api_key_hash_1", "gpt-4", 100, 50)
	ta.RecordUsageWithModel("api_key_hash_1", "gpt-3.5-turbo", 200, 80)
	ta.RecordUsageWithModel("api_key_hash_2", "gpt-4", 150, 70)
	
	// Get usage and verify structure
	usage := ta.PeekUsage()
	
	// Check first API key
	if len(usage) != 2 {
		t.Errorf("Expected 2 API keys, got %d", len(usage))
	}
	
	apiKey1Usage := usage["api_key_hash_1"]
	if apiKey1Usage.InputTokens != 300 { // 100 + 200
		t.Errorf("Expected total input tokens 300, got %d", apiKey1Usage.InputTokens)
	}
	if apiKey1Usage.OutputTokens != 130 { // 50 + 80
		t.Errorf("Expected total output tokens 130, got %d", apiKey1Usage.OutputTokens)
	}
	
	// Check per-model breakdown
	if len(apiKey1Usage.ModelUsage) != 2 {
		t.Errorf("Expected 2 models for api_key_hash_1, got %d", len(apiKey1Usage.ModelUsage))
	}
	
	gpt4Usage := apiKey1Usage.ModelUsage["gpt-4"]
	if gpt4Usage.InputTokens != 100 || gpt4Usage.OutputTokens != 50 {
		t.Errorf("Expected gpt-4 usage (100, 50), got (%d, %d)", gpt4Usage.InputTokens, gpt4Usage.OutputTokens)
	}
	
	gpt35Usage := apiKey1Usage.ModelUsage["gpt-3.5-turbo"]
	if gpt35Usage.InputTokens != 200 || gpt35Usage.OutputTokens != 80 {
		t.Errorf("Expected gpt-3.5-turbo usage (200, 80), got (%d, %d)", gpt35Usage.InputTokens, gpt35Usage.OutputTokens)
	}
}

func TestBuildRecordsWithModelData(t *testing.T) {
	config := &Config{
		MeteringURL: "http://example.com",
	}
	ta := NewTokenAccumulator()
	reporter := NewResilientReporter(config, ta)
	
	// Create test usage data
	ta.RecordUsageWithModel("test_hash_1", "gpt-4", 100, 50)
	ta.RecordUsageWithModel("test_hash_1", "gpt-3.5-turbo", 200, 80)
	
	usage := ta.FlushUsage()
	records := reporter.buildRecords(usage)
	
	if len(records) != 1 {
		t.Errorf("Expected 1 record, got %d", len(records))
	}
	
	record := records[0]
	
	// Check basic fields
	if record["api_key_hash"] != "test_hash_1" {
		t.Errorf("Expected api_key_hash test_hash_1, got %v", record["api_key_hash"])
	}
	if record["input_tokens"] != 300 {
		t.Errorf("Expected input_tokens 300, got %v", record["input_tokens"])
	}
	if record["output_tokens"] != 130 {
		t.Errorf("Expected output_tokens 130, got %v", record["output_tokens"])
	}
	
	// Check model usage data
	modelUsageRaw, exists := record["model_usage"]
	if !exists {
		t.Error("Expected model_usage field to exist")
	}
	
	modelUsage, ok := modelUsageRaw.(map[string]map[string]any)
	if !ok {
		t.Error("Expected model_usage to be map[string]map[string]any")
	}
	
	if len(modelUsage) != 2 {
		t.Errorf("Expected 2 models in usage data, got %d", len(modelUsage))
	}
	
	// Verify specific model data
	gpt4Data := modelUsage["gpt-4"]
	if gpt4Data["input_tokens"] != 100 || gpt4Data["output_tokens"] != 50 {
		t.Errorf("Expected gpt-4 data (100, 50), got (%v, %v)", 
			gpt4Data["input_tokens"], gpt4Data["output_tokens"])
	}
}

func TestJSONPayloadStructure(t *testing.T) {
	config := &Config{
		MeteringURL: "http://example.com",
	}
	ta := NewTokenAccumulator()
	reporter := NewResilientReporter(config, ta)
	
	// Create test usage data with models
	ta.RecordUsageWithModel("hash1", "gpt-4", 100, 50)
	ta.RecordUsageWithModel("hash1", "claude-3", 200, 80)
	ta.RecordUsageWithModel("hash2", "gpt-3.5-turbo", 150, 70)
	
	usage := ta.FlushUsage()
	records := reporter.buildRecords(usage)
	
	// Convert to the payload format that would be sent
	usageData := make(map[string]map[string]any)
	for _, record := range records {
		if apiKeyHash, ok := record["api_key_hash"].(string); ok {
			apiKeyData := map[string]any{
				"input_tokens":  record["input_tokens"],
				"output_tokens": record["output_tokens"],
				"timestamp":     record["timestamp"],
			}
			if modelUsage, exists := record["model_usage"]; exists {
				apiKeyData["model_usage"] = modelUsage
			}
			usageData[apiKeyHash] = apiKeyData
		}
	}
	
	payload := map[string]any{
		"usage_data": usageData,
	}
	
	// Convert to JSON to verify structure
	jsonData, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal payload: %v", err)
	}
	
	t.Logf("Generated payload structure:\n%s", string(jsonData))
	
	// Basic verification
	if len(usageData) != 2 {
		t.Errorf("Expected 2 API keys in payload, got %d", len(usageData))
	}
	
	// Verify hash1 has model_usage data
	hash1Data := usageData["hash1"]
	if modelUsage, exists := hash1Data["model_usage"]; !exists {
		t.Error("Expected hash1 to have model_usage data")
	} else if modelUsageMap, ok := modelUsage.(map[string]map[string]any); !ok {
		t.Error("Expected model_usage to be proper map structure")
	} else if len(modelUsageMap) != 2 {
		t.Errorf("Expected hash1 to have 2 models, got %d", len(modelUsageMap))
	}
}