# Caddy with Verifiable Message Signing

---

## Detailed Design: Message Signing Extension

### 1. Configuration Additions

**New fields in `config/config.go`:**

```go
type Config struct {
    // ... existing fields ...
    
    // === Signing Configuration ===
    SigningEnabled   bool          `json:"signing_enabled,omitempty"`
    SigningServer    string        `json:"signing_server,omitempty"`    // e.g., "http://172.17.0.1:49153"
    SigningKeyType   string        `json:"signing_key_type,omitempty"`  // "secp256k1" or "ed25519"
    SigningTimeout   time.Duration `json:"signing_timeout,omitempty"`   // e.g., 5s
    SigningPaths     []string      `json:"signing_paths,omitempty"`     // e.g., ["/api/generate", "/api/chat"]
}

func DefaultConfig() *Config {
    return &Config{
        // ... existing defaults ...
        
        // Signing defaults
        SigningEnabled:  false,
        SigningServer:   "http://172.17.0.1:49153",
        SigningKeyType:  "secp256k1",
        SigningTimeout:  5 * time.Second,
        SigningPaths:    []string{"/api/generate", "/api/chat"},
    }
}
```

**Caddyfile directives:**

```caddyfile
secret_reverse_proxy {
    # ... existing config ...
    
    # Signing configuration
    signing_enabled true
    signing_server http://172.17.0.1:49153
    signing_key_type secp256k1
    signing_timeout 5s
    signing_paths /api/generate /api/chat /v1/chat/completions
}
```

---

### 2. New Package Structure

```
secret-reverse-proxy/
├── signing/                      # NEW PACKAGE
│   ├── client.go                 # HTTP client for signing server
│   ├── hasher.go                 # Text extraction + SHA256 hashing
│   ├── types.go                  # Request/response types
│   └── headers.go                # Header constants and injection helper
├── interfaces/
│   └── interfaces.go             # Add SigningClient interface
└── secret_reverse_proxy.go       # Integrate signing into ServeHTTP
```

---

### 3. Interface Definitions

**In `interfaces/interfaces.go`:**

```go
// SigningClient handles communication with the SecretVM signing server
type SigningClient interface {
    // Sign sends a payload to the signing server and returns the signature
    Sign(ctx context.Context, payload string, keyType string) (*SigningResult, error)
    
    // IsHealthy checks if the signing server is reachable
    IsHealthy(ctx context.Context) bool
}

// SigningResult contains the result of a signing operation
type SigningResult struct {
    Signature string  // Base64-encoded signature
    Error     error   // Non-nil if signing failed
}

// TextExtractor extracts signable text from request/response bodies
type TextExtractor interface {
    // ExtractPromptText extracts the prompt text from a request body
    ExtractPromptText(body []byte, contentType string) string
    
    // ExtractCompletionText extracts the completion text from a response body
    ExtractCompletionText(body []byte, contentType string) string
}
```

---

### 4. Signing Types

**In `signing/types.go`:**

```go
package signing

import "time"

// SignRequest is sent to the SecretVM signing server
type SignRequest struct {
    KeyType string `json:"key_type"` // "secp256k1" or "ed25519"
    Payload string `json:"payload"`  // The data to sign
}

// SignResponse is returned by the SecretVM signing server
type SignResponse struct {
    Signature string `json:"signature"` // Base64-encoded signature
}

// SigningContext holds all data needed for signing a request/response pair
type SigningContext struct {
    PromptText     string
    CompletionText string
    PromptHash     string    // hex-encoded SHA256
    CompletionHash string    // hex-encoded SHA256
    Timestamp      time.Time
    Payload        string    // Final payload sent to signing server
}

// SigningOutcome represents the result of the signing process
type SigningOutcome struct {
    Status         string // "signed", "failed", "disabled", "skipped"
    Signature      string // Base64 signature (if signed)
    Algorithm      string // "secp256k1" or "ed25519"
    RequestHash    string // Hex SHA256 of prompt
    ResponseHash   string // Hex SHA256 of completion
    Timestamp      string // RFC3339 timestamp
    Error          string // Error message (if failed)
}
```

---

### 5. Text Extraction & Hashing

**In `signing/hasher.go`:**

