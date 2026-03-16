# Verifiable Message Signing - Implementation Plan

## Document Information

| Field             | Value                                      |
| ----------------- | ------------------------------------------ |
| **Version**       | 1.0                                        |
| **Status**        | Ready for Implementation                   |
| **Last Updated**  | January 2025                               |
| **Target Module** | `github.com/scrtlabs/secret-reverse-proxy` |
| **Go Version**    | 1.24+                                      |
| **Caddy Version** | 2.10.0                                     |

---

## Table of Contents

1. [Prerequisites](#1-prerequisites)
2. [Repository Setup](#2-repository-setup)
3. [Implementation Phases](#3-implementation-phases)
4. [Phase 1: Types and Interfaces](#4-phase-1-types-and-interfaces)
5. [Phase 2: Signing Package](#5-phase-2-signing-package)
6. [Phase 3: Configuration](#6-phase-3-configuration)
7. [Phase 4: Middleware Integration](#7-phase-4-middleware-integration)
8. [Phase 5: Metrics Integration](#8-phase-5-metrics-integration)
9. [Phase 6: Testing](#9-phase-6-testing)
10. [Phase 7: Documentation & Review](#10-phase-7-documentation--review)
11. [Claude Code Quick Reference](#11-claude-code-quick-reference)
12. [Troubleshooting](#12-troubleshooting)

---

## 1. Prerequisites

### 1.1 Development Environment

Before starting implementation, ensure you have:

```bash
# Required tools
go version          # Must be 1.24+
git version         # Any recent version
docker --version    # For testing
docker-compose --version

# Install xcaddy for building custom Caddy
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

# Verify xcaddy installation
xcaddy version
```

### 1.2 Understanding the Codebase

**Key files to review before starting:**

| File                               | Purpose               | Why It Matters                             |
| ---------------------------------- | --------------------- | ------------------------------------------ |
| `secret_reverse_proxy.go`          | Main middleware       | Where signing orchestration will be added  |
| `config/config.go`                 | Configuration structs | Where signing config fields go             |
| `interfaces/interfaces.go`         | Interface definitions | Pattern for adding SigningClient interface |
| `metering/request_body_handler.go` | Body capture          | Already captures request/response bodies   |
| `metering/metrics_collector.go`    | Metrics               | Pattern for adding signing metrics         |
| `factories/factories.go`           | Component creation    | Pattern for signing component factory      |

### 1.3 Architecture Context

```
┌─────────────────────────────────────────────────────────────────┐
│                    secret_reverse_proxy                          │
├─────────────────────────────────────────────────────────────────┤
│  Existing Components:                                            │
│  • APIKeyValidator     - Authentication                         │
│  • BodyHandler         - Request/response body capture          │
│  • TokenCounter        - Token counting                         │
│  • MetricsCollector    - Metrics collection                     │
│  • TokenAccumulator    - Usage accumulation                     │
│  • ResilientReporter   - Usage reporting                        │
│                                                                  │
│  NEW Components (to implement):                                  │
│  • SigningClient       - HTTP client for signing server         │
│  • Hasher              - Text extraction + SHA256               │
│  • HeaderWriter        - Response header injection              │
└─────────────────────────────────────────────────────────────────┘
```

---

## 2. Repository Setup

### 2.1 Clone and Setup

```bash
# Clone the repository (replace with actual repo URL)
git clone https://github.com/scrtlabs/secret-reverse-proxy.git
cd secret-reverse-proxy

# Create a feature branch
git checkout -b feature/verifiable-message-signing

# Verify the module builds
go mod tidy
go build ./...

# Run existing tests to ensure baseline is working
go test ./... -v
```

### 2.2 Pull Latest Dependencies

```bash
# Update to latest Caddy (if needed)
go get github.com/caddyserver/caddy/v2@v2.10.0

# Tidy up
go mod tidy

# Verify build still works
go build ./...
```

### 2.3 Project Structure After Implementation

```
secret-reverse-proxy/
├── go.mod
├── go.sum
├── secret_reverse_proxy.go      # MODIFY: Add signing orchestration
├── config/
│   └── config.go                # MODIFY: Add signing config fields
├── interfaces/
│   └── interfaces.go            # MODIFY: Add signing interfaces
├── signing/                     # NEW PACKAGE
│   ├── types.go                 # NEW: Type definitions
│   ├── client.go                # NEW: Signing server HTTP client
│   ├── hasher.go                # NEW: Text extraction & hashing
│   ├── headers.go               # NEW: Header constants & injection
│   └── signing_test.go          # NEW: Unit tests
├── metering/
│   ├── metrics_collector.go     # MODIFY: Add signing metrics
│   └── request_body_handler.go  # (no changes needed)
├── factories/
│   └── factories.go             # MODIFY: Add signing factory
├── apikeyval/
│   └── validator.go             # (no changes needed)
└── testdata/                    # NEW: Test fixtures
    └── signing/
        ├── sample_request.json
        └── sample_response.json
```

---

## 3. Implementation Phases

### 3.1 Phase Overview

```
┌─────────────────────────────────────────────────────────────────┐
│  Phase 1: Types & Interfaces (Day 1)                            │
│  ├── Define SigningResult, SigningPayload types                 │
│  ├── Define SigningClient interface                             │
│  └── Define TextExtractor interface                             │
├─────────────────────────────────────────────────────────────────┤
│  Phase 2: Signing Package (Days 2-3)                            │
│  ├── Implement types.go                                         │
│  ├── Implement hasher.go (text extraction + SHA256)             │
│  ├── Implement client.go (HTTP client)                          │
│  └── Implement headers.go (header injection)                    │
├─────────────────────────────────────────────────────────────────┤
│  Phase 3: Configuration (Day 4)                                 │
│  ├── Add signing fields to Config struct                        │
│  ├── Add Caddyfile parsing for new directives                   │
│  └── Add configuration validation                               │
├─────────────────────────────────────────────────────────────────┤
│  Phase 4: Middleware Integration (Days 5-6)                     │
│  ├── Add signing fields to Middleware struct                    │
│  ├── Initialize signing components in Provision()               │
│  ├── Add signing logic to ServeHTTP()                           │
│  └── Add cleanup in Cleanup()                                   │
├─────────────────────────────────────────────────────────────────┤
│  Phase 5: Metrics Integration (Day 7)                           │
│  ├── Add signing metrics to MetricsCollector                    │
│  └── Update metrics endpoint output                             │
├─────────────────────────────────────────────────────────────────┤
│  Phase 6: Testing (Days 8-9)                                    │
│  ├── Unit tests for signing package                             │
│  ├── Integration tests for full flow                            │
│  └── End-to-end tests with Docker                               │
├─────────────────────────────────────────────────────────────────┤
│  Phase 7: Documentation & Review (Day 10)                       │
│  ├── Update README.md                                           │
│  ├── Update ARCHITECTURE.md                                     │
│  └── Code review and refinement                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## 4. Phase 1: Types and Interfaces

### 4.1 Update `interfaces/interfaces.go`

**Location:** `interfaces/interfaces.go`

**What to add:** New interfaces for the signing subsystem.

**Claude Code Prompt:**
```
Open interfaces/interfaces.go and add the following new interfaces after the existing 
MetricsCollector interface:

1. SigningClient interface with methods:
   - Sign(ctx context.Context, payload string, keyType string) (*SigningResult, error)
   - IsHealthy(ctx context.Context) bool

2. SigningResult struct with fields:
   - Signature string
   - Error error

3. TextExtractor interface with methods:
   - ExtractPromptText(body []byte, contentType string) string
   - ExtractCompletionText(body []byte, contentType string) string

Make sure to add the "context" import if not already present.
Follow the existing code style with comprehensive comments.
```

**Expected additions:**

```go
// === Signing Interfaces ===

// SigningResult contains the result of a signing operation
type SigningResult struct {
    // Signature is the base64-encoded signature from the signing server
    Signature string
    // Error is non-nil if signing failed
    Error error
}

// SigningClient handles communication with the SecretVM signing server
type SigningClient interface {
    // Sign sends a payload to the signing server and returns the signature
    // Parameters:
    //   - ctx: Context for cancellation and timeout
    //   - payload: The string payload to sign (hash concatenation)
    //   - keyType: Algorithm to use ("secp256k1" or "ed25519")
    // Returns:
    //   - *SigningResult: Contains signature or error
    //   - error: Non-nil if the request itself failed
    Sign(ctx context.Context, payload string, keyType string) (*SigningResult, error)
    
    // IsHealthy checks if the signing server is reachable
    IsHealthy(ctx context.Context) bool
}

// TextExtractor extracts signable text content from request/response bodies
type TextExtractor interface {
    // ExtractPromptText extracts the prompt text from a request body
    // Handles multiple formats: Ollama /api/generate, /api/chat, OpenAI format
    ExtractPromptText(body []byte, contentType string) string
    
    // ExtractCompletionText extracts the completion text from a response body
    // Handles multiple formats: Ollama responses, OpenAI format
    ExtractCompletionText(body []byte, contentType string) string
}
```

### 4.2 Verification

```bash
# After making changes, verify the file compiles
go build ./interfaces/...
```

---

## 5. Phase 2: Signing Package

### 5.1 Create Package Directory

```bash
mkdir -p signing
```

### 5.2 Create `signing/types.go`

**Claude Code Prompt:**
```
Create a new file signing/types.go with the following:

Package: signing
Purpose: Type definitions for the signing subsystem

Define these types:
1. SignRequest struct - sent to SecretVM signing server
   - KeyType string `json:"key_type"`
   - Payload string `json:"payload"`

2. SignResponse struct - received from signing server
   - Signature string `json:"signature"`

3. SigningContext struct - holds all data for signing a request/response pair
   - PromptText string
   - CompletionText string
   - PromptHash string (hex-encoded SHA256)
   - CompletionHash string (hex-encoded SHA256)
   - Timestamp time.Time
   - Payload string (final payload for signing server)

4. SigningOutcome struct - result of signing process
   - Status string ("signed", "failed", "disabled", "skipped")
   - Signature string
   - Algorithm string
   - RequestHash string
   - ResponseHash string
   - Timestamp string (RFC3339)
   - Error string

Add comprehensive documentation comments for each type.
Include the time import.
```

**File content:**

```go
// Package signing provides verifiable message signing capabilities for the
// Secret Reverse Proxy middleware. It enables cryptographic signing of LLM
// request/response pairs using keys secured within a SecretVM TEE.
package signing

import "time"

// SignRequest is the payload sent to the SecretVM signing server.
// The server expects a JSON body with the key type and data to sign.
type SignRequest struct {
    // KeyType specifies the signing algorithm: "secp256k1" or "ed25519"
    KeyType string `json:"key_type"`
    
    // Payload is the data to be signed (concatenated hashes + timestamp)
    Payload string `json:"payload"`
}

// SignResponse is the response from the SecretVM signing server.
// Contains the base64-encoded signature on success.
type SignResponse struct {
    // Signature is the base64-encoded cryptographic signature
    Signature string `json:"signature"`
}

// SigningContext holds all data needed for signing a request/response pair.
// This is built during request processing and used to construct the signing payload.
type SigningContext struct {
    // PromptText is the extracted prompt text from the request
    PromptText string
    
    // CompletionText is the extracted completion text from the response
    CompletionText string
    
    // PromptHash is the hex-encoded SHA256 hash of PromptText
    PromptHash string
    
    // CompletionHash is the hex-encoded SHA256 hash of CompletionText
    CompletionHash string
    
    // Timestamp is when the signing context was created
    Timestamp time.Time
    
    // Payload is the final string sent to the signing server
    // Format: {PromptHash}{CompletionHash}{Timestamp_RFC3339}
    Payload string
}

// SigningOutcome represents the complete result of the signing process.
// This is used to populate response headers.
type SigningOutcome struct {
    // Status indicates the signing result: "signed", "failed", "disabled", "skipped"
    Status string
    
    // Signature is the base64-encoded signature (only if Status == "signed")
    Signature string
    
    // Algorithm is the signing algorithm used: "secp256k1" or "ed25519"
    Algorithm string
    
    // RequestHash is the hex SHA256 of the prompt text
    RequestHash string
    
    // ResponseHash is the hex SHA256 of the completion text
    ResponseHash string
    
    // Timestamp is the RFC3339 formatted signing timestamp
    Timestamp string
    
    // Error contains the error message if Status == "failed"
    Error string
}

// Signing status constants
const (
    StatusSigned   = "signed"
    StatusFailed   = "failed"
    StatusDisabled = "disabled"
    StatusSkipped  = "skipped"
)
```

### 5.3 Create `signing/hasher.go`

**Claude Code Prompt:**
```
Create signing/hasher.go with the following:

Package: signing
Purpose: Text extraction from JSON bodies and SHA256 hashing

Implement a Hasher struct with these methods:

1. NewHasher() *Hasher - constructor

2. ExtractPromptText(body []byte, contentType string) string
   - If not JSON, return body as string
   - Try to extract from "prompt" field (Ollama /api/generate)
   - Try to extract from "messages" array, joining all "content" fields (Ollama /api/chat)
   - Return empty string if no text found

3. ExtractCompletionText(body []byte, contentType string) string
   - If not JSON, return body as string
   - Try "response" field (Ollama /api/generate)
   - Try "message.content" (Ollama /api/chat)
   - Try "choices[0].message.content" (OpenAI format)
   - Return empty string if no text found

4. HashText(text string) string
   - Compute SHA256 and return hex-encoded string

5. BuildSigningPayload(promptHash, completionHash string, timestamp time.Time) string
   - Concatenate: promptHash + completionHash + timestamp.UTC().Format(time.RFC3339)

6. CreateSigningContext(requestBody []byte, requestContentType string, 
                        responseBody []byte, responseContentType string) *SigningContext
   - Extract texts, compute hashes, build payload, return complete context

Use crypto/sha256, encoding/hex, encoding/json, strings, time packages.
Add comprehensive comments explaining the JSON extraction logic.
```

### 5.4 Create `signing/client.go`

**Claude Code Prompt:**
```
Create signing/client.go with the following:

Package: signing
Purpose: HTTP client for SecretVM signing server

Implement a Client struct with:

Fields:
- serverURL string
- httpClient *http.Client
- logger *zap.Logger

Methods:

1. NewClient(serverURL string, timeout time.Duration, logger *zap.Logger) *Client
   - Create http.Client with specified timeout
   - Store serverURL and logger

2. Sign(ctx context.Context, payload string, keyType string) (*interfaces.SigningResult, error)
   - Create SignRequest with keyType and payload
   - Marshal to JSON
   - POST to serverURL + "/sign"
   - Handle HTTP errors (non-200 status)
   - Decode SignResponse
   - Return SigningResult with signature
   - Log debug messages for request/response

3. IsHealthy(ctx context.Context) bool
   - GET serverURL + "/health"
   - Return true if status 200, false otherwise

Use: bytes, context, encoding/json, fmt, net/http, go.uber.org/zap
Import interfaces from github.com/scrtlabs/secret-reverse-proxy/interfaces

Add error handling for:
- JSON marshal/unmarshal errors
- HTTP request creation errors
- Network errors
- Non-200 status codes
- Empty signature in response
```

### 5.5 Create `signing/headers.go`

**Claude Code Prompt:**
```
Create signing/headers.go with the following:

Package: signing
Purpose: HTTP header constants and injection helper

Define header constants:
- HeaderSignature = "X-Secret-Signature"
- HeaderSignatureAlgo = "X-Secret-Signature-Algo"
- HeaderRequestHash = "X-Secret-Request-Hash"
- HeaderResponseHash = "X-Secret-Response-Hash"
- HeaderSignatureTime = "X-Secret-Signature-Timestamp"
- HeaderSignatureStatus = "X-Secret-Signature-Status"
- HeaderSignatureError = "X-Secret-Signature-Error"

Implement function:
InjectSigningHeaders(w http.ResponseWriter, outcome *SigningOutcome)
- Always set HeaderSignatureStatus
- If Status == "signed": set all headers (signature, algo, hashes, timestamp)
- If Status == "failed": set hashes, timestamp, and error
- If Status == "skipped" or "disabled": only set status

Use net/http package.
Add comments explaining when each header is set.
```

### 5.6 Verification

```bash
# Verify the signing package compiles
go build ./signing/...
```

---

## 6. Phase 3: Configuration

### 6.1 Update `config/config.go`

**Claude Code Prompt:**
```
Open config/config.go and add signing configuration fields to the Config struct.

Add these fields (grouped together with a comment block "// === Signing Configuration ==="):

1. SigningEnabled bool `json:"signing_enabled,omitempty"`
   - Default: false
   - Purpose: Master switch for signing feature

2. SigningServer string `json:"signing_server,omitempty"`
   - Default: "http://172.17.0.1:49153"
   - Purpose: URL of SecretVM signing server

3. SigningKeyType string `json:"signing_key_type,omitempty"`
   - Default: "secp256k1"
   - Purpose: Signing algorithm (secp256k1 or ed25519)

4. SigningTimeout time.Duration `json:"signing_timeout,omitempty"`
   - Default: 5 * time.Second
   - Purpose: Timeout for signing server requests

5. SigningPaths []string `json:"signing_paths,omitempty"`
   - Default: []string{"/api/generate", "/api/chat"}
   - Purpose: URL paths that should be signed

Also update the DefaultConfig() function to set these defaults.
```

### 6.2 Update `secret_reverse_proxy.go` - Caddyfile Parsing

**Claude Code Prompt:**
```
In secret_reverse_proxy.go, find the UnmarshalCaddyfile function and add parsing 
for the new signing directives in the switch statement.

Add these cases:

case "signing_enabled":
    - Parse boolean string ("true", "yes", "1" = true)
    - Set m.Config.SigningEnabled

case "signing_server":
    - Parse string URL
    - Set m.Config.SigningServer

case "signing_key_type":
    - Parse string
    - Validate: must be "secp256k1" or "ed25519"
    - Return error if invalid
    - Set m.Config.SigningKeyType

case "signing_timeout":
    - Parse duration string (e.g., "5s")
    - Return error if invalid
    - Set m.Config.SigningTimeout

case "signing_paths":
    - Use d.RemainingArgs() to get all paths
    - Return error if empty
    - Set m.Config.SigningPaths

Follow the existing code style for error handling and logging.
```

### 6.3 Update Validation

**Claude Code Prompt:**
```
In secret_reverse_proxy.go, find the Validate() function and add validation 
for signing configuration.

Add validation block (only if SigningEnabled is true):
1. Check SigningServer is not empty
2. Check SigningKeyType is "secp256k1" or "ed25519"
3. Check SigningTimeout is positive and <= 30s
4. Check SigningPaths is not empty

Log validation results with zap logger.
Return descriptive errors for invalid configuration.
```

---

## 7. Phase 4: Middleware Integration

### 7.1 Update Middleware Struct

**Claude Code Prompt:**
```
In secret_reverse_proxy.go, add signing-related fields to the Middleware struct.

Add after existing fields (with comment "// Signing components"):
1. signingClient *signing.Client
2. signingHasher *signing.Hasher
3. signingPaths map[string]bool  // Fast path lookup

Add import for the signing package:
"github.com/scrtlabs/secret-reverse-proxy/signing"
```

### 7.2 Update Provision() Function

**Claude Code Prompt:**
```
In secret_reverse_proxy.go, update the Provision() function to initialize 
signing components.

Add after existing component initialization (before the final return):

// Initialize signing components if enabled
if m.Config.SigningEnabled {
    1. Create signing.NewClient(m.Config.SigningServer, m.Config.SigningTimeout, logger)
    2. Create signing.NewHasher()
    3. Build signingPaths map from m.Config.SigningPaths slice
    4. Log successful initialization with config details

    Handle errors:
    - If client creation fails, return error
    - Log all initialization steps
}
```

### 7.3 Update ServeHTTP() Function

This is the most complex change. The signing logic must be integrated into the existing request flow.

**Claude Code Prompt:**
```
In secret_reverse_proxy.go, update ServeHTTP() to add signing logic.

The current flow is approximately:
1. Record start time
2. Handle /metrics endpoint
3. Check blocked URLs
4. Extract and validate API key
5. Read request body
6. Count input tokens
7. Forward to next handler (Ollama)
8. Capture response
9. Count output tokens
10. Record usage
11. Return response

Modify to add signing:

BEFORE forwarding to Ollama (after reading request body):
- Check if m.Config.SigningEnabled
- Check if request path is in m.signingPaths
- If signing applicable: extract prompt text and store in a variable

AFTER capturing response (before returning):
- If signing was applicable for this request:
  a. Extract completion text from response body
  b. Create SigningContext using m.signingHasher.CreateSigningContext()
  c. Call signing server: m.signingClient.Sign(ctx, context.Payload, m.Config.SigningKeyType)
  d. Build SigningOutcome based on result
  e. Call signing.InjectSigningHeaders(w, outcome)
  f. Record signing metrics if metricsCollector available

- If signing not applicable:
  a. Build SigningOutcome with Status = StatusSkipped or StatusDisabled
  b. Inject minimal headers

Key considerations:
1. Use request context with timeout for signing: ctx, cancel := context.WithTimeout(r.Context(), m.Config.SigningTimeout)
2. Always return the response even if signing fails
3. Log signing attempts and results
4. Don't modify the response body, only add headers
```

**Detailed implementation guidance:**

```go
// Add this helper method to check if path should be signed
func (m *Middleware) shouldSignPath(path string) bool {
    if !m.Config.SigningEnabled {
        return false
    }
    if m.signingPaths == nil {
        return false
    }
    return m.signingPaths[path]
}

// In ServeHTTP, the signing logic block should look like:
func (m Middleware) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
    // ... existing code up to request body reading ...
    
    // Determine if this request should be signed
    shouldSign := m.shouldSignPath(r.URL.Path)
    var promptText string
    
    if shouldSign {
        // Extract prompt text for later signing
        promptText = m.signingHasher.ExtractPromptText([]byte(requestBody), contentType)
    }
    
    // ... existing code: forward to Ollama, capture response ...
    
    // After response is captured and tokens counted:
    if m.Config.SigningEnabled {
        outcome := m.performSigning(r.Context(), shouldSign, promptText, responseBody, responseContentType)
        signing.InjectSigningHeaders(w, outcome)
        
        // Record metrics
        if m.metricsCollector != nil {
            // Record signing metrics based on outcome
        }
    }
    
    // ... existing code: record usage, return ...
}

// Add this helper method for signing orchestration
func (m *Middleware) performSigning(
    ctx context.Context,
    shouldSign bool,
    promptText string,
    responseBody []byte,
    responseContentType string,
) *signing.SigningOutcome {
    
    if !m.Config.SigningEnabled {
        return &signing.SigningOutcome{Status: signing.StatusDisabled}
    }
    
    if !shouldSign {
        return &signing.SigningOutcome{Status: signing.StatusSkipped}
    }
    
    // Extract completion and create signing context
    completionText := m.signingHasher.ExtractCompletionText(responseBody, responseContentType)
    signingCtx := m.signingHasher.CreateSigningContext(
        []byte(promptText), "application/json",
        responseBody, responseContentType,
    )
    
    // Call signing server with timeout
    signCtx, cancel := context.WithTimeout(ctx, m.Config.SigningTimeout)
    defer cancel()
    
    result, err := m.signingClient.Sign(signCtx, signingCtx.Payload, m.Config.SigningKeyType)
    
    if err != nil {
        return &signing.SigningOutcome{
            Status:       signing.StatusFailed,
            Algorithm:    m.Config.SigningKeyType,
            RequestHash:  signingCtx.PromptHash,
            ResponseHash: signingCtx.CompletionHash,
            Timestamp:    signingCtx.Timestamp.UTC().Format(time.RFC3339),
            Error:        err.Error(),
        }
    }
    
    return &signing.SigningOutcome{
        Status:       signing.StatusSigned,
        Signature:    result.Signature,
        Algorithm:    m.Config.SigningKeyType,
        RequestHash:  signingCtx.PromptHash,
        ResponseHash: signingCtx.CompletionHash,
        Timestamp:    signingCtx.Timestamp.UTC().Format(time.RFC3339),
    }
}
```

### 7.4 Update Cleanup() Function

**Claude Code Prompt:**
```
In secret_reverse_proxy.go, update the Cleanup() function to clean up signing resources.

Add after existing cleanup code:
- Log "Cleaning up signing components" if signing was enabled
- Set signingClient to nil
- Set signingHasher to nil
- Clear signingPaths map

This ensures no resource leaks when Caddy reloads configuration.
```

---

## 8. Phase 5: Metrics Integration

### 8.1 Update MetricsCollector Interface

**Claude Code Prompt:**
```
In interfaces/interfaces.go, add signing metrics methods to MetricsCollector interface:

RecordSigningAttempt()
RecordSigningSuccess()
RecordSigningFailure(reason string)
RecordSigningTime(duration time.Duration)
RecordSigningSkipped()
```

### 8.2 Update MetricsCollector Implementation

**Claude Code Prompt:**
```
In metering/metrics_collector.go, add signing metrics tracking:

Add fields to the struct:
- signingAttempts int64
- signingSuccesses int64
- signingFailures int64
- signingSkipped int64
- signingTotalTimeNs int64
- signingOperations int64
- signingFailureReasons map[string]int64 (with mutex protection)

Implement the new interface methods.

Update GetMetrics() to include signing metrics in the output:
"signing": {
    "enabled": true,
    "attempts_total": ...,
    "successes_total": ...,
    "failures_total": ...,
    "skipped_total": ...,
    "success_rate": ...,
    "avg_duration_ms": ...,
    "failures_by_type": {...}
}
```

### 8.3 Update Factories

**Claude Code Prompt:**
```
In factories/factories.go, add a factory function for creating signing components:

func CreateSigningClient(serverURL string, timeout time.Duration, logger *zap.Logger) *signing.Client {
    return signing.NewClient(serverURL, timeout, logger)
}

func CreateHasher() *signing.Hasher {
    return signing.NewHasher()
}

Add import for the signing package.
```

---

## 9. Phase 6: Testing

### 9.1 Create Test Fixtures

```bash
mkdir -p testdata/signing
```

**Create `testdata/signing/sample_request_generate.json`:**
```json
{
    "model": "llama3.2",
    "prompt": "What is the capital of France?"
}
```

**Create `testdata/signing/sample_request_chat.json`:**
```json
{
    "model": "llama3.2",
    "messages": [
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "What is the capital of France?"}
    ]
}
```

**Create `testdata/signing/sample_response_generate.json`:**
```json
{
    "model": "llama3.2",
    "response": "The capital of France is Paris.",
    "done": true
}
```

**Create `testdata/signing/sample_response_chat.json`:**
```json
{
    "model": "llama3.2",
    "message": {
        "role": "assistant",
        "content": "The capital of France is Paris."
    },
    "done": true
}
```

### 9.2 Create Unit Tests

**Claude Code Prompt:**
```
Create signing/signing_test.go with comprehensive unit tests:

Test Hasher:
1. TestExtractPromptText_Generate - test /api/generate format
2. TestExtractPromptText_Chat - test /api/chat format with messages array
3. TestExtractPromptText_NonJSON - test plain text fallback
4. TestExtractPromptText_Empty - test empty/invalid JSON
5. TestExtractCompletionText_Generate - test response field
6. TestExtractCompletionText_Chat - test message.content
7. TestExtractCompletionText_OpenAI - test choices[0].message.content
8. TestHashText - verify SHA256 output matches expected
9. TestBuildSigningPayload - verify payload format
10. TestCreateSigningContext - verify complete context creation

Test Client (with httptest mock server):
1. TestSign_Success - mock 200 response with signature
2. TestSign_ServerError - mock 500 response
3. TestSign_Timeout - mock slow response
4. TestSign_InvalidJSON - mock malformed response
5. TestSign_EmptySignature - mock response with empty signature
6. TestIsHealthy_Success - mock 200 on /health
7. TestIsHealthy_Failure - mock error/non-200

Test Headers:
1. TestInjectSigningHeaders_Signed - verify all headers set
2. TestInjectSigningHeaders_Failed - verify error headers
3. TestInjectSigningHeaders_Skipped - verify minimal headers

Use testify/assert for assertions.
Use httptest.NewServer for mock signing server.
Load test fixtures from testdata/signing/.
```

### 9.3 Create Integration Tests

**Claude Code Prompt:**
```
Create signing/integration_test.go with integration tests:

1. TestFullSigningFlow
   - Create mock signing server
   - Create Hasher and Client
   - Process sample request through full flow
   - Verify signature headers would be set correctly

2. TestSigningWithRealJSONFormats
   - Test with actual Ollama request/response formats
   - Verify text extraction works correctly
   - Verify hashes are deterministic

Use build tag: //go:build integration

These tests verify components work together correctly.
```

### 9.4 Run Tests

```bash
# Run all tests
go test ./... -v

# Run with coverage
go test ./... -coverprofile=coverage.out
go tool cover -html=coverage.out -o coverage.html

# Run only signing package tests
go test ./signing/... -v

# Run integration tests
go test ./signing/... -v -tags=integration
```

---

## 10. Phase 7: Documentation & Review

### 10.1 Update README.md

**Claude Code Prompt:**
```
Update README.md to add documentation for the signing feature:

Add new section "## 🔐 Verifiable Message Signing" with:
1. Feature overview
2. How it works (brief explanation)
3. Configuration example in Caddyfile
4. Response headers table
5. Verification guide (link to SecretVM docs)

Add to the Features list at the top:
- **Verifiable Message Signing**: Cryptographic signing of LLM interactions using TEE-secured keys
```

### 10.2 Update ARCHITECTURE.md

**Claude Code Prompt:**
```
Update ARCHITECTURE.md to include signing components:

1. Add signing package to the module architecture diagram
2. Add SigningClient, Hasher to the class diagram
3. Add signing flow to the request processing sequence diagram
4. Document the signing interfaces
```

### 10.3 Code Review Checklist

Before submitting PR:

- [ ] All tests pass: `go test ./... -v`
- [ ] Code builds: `go build ./...`
- [ ] No linting errors: `golangci-lint run`
- [ ] Documentation updated
- [ ] Caddyfile example works
- [ ] Docker build succeeds
- [ ] End-to-end test with real signing server (if available)

---

## 11. Claude Code Quick Reference

### 11.1 Recommended Workflow

When using Claude Code for this implementation:

```bash
# Start Claude Code in the project directory
cd secret-reverse-proxy
claude

# Use these patterns for effective prompts:
```

### 11.2 Effective Prompts

**For creating new files:**
```
Create a new file at signing/types.go with package signing. 
Define the following types with comprehensive documentation comments:
[list types and fields]
Follow the code style used in interfaces/interfaces.go.
```

**For modifying existing files:**
```
Open config/config.go. Find the Config struct and add the following fields 
after the Metering configuration section:
[list fields]
Also update DefaultConfig() to set defaults for these new fields.
Preserve all existing code and formatting.
```

**For implementing methods:**
```
In signing/hasher.go, implement the ExtractPromptText method:
- Input: body []byte, contentType string
- Output: string
- Logic: [describe extraction logic]
- Handle edge cases: [list edge cases]
Add debug logging using zap.Logger.
```

**For adding tests:**
```
Create signing/hasher_test.go with table-driven tests for ExtractPromptText.
Test cases should include:
1. Valid /api/generate format
2. Valid /api/chat format with messages array
3. Non-JSON content type
4. Malformed JSON
5. Missing prompt field
Use testify/assert for assertions.
```

### 11.3 Useful Commands

```bash
# Verify changes compile
go build ./...

# Run specific test
go test -v -run TestExtractPromptText ./signing/...

# Check for issues
golangci-lint run ./signing/...

# View test coverage
go test -coverprofile=c.out ./signing/... && go tool cover -func=c.out

# Build Caddy with module
xcaddy build --with github.com/scrtlabs/secret-reverse-proxy=./

# Test Caddy module is loaded
./caddy list-modules | grep secret
```

### 11.4 Debugging Tips

**If signing isn't working:**
```bash
# Enable debug logging in Caddyfile
{
    debug
}

# Check logs for signing-related messages
docker logs <container> 2>&1 | grep -i sign
```

**If tests fail:**
```bash
# Run with verbose output
go test -v ./signing/... 2>&1 | tee test.log

# Run single test with extra output
go test -v -run TestSign_Success ./signing/... -count=1
```

---

## 12. Troubleshooting

### 12.1 Common Issues

| Issue                        | Cause                      | Solution                                                            |
| ---------------------------- | -------------------------- | ------------------------------------------------------------------- |
| `undefined: signing`         | Import missing             | Add `"github.com/scrtlabs/secret-reverse-proxy/signing"` to imports |
| `cannot use m.signingClient` | Wrong type                 | Ensure Client implements SigningClient interface                    |
| Tests timeout                | Mock server not responding | Check httptest.Server is started before test                        |
| Headers not appearing        | Writer already flushed     | Inject headers before calling next.ServeHTTP or use wrapped writer  |
| JSON parse errors            | Invalid content type       | Check contentType contains "application/json"                       |

### 12.2 Verification Steps

After implementation, verify:

1. **Build succeeds:**
   ```bash
   go build ./...
   ```

2. **Tests pass:**
   ```bash
   go test ./... -v
   ```

3. **Caddy builds with module:**
   ```bash
   xcaddy build --with github.com/scrtlabs/secret-reverse-proxy=./
   ./caddy list-modules | grep secret_reverse_proxy
   ```

4. **Configuration parses:**
   ```bash
   ./caddy validate --config Caddyfile-test
   ```

5. **End-to-end works:**
   ```bash
   # Start Caddy with signing enabled
   # Make request to /api/generate
   # Verify X-Secret-Signature-Status header in response
   curl -v -X POST http://localhost:8080/api/generate \
     -H "Authorization: Bearer test-key" \
     -H "Content-Type: application/json" \
     -d '{"prompt": "Hello"}' 2>&1 | grep X-Secret
   ```

---

## Appendix A: File Templates

### A.1 Complete `signing/types.go`

See Phase 2, Section 5.2 for complete implementation.

### A.2 Complete `signing/headers.go`

```go
// Package signing provides verifiable message signing capabilities.
package signing

import "net/http"

// Header constants for signing metadata in HTTP responses
const (
    // HeaderSignature contains the base64-encoded signature
    HeaderSignature = "X-Secret-Signature"
    
    // HeaderSignatureAlgo indicates the algorithm used (secp256k1 or ed25519)
    HeaderSignatureAlgo = "X-Secret-Signature-Algo"
    
    // HeaderRequestHash contains the hex SHA256 of the prompt text
    HeaderRequestHash = "X-Secret-Request-Hash"
    
    // HeaderResponseHash contains the hex SHA256 of the completion text
    HeaderResponseHash = "X-Secret-Response-Hash"
    
    // HeaderSignatureTime contains the RFC3339 timestamp of signing
    HeaderSignatureTime = "X-Secret-Signature-Timestamp"
    
    // HeaderSignatureStatus indicates the signing result
    HeaderSignatureStatus = "X-Secret-Signature-Status"
    
    // HeaderSignatureError contains error details if signing failed
    HeaderSignatureError = "X-Secret-Signature-Error"
)

// InjectSigningHeaders adds signing-related headers to the HTTP response.
// The headers added depend on the signing outcome status.
func InjectSigningHeaders(w http.ResponseWriter, outcome *SigningOutcome) {
    if outcome == nil {
        return
    }
    
    h := w.Header()
    
    // Always set the status header
    h.Set(HeaderSignatureStatus, outcome.Status)
    
    switch outcome.Status {
    case StatusSigned:
        // Full success - include all headers
        h.Set(HeaderSignature, outcome.Signature)
        h.Set(HeaderSignatureAlgo, outcome.Algorithm)
        h.Set(HeaderRequestHash, outcome.RequestHash)
        h.Set(HeaderResponseHash, outcome.ResponseHash)
        h.Set(HeaderSignatureTime, outcome.Timestamp)
        
    case StatusFailed:
        // Signing failed - include hashes and error for debugging
        h.Set(HeaderRequestHash, outcome.RequestHash)
        h.Set(HeaderResponseHash, outcome.ResponseHash)
        h.Set(HeaderSignatureTime, outcome.Timestamp)
        if outcome.Error != "" {
            h.Set(HeaderSignatureError, outcome.Error)
        }
        
    case StatusSkipped, StatusDisabled:
        // Minimal headers - just the status
        // No additional headers needed
    }
}
```

---

## Appendix B: Caddyfile Examples

### B.1 Minimal Signing Configuration

```caddyfile
:8080 {
    secret_reverse_proxy {
        API_MASTER_KEY {$API_KEY}
        contract_address secret1abc...
        secret_node lcd.secret.example.com
        secret_chain_id secret-4
        cache_ttl 30m
        
        # Enable signing with defaults
        signing_enabled true
    }
    
    reverse_proxy localhost:11434
}
```

### B.2 Full Signing Configuration

```caddyfile
:8080 {
    secret_reverse_proxy {
        # Authentication
        API_MASTER_KEY {$API_KEY}
        master_keys_file /etc/caddy/master_keys.txt
        contract_address secret1abc...
        secret_node lcd.secret.example.com
        secret_chain_id secret-4
        cache_ttl 30m
        
        # Metering
        metering true
        metering_interval 60s
        metering_url https://api.example.com/usage
        
        # Metrics
        enable_metrics true
        metrics_path /metrics
        
        # Signing (all options)
        signing_enabled true
        signing_server http://172.17.0.1:49153
        signing_key_type secp256k1
        signing_timeout 5s
        signing_paths /api/generate /api/chat /v1/chat/completions
    }
    
    reverse_proxy localhost:11434
}
```

---

*End of Implementation Plan*