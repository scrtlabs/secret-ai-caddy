// simulator.go — SecretVM API mock + AI upstream mock in one binary.
// Runs two HTTP servers:
//   - :9100 — SecretVM API (balance, add-funds, list-agents, vm status)
//   - :9200 — AI upstream (echoes back a chat completion response)
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
)

// ---- SecretVM mock state ----

type agentState struct {
	mu       sync.Mutex
	balances map[string]int64 // agent address -> balance
}

var state = &agentState{
	balances: map[string]int64{
		// Pre-funded agents for testing
		"agent-funded":   100000,
		"agent-low":      10,
		"agent-unfunded": 0,
	},
}

func main() {
	// SecretVM API mock
	secretVM := http.NewServeMux()
	secretVM.HandleFunc("/api/agent/balance", handleBalance)
	secretVM.HandleFunc("/api/agent/add-funds", handleAddFunds)
	secretVM.HandleFunc("/api/agent/list", handleListAgents)
	secretVM.HandleFunc("/api/agent/vm/", handleVMStatus)
	secretVM.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	})

	// AI upstream mock
	upstream := http.NewServeMux()
	upstream.HandleFunc("/v1/chat/completions", handleChatCompletions)
	upstream.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{"status":"ok"}`))
	})

	secretVMPort := envOrDefault("SECRETVM_PORT", "9100")
	upstreamPort := envOrDefault("UPSTREAM_PORT", "9200")

	go func() {
		log.Printf("[SecretVM] Listening on :%s", secretVMPort)
		if err := http.ListenAndServe(":"+secretVMPort, secretVM); err != nil {
			log.Fatalf("SecretVM server failed: %v", err)
		}
	}()

	log.Printf("[Upstream] Listening on :%s", upstreamPort)
	if err := http.ListenAndServe(":"+upstreamPort, upstream); err != nil {
		log.Fatalf("Upstream server failed: %v", err)
	}
}

// ---- SecretVM handlers ----

func handleBalance(w http.ResponseWriter, r *http.Request) {
	// The "address" query param specifies which agent to query.
	// Falls back to X-Agent-Address header for direct agent calls.
	agentAddr := r.URL.Query().Get("address")
	if agentAddr == "" {
		agentAddr = r.Header.Get("X-Agent-Address")
	}
	log.Printf("[SecretVM] GET /api/agent/balance agent=%s", agentAddr)

	state.mu.Lock()
	bal, exists := state.balances[agentAddr]
	if !exists {
		bal = 0
	}
	state.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]any{
		"balance":       bal,
		"agent_address": agentAddr,
	})
}

func handleAddFunds(w http.ResponseWriter, r *http.Request) {
	agentAddr := r.Header.Get("X-Agent-Address")

	var body struct {
		AgentAddress string `json:"agent_address"`
		Amount       int64  `json:"amount"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	addr := body.AgentAddress
	if addr == "" {
		addr = agentAddr
	}

	state.mu.Lock()
	state.balances[addr] += body.Amount
	newBal := state.balances[addr]
	state.mu.Unlock()

	log.Printf("[SecretVM] POST /api/agent/add-funds agent=%s amount=%d new_balance=%d", addr, body.Amount, newBal)

	json.NewEncoder(w).Encode(map[string]any{
		"balance":       newBal,
		"agent_address": addr,
	})
}

func handleListAgents(w http.ResponseWriter, r *http.Request) {
	log.Printf("[SecretVM] GET /api/agent/list")

	state.mu.Lock()
	agents := make([]string, 0, len(state.balances))
	for addr, bal := range state.balances {
		if bal > 0 {
			agents = append(agents, addr)
		}
	}
	state.mu.Unlock()

	json.NewEncoder(w).Encode(map[string]any{
		"agents": agents,
	})
}

func handleVMStatus(w http.ResponseWriter, r *http.Request) {
	log.Printf("[SecretVM] GET %s", r.URL.Path)
	json.NewEncoder(w).Encode(map[string]any{
		"status": "running",
		"id":     "vm-123",
	})
}

// ---- AI Upstream handler ----

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string `json:"model"`
		MaxTokens int    `json:"max_tokens"`
		Messages  []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", 400)
		return
	}

	model := body.Model
	if model == "" {
		model = "test-model"
	}

	// Generate a deterministic response
	responseContent := fmt.Sprintf("Hello! I'm the %s model. This is a test response from the x402 simulation upstream.", model)

	log.Printf("[Upstream] POST /v1/chat/completions model=%s messages=%d", model, len(body.Messages))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-test-123",
		"object":  "chat.completion",
		"created": 1700000000,
		"model":   model,
		"choices": []map[string]any{
			{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": responseContent,
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     25,
			"completion_tokens": 30,
			"total_tokens":      55,
		},
	})
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Unused but kept for potential future use
var _ = strconv.Itoa