```go
package signing

import (
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "strings"
    "time"
)

type Hasher struct{}

func NewHasher() *Hasher {
    return &Hasher{}
}

// ExtractPromptText extracts prompt/message content from Ollama request formats
func (h *Hasher) ExtractPromptText(body []byte, contentType string) string {
    if !strings.Contains(strings.ToLower(contentType), "application/json") {
        return string(body) // Fallback: treat as plain text
    }
    
    var data map[string]any
    if err := json.Unmarshal(body, &data); err != nil {
        return string(body)
    }
    
    // Try "prompt" field (Ollama /api/generate)
    if prompt, ok := data["prompt"].(string); ok {
        return prompt
    }
    
    // Try "messages" array (Ollama /api/chat, OpenAI format)
    if messages, ok := data["messages"].([]any); ok {
        var texts []string
        for _, msg := range messages {
            if msgMap, ok := msg.(map[string]any); ok {
                if content, ok := msgMap["content"].(string); ok {
                    texts = append(texts, content)
                }
            }
        }
        return strings.Join(texts, "\n")
    }
    
    return ""
}

// ExtractCompletionText extracts response/completion from Ollama response formats
func (h *Hasher) ExtractCompletionText(body []byte, contentType string) string {
    if !strings.Contains(strings.ToLower(contentType), "application/json") {
        return string(body)
    }
    
    var data map[string]any
    if err := json.Unmarshal(body, &data); err != nil {
        return string(body)
    }
    
    // Try "response" field (Ollama /api/generate)
    if response, ok := data["response"].(string); ok {
        return response
    }
    
    // Try "message.content" (Ollama /api/chat)
    if message, ok := data["message"].(map[string]any); ok {
        if content, ok := message["content"].(string); ok {
            return content
        }
    }
    
    // Try "choices[0].message.content" (OpenAI format)
    if choices, ok := data["choices"].([]any); ok && len(choices) > 0 {
        if choice, ok := choices[0].(map[string]any); ok {
            if message, ok := choice["message"].(map[string]any); ok {
                if content, ok := message["content"].(string); ok {
                    return content
                }
            }
        }
    }
    
    return ""
}

// HashText computes SHA256 of text and returns hex-encoded string
func (h *Hasher) HashText(text string) string {
    hash := sha256.Sum256([]byte(text))
    return hex.EncodeToString(hash[:])
}

// BuildSigningPayload creates the payload string for the signing server
// Format: sha256(prompt) || sha256(completion) || timestamp
func (h *Hasher) BuildSigningPayload(promptHash, completionHash string, timestamp time.Time) string {
    return promptHash + completionHash + timestamp.UTC().Format(time.RFC3339)
}

// CreateSigningContext builds the complete signing context from request/response
func (h *Hasher) CreateSigningContext(
    requestBody []byte, 
    requestContentType string,
    responseBody []byte, 
    responseContentType string,
) *SigningContext {
    promptText := h.ExtractPromptText(requestBody, requestContentType)
    completionText := h.ExtractCompletionText(responseBody, responseContentType)
    
    promptHash := h.HashText(promptText)
    completionHash := h.HashText(completionText)
    timestamp := time.Now().UTC()
    
    return &SigningContext{
        PromptText:     promptText,
        CompletionText: completionText,
        PromptHash:     promptHash,
        CompletionHash: completionHash,
        Timestamp:      timestamp,
        Payload:        h.BuildSigningPayload(promptHash, completionHash, timestamp),
    }
}
```

---

### 6. Signing Client

**In `signing/client.go`:**

```go
package signing

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "net/http"
    "time"
    
    "go.uber.org/zap"
)

type Client struct {
    serverURL  string
    httpClient *http.Client
    logger     *zap.Logger
}

func NewClient(serverURL string, timeout time.Duration, logger *zap.Logger) *Client {
    return &Client{
        serverURL: serverURL,
        httpClient: &http.Client{
            Timeout: timeout,
        },
        logger: logger,
    }
}

// Sign sends the payload to the signing server and returns the signature
func (c *Client) Sign(ctx context.Context, payload string, keyType string) (*SigningResult, error) {
    reqBody := SignRequest{
        KeyType: keyType,
        Payload: payload,
    }
    
    jsonBody, err := json.Marshal(reqBody)
    if err != nil {
        return nil, fmt.Errorf("failed to marshal signing request: %w", err)
    }
    
    req, err := http.NewRequestWithContext(ctx, "POST", c.serverURL+"/sign", bytes.NewReader(jsonBody))
    if err != nil {
        return nil, fmt.Errorf("failed to create signing request: %w", err)
    }
    req.Header.Set("Content-Type", "application/json")
    
    c.logger.Debug("Sending signing request",
        zap.String("server", c.serverURL),
        zap.String("key_type", keyType),
        zap.Int("payload_length", len(payload)))
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, fmt.Errorf("signing request failed: %w", err)
    }
    defer resp.Body.Close()
    
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("signing server returned status %d", resp.StatusCode)
    }
    
    var signResp SignResponse
    if err := json.NewDecoder(resp.Body).Decode(&signResp); err != nil {
        return nil, fmt.Errorf("failed to decode signing response: %w", err)
    }
    
    if signResp.Signature == "" {
        return nil, fmt.Errorf("signing server returned empty signature")
    }
    
    c.logger.Debug("Signing successful",
        zap.Int("signature_length", len(signResp.Signature)))
    
    return &SigningResult{
        Signature: signResp.Signature,
    }, nil
}

// IsHealthy performs a simple check if the signing server is reachable
func (c *Client) IsHealthy(ctx context.Context) bool {
    req, err := http.NewRequestWithContext(ctx, "GET", c.serverURL+"/health", nil)
    if err != nil {
        return false
    }
    
    resp, err := c.httpClient.Do(req)
    if err != nil {
        return false
    }
    defer resp.Body.Close()
    
    return resp.StatusCode == http.StatusOK
}
```

---

### 7. Header Constants & Injection

**In `signing/headers.go`:**

```go
package signing

import "net/http"

// Header constants for signing metadata
const (
    HeaderSignature       = "X-Secret-Signature"
    HeaderSignatureAlgo   = "X-Secret-Signature-Algo"
    HeaderRequestHash     = "X-Secret-Request-Hash"
    HeaderResponseHash    = "X-Secret-Response-Hash"
    HeaderSignatureTime   = "X-Secret-Signature-Timestamp"
    HeaderSignatureStatus = "X-Secret-Signature-Status"
    HeaderSignatureError  = "X-Secret-Signature-Error"
)

// Status values
const (
    StatusSigned   = "signed"
    StatusFailed   = "failed"
    StatusDisabled = "disabled"
    StatusSkipped  = "skipped"  // Path not in signing_paths
)

// InjectSigningHeaders adds signing headers to the response
func InjectSigningHeaders(w http.ResponseWriter, outcome *SigningOutcome) {
    h := w.Header()
    
    h.Set(HeaderSignatureStatus, outcome.Status)
    
    switch outcome.Status {
    case StatusSigned:
        h.Set(HeaderSignature, outcome.Signature)
        h.Set(HeaderSignatureAlgo, outcome.Algorithm)
        h.Set(HeaderRequestHash, outcome.RequestHash)
        h.Set(HeaderResponseHash, outcome.ResponseHash)
        h.Set(HeaderSignatureTime, outcome.Timestamp)
        
    case StatusFailed:
        h.Set(HeaderRequestHash, outcome.RequestHash)
        h.Set(HeaderResponseHash, outcome.ResponseHash)
        h.Set(HeaderSignatureTime, outcome.Timestamp)
        if outcome.Error != "" {
            h.Set(HeaderSignatureError, outcome.Error)
        }
        
    case StatusSkipped, StatusDisabled:
        // Minimal headers - just status
    }
}
```

---

### 8. Modified ServeHTTP Flow

**Updated flow in `secret_reverse_proxy.go`:**

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                            ServeHTTP Flow                                    │
├─────────────────────────────────────────────────────────────────────────────┤
│  1. Record request start time                                               │
│  2. Handle /metrics endpoint (existing)                                     │
│  3. Check blocked URLs (existing)                                           │
│  4. Extract & validate API key (existing)                                   │
│  5. Read request body (existing - for token counting)                       │
│                                                                             │
│  ┌─ NEW: Check if signing enabled AND path matches signing_paths ─┐        │
│  │  • If no: proceed normally, add X-Secret-Signature-Status: skipped      │
│  │  • If yes: extract prompt text, store for later                         │
│  └─────────────────────────────────────────────────────────────────┘        │
│                                                                             │
│  6. Forward request to Ollama via next.ServeHTTP (existing)                │
│  7. Capture response body (existing - via wrapped ResponseWriter)           │
│  8. Count tokens (existing)                                                 │
│                                                                             │
│  ┌─ NEW: If signing enabled for this path ─────────────────────────┐       │
│  │  • Extract completion text from response                         │       │
│  │  • Hash prompt + completion                                      │       │
│  │  • Build signing payload                                         │       │
│  │  • Call signing server                                           │       │
│  │  • Inject headers based on outcome                               │       │
│  └──────────────────────────────────────────────────────────────────┘       │
│                                                                             │
│  9. Record usage metrics (existing)                                         │
│ 10. Return response                                                         │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

### 9. Middleware Struct Additions

```go
type Middleware struct {
    // ... existing fields ...
    
    // Signing components
    signingClient  *signing.Client
    signingHasher  *signing.Hasher
    signingPaths   map[string]bool  // Fast path lookup
}
```

**In Provision():**

```go
// Initialize signing components if enabled
if m.Config.SigningEnabled {
    m.signingClient = signing.NewClient(
        m.Config.SigningServer,
        m.Config.SigningTimeout,
        logger,
    )
    m.signingHasher = signing.NewHasher()
    
    // Build fast path lookup map
    m.signingPaths = make(map[string]bool)
    for _, path := range m.Config.SigningPaths {
        m.signingPaths[path] = true
    }
    
    logger.Info("✅ Signing components initialized",
        zap.String("server", m.Config.SigningServer),
        zap.String("key_type", m.Config.SigningKeyType),
        zap.Strings("paths", m.Config.SigningPaths))
}
```

---

### 10. Metrics Additions

**Extended `MetricsCollector` interface:**

```go
type MetricsCollector interface {
    // ... existing methods ...
    
    // Signing metrics
    RecordSigningAttempt()
    RecordSigningSuccess()
    RecordSigningFailure(reason string)
    RecordSigningTime(duration time.Duration)
    RecordSigningSkipped()  // Path not in signing_paths
}
```

**Metrics output:**

```json
{
    "signing": {
        "attempts": 1000,
        "successes": 995,
        "failures": 5,
        "skipped": 500,
        "avg_signing_time_ms": 12.5,
        "failure_reasons": {
            "timeout": 3,
            "server_error": 2
        }
    }
}
```

---

### 11. Error Handling Matrix

| Scenario                    | Response Behavior | Headers                                         |
| --------------------------- | ----------------- | ----------------------------------------------- |
| Signing disabled globally   | Normal response   | `X-Secret-Signature-Status: disabled`           |
| Path not in `signing_paths` | Normal response   | `X-Secret-Signature-Status: skipped`            |
| Signing server timeout      | Normal response   | `Status: failed`, `Error: timeout`              |
| Signing server HTTP error   | Normal response   | `Status: failed`, `Error: server returned 5xx`  |
| Empty signature returned    | Normal response   | `Status: failed`, `Error: empty signature`      |
| JSON parse error (request)  | Normal response   | `Status: failed`, `Error: invalid request body` |
| Signing successful          | Normal response   | `Status: signed`, all hash headers              |

---

### 12. Example Request/Response Flow

**Request:**
```http
POST /api/generate HTTP/1.1
Authorization: Bearer xxx
Content-Type: application/json

{"model": "llama3.2", "prompt": "What is the capital of France?"}
```

**Response (signing successful):**
```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Secret-Signature: MEUCIQD...
X-Secret-Signature-Algo: secp256k1
X-Secret-Request-Hash: a1b2c3d4e5f6...
X-Secret-Response-Hash: f6e5d4c3b2a1...
X-Secret-Signature-Timestamp: 2025-01-22T15:30:00Z
X-Secret-Signature-Status: signed

{"model":"llama3.2","response":"The capital of France is Paris.","done":true}
```

**Response (signing failed):**
```http
HTTP/1.1 200 OK
Content-Type: application/json
X-Secret-Request-Hash: a1b2c3d4e5f6...
X-Secret-Response-Hash: f6e5d4c3b2a1...
X-Secret-Signature-Timestamp: 2025-01-22T15:30:00Z
X-Secret-Signature-Status: failed
X-Secret-Signature-Error: signing server timeout

{"model":"llama3.2","response":"The capital of France is Paris.","done":true}
```

---

### 13. Caddyfile Parsing Additions

**In `UnmarshalCaddyfile`:**

```go
case "signing_enabled":
    var enabled string
    if !d.Args(&enabled) {
        return d.ArgErr()
    }
    m.Config.SigningEnabled = (enabled == "true" || enabled == "yes" || enabled == "1")

case "signing_server":
    if !d.Args(&m.Config.SigningServer) {
        return d.ArgErr()
    }

case "signing_key_type":
    if !d.Args(&m.Config.SigningKeyType) {
        return d.ArgErr()
    }
    // Validate key type
    if m.Config.SigningKeyType != "secp256k1" && m.Config.SigningKeyType != "ed25519" {
        return fmt.Errorf("invalid signing_key_type: must be 'secp256k1' or 'ed25519'")
    }

case "signing_timeout":
    var timeout string
    if !d.Args(&timeout) {
        return d.ArgErr()
    }
    duration, err := time.ParseDuration(timeout)
    if err != nil {
        return fmt.Errorf("invalid signing_timeout: %w", err)
    }
    m.Config.SigningTimeout = duration

case "signing_paths":
    m.Config.SigningPaths = d.RemainingArgs()
    if len(m.Config.SigningPaths) == 0 {
        return d.ArgErr()
    }
```

---

## Summary

| Component  | File                            | Purpose                        |
| ---------- | ------------------------------- | ------------------------------ |
| Config     | `config/config.go`              | New signing config fields      |
| Types      | `signing/types.go`              | Request/response structs       |
| Hasher     | `signing/hasher.go`             | Text extraction + SHA256       |
| Client     | `signing/client.go`             | HTTP client for signing server |
| Headers    | `signing/headers.go`            | Header constants + injection   |
| Interfaces | `interfaces/interfaces.go`      | SigningClient interface        |
| Middleware | `secret_reverse_proxy.go`       | Orchestration in ServeHTTP     |
| Caddyfile  | `secret_reverse_proxy.go`       | New directive parsing          |
| Metrics    | `metering/metrics_collector.go` | Signing metrics                |

