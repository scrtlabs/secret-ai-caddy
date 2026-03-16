# x402 Payment Protocol — Implementation Plan

Detailed step-by-step implementation plan for adding x402 support to the Secret AI Caddy middleware. Refers to [architecture.md](./architecture.md) for component specifications and [design.md](./design.md) for the protocol design.

## Prerequisites

- Caddy v2.11.1 (done)
- Go 1.25 (done)
- Familiarity with [architecture.md](./architecture.md)

## Phase Overview

| Phase | Scope | Depends On |
|-------|-------|------------|
| **1** | Types, config, interfaces | — |
| **2** | SpendableLedger + ReservationStore | Phase 1 |
| **3** | AuthVerifier | Phase 1 |
| **4** | QuoteEngine | Phase 1 |
| **5** | ChallengeBuilder | Phase 1 |
| **6** | SettlementEngine | Phases 2, 4 |
| **7** | SecretVMClient | Phase 1 |
| **8** | Reconciler | Phases 2, 7 |
| **9** | Middleware integration (ServeHTTP) | Phases 2–6 |
| **10** | Caddyfile parsing + Provision + Validate | Phases 1, 9 |
| **11** | Metrics extension | Phase 9 |
| **12** | Tests | All |
| **13** | Docker + deployment | All |

Phases 2–5 can be developed in parallel once Phase 1 is complete. Phase 6 depends on 2 and 4. Phase 9 ties everything together.

---

## Phase 1: Types, Config, Interfaces

**Goal:** Define all shared types so subsequent phases can compile against stable contracts.

### Step 1.1 — Create `x402/types.go`

```go
package x402

import "time"

// Quote is the cost estimate returned by the QuoteEngine.
type Quote struct {
    EstimatedInputTokens  int
    EstimatedOutputTokens int
    InputCost             int64
    OutputCost            int64
    TotalCost             int64
    Model                 string
    Currency              string
}

// Reservation tracks an in-flight spend hold.
type Reservation struct {
    ID           string
    AgentAddress string
    Amount       int64
    CreatedAt    time.Time
    Model        string
}

// LedgerEntry is the per-agent balance state.
type LedgerEntry struct {
    Balance   int64
    Reserved  int64
    UpdatedAt time.Time
}

// Challenge is the 402 response payload.
type Challenge struct {
    AgentAddress   string `json:"agent_address"`
    RequiredAmount int64  `json:"required_amount"`
    Currency       string `json:"currency"`
    PaymentURL     string `json:"payment_url"`
    ChallengeRef   string `json:"challenge_ref"`
    Message        string `json:"message"`
}

// SettlementResult is the outcome of finalizing a reservation.
type SettlementResult struct {
    ReservationID string
    AgentAddress  string
    EstimatedCost int64
    ActualCost    int64
    Refunded      int64
    InputTokens   int
    OutputTokens  int
    Model         string
}

// ModelPricing holds per-model cost rates.
type ModelPricing struct {
    InputCostPer1kTokens  int64 `json:"input_cost_per_1k_tokens"`
    OutputCostPer1kTokens int64 `json:"output_cost_per_1k_tokens"`
}

// PricingConfig is the top-level pricing file structure.
type PricingConfig struct {
    Default ModelPricing            `json:"default"`
    Models  map[string]ModelPricing `json:"models"`
}

// Sentinel errors.
var (
    ErrInsufficientBalance = errors.New("insufficient balance")
    ErrReservationNotFound = errors.New("reservation not found")
    ErrReservationExpired  = errors.New("reservation expired")
    ErrInvalidSignature    = errors.New("invalid agent signature")
    ErrStaleTimestamp      = errors.New("timestamp outside allowed skew")
    ErrUnknownAgent        = errors.New("unknown agent address")
)
```

### Step 1.2 — Extend `config/proxy_config.go`

Add the following fields to the `Config` struct:

```go
// x402 Payment Protocol
X402Enabled             bool          `json:"x402_enabled,omitempty"`
X402AgentKey            string        `json:"x402_agent_key,omitempty"`
X402AgentAddress        string        `json:"x402_agent_address,omitempty"`
X402SecretVMURL         string        `json:"x402_secretvm_url,omitempty"`
X402PaymentURL          string        `json:"x402_payment_url,omitempty"`
X402TimestampSkew       time.Duration `json:"x402_timestamp_skew,omitempty"`
X402ReconcileInterval   time.Duration `json:"x402_reconcile_interval,omitempty"`
X402DefaultOutputBudget int           `json:"x402_default_output_budget,omitempty"`
X402PricingFile         string        `json:"x402_pricing_file,omitempty"`
X402Currency            string        `json:"x402_currency,omitempty"`
X402ReservationTTL      time.Duration `json:"x402_reservation_ttl,omitempty"`
```

Set defaults in the config constructor / Provision:

```go
if cfg.X402TimestampSkew == 0 {
    cfg.X402TimestampSkew = 5 * time.Minute
}
if cfg.X402ReconcileInterval == 0 {
    cfg.X402ReconcileInterval = 30 * time.Second
}
if cfg.X402DefaultOutputBudget == 0 {
    cfg.X402DefaultOutputBudget = 4096
}
if cfg.X402Currency == "" {
    cfg.X402Currency = "uscrt"
}
if cfg.X402ReservationTTL == 0 {
    cfg.X402ReservationTTL = 5 * time.Minute
}
```

### Step 1.3 — Extend `interfaces/interfaces.go`

Add interfaces for all x402 components as specified in [architecture.md §3](./architecture.md#3-component-specifications). This lets phases 2–8 implement against stable contracts.

```go
// AuthVerifier validates x-agent-* headers.
type AuthVerifier interface {
    IsAgentRequest(r *http.Request) bool
    Verify(r *http.Request, body []byte) (agentAddress string, err error)
}

// QuoteEngine estimates request cost.
type QuoteEngine interface {
    Estimate(model string, inputTokens int, maxOutputTokens int) (*x402.Quote, error)
}

// SpendableLedger manages per-agent balances.
type SpendableLedger interface {
    Credit(agentAddress string, amount int64) error
    Reserve(agentAddress string, amount int64) (reservationID string, err error)
    Commit(reservationID string, actualAmount int64) error
    Release(reservationID string) error
    GetBalance(agentAddress string) (*x402.LedgerEntry, error)
    Snapshot() map[string]x402.LedgerEntry
}

// SettlementEngine finalizes reservations.
type SettlementEngine interface {
    Settle(reservationID string, inputTokens, outputTokens int, model string) (*x402.SettlementResult, error)
    Cancel(reservationID string) error
}

// ChallengeBuilder constructs 402 responses.
type ChallengeBuilder interface {
    Build402Response(w http.ResponseWriter, agentAddress string, requiredAmount int64) error
}

// SecretVMClient communicates with SecretVM agent APIs.
type SecretVMClient interface {
    GetBalance(agentAddress string) (balance int64, err error)
    AddFunds(agentAddress string, amount int64) error
    GetVMStatus(vmID string) (status string, err error)
}

// Reconciler syncs balances from billing backend to ledger.
type Reconciler interface {
    Start(interval time.Duration)
    Stop()
    ForceSync(agentAddress string) error
}
```

### Step 1.4 — Extend `Middleware` struct

Add fields to the `Middleware` struct in `secret_reverse_proxy.go`:

```go
// x402 components (nil when x402 is disabled)
authVerifier      interfaces.AuthVerifier
quoteEngine       interfaces.QuoteEngine
ledger            interfaces.SpendableLedger
settlementEngine  interfaces.SettlementEngine
challengeBuilder  interfaces.ChallengeBuilder
secretVMClient    interfaces.SecretVMClient
reconciler        interfaces.Reconciler
```

**Files to create/modify:**
- Create: `x402/types.go`
- Modify: `config/proxy_config.go`
- Modify: `interfaces/interfaces.go`
- Modify: `secret_reverse_proxy.go` (struct fields only)

---

## Phase 2: SpendableLedger + ReservationStore

**Goal:** Thread-safe in-memory balance management.

### Step 2.1 — Create `x402/ledger.go`

Implement `SpendableLedger` interface.

**Key implementation details:**

- Use `sync.RWMutex` for the top-level agents map.
- Each `agentEntry` has its own `sync.Mutex` for reserve/commit/release to allow concurrent operations on different agents.
- Reservation IDs generated with `xid` (already a dependency) for short, sortable, unique IDs.
- Reservations stored in `map[string]*Reservation` within each agent entry.

```go
type agentEntry struct {
    mu           sync.Mutex
    balance      int64
    reservations map[string]*Reservation
    updatedAt    time.Time
}

type ledgerImpl struct {
    mu      sync.RWMutex
    agents  map[string]*agentEntry
    ttl     time.Duration
    logger  *zap.Logger
}
```

**Methods to implement:**
- `Credit` — lock agent, add to balance, update timestamp.
- `Reserve` — lock agent, check `balance - sumReservations >= amount`, create reservation, return ID.
- `Commit` — lock agent, find reservation, deduct `actualAmount` from balance, delete reservation.
- `Release` — lock agent, find reservation, delete it (balance unchanged since we track reservations separately).
- `GetBalance` — read-lock top map, lock agent, return snapshot.
- `Snapshot` — read-lock, iterate, build copy.

### Step 2.2 — Reservation cleanup goroutine

Add `StartCleanup(interval time.Duration)` and `StopCleanup()` methods. The cleanup goroutine iterates all agents, removes reservations older than TTL, and logs warnings (these indicate dropped connections or bugs).

**Files to create:**
- `x402/ledger.go`

**Tests to write:**
- `x402/ledger_test.go` — concurrent reserve/commit/release, insufficient balance, reservation expiry, credit, snapshot.

---

## Phase 3: AuthVerifier

**Goal:** Validate x-agent-* headers and extract agent identity.

### Step 3.1 — Create `x402/auth_verifier.go`

**Header constants:**
```go
const (
    HeaderAgentAddress   = "X-Agent-Address"
    HeaderAgentSignature = "X-Agent-Signature"
    HeaderAgentTimestamp  = "X-Agent-Timestamp"
)
```

**`IsAgentRequest`:** Check for presence of `X-Agent-Address` header.

**`Verify` implementation:**
1. Extract all three headers. If any missing, return error.
2. Parse timestamp as RFC3339. Check `|now - timestamp| < skew`. If stale, return `ErrStaleTimestamp`.
3. Reconstruct canonical payload:
   ```
   METHOD + "\n" + PATH_ONLY + "\n" + BODY + "\n" + TIMESTAMP
   ```
   Where `PATH_ONLY` is `r.URL.Path` (no query string), and `BODY` is the raw bytes passed as parameter.
4. Compute HMAC-SHA256 of the canonical payload using the agent's secret key.
5. Compare with the provided signature (constant-time comparison via `hmac.Equal`).
6. If valid, return the agent address from the header.

**Agent key resolution:** For the initial implementation, a single shared agent key is configured via `x402_agent_key`. Multi-agent key lookup can be added later (Phase 2+) via a key registry.

**Files to create:**
- `x402/auth_verifier.go`

**Tests:**
- `x402/auth_verifier_test.go` — valid signature, wrong signature, stale timestamp, missing headers.

---

## Phase 4: QuoteEngine

**Goal:** Estimate request cost from model, input tokens, and output budget.

### Step 4.1 — Create `x402/quote_engine.go`

**Initialization:**
- Load pricing from `x402_pricing_file` (JSON). If no file, use built-in defaults.
- Store as `PricingConfig` struct.

**`Estimate` implementation:**
1. Look up model in `PricingConfig.Models`. If not found, use `PricingConfig.Default`.
2. Compute:
   ```
   inputCost  = (inputTokens * pricing.InputCostPer1kTokens) / 1000
   outputCost = (maxOutputTokens * pricing.OutputCostPer1kTokens) / 1000
   totalCost  = inputCost + outputCost
   ```
3. Use integer math throughout (no floating point) to avoid rounding issues. Round up using `(tokens * cost + 999) / 1000`.
4. Return `Quote`.

**Pricing refresh:** The Reconciler can call `ReloadPricing()` to hot-reload pricing without restart.

**Files to create:**
- `x402/quote_engine.go`

**Tests:**
- `x402/quote_engine_test.go` — known model pricing, default fallback, zero tokens, large values, reload.

---

## Phase 5: ChallengeBuilder

**Goal:** Construct 402 Payment Required responses.

### Step 5.1 — Create `x402/challenge.go`

**`Build402Response` implementation:**
1. Generate a challenge reference (UUID or xid).
2. Build `Challenge` struct with agent address, required amount, currency, payment URL from config, and a human-readable message.
3. Marshal to JSON.
4. Set headers:
   ```
   Content-Type: application/json
   X-Payment-Required: true
   ```
5. Write 402 status and body.

**Files to create:**
- `x402/challenge.go`

**Tests:**
- `x402/challenge_test.go` — verify status code, JSON structure, headers.

---

## Phase 6: SettlementEngine

**Goal:** Finalize reservations with actual usage.

### Step 6.1 — Create `x402/settlement.go`

**Dependencies:** SpendableLedger (Phase 2), QuoteEngine (Phase 4).

**`Settle` implementation:**
1. Reprice using `QuoteEngine.Estimate(model, inputTokens, outputTokens)` — note: for settlement we use actual output tokens, not the budget.
2. Call `Ledger.Commit(reservationID, actualCost)`.
3. Build `SettlementResult` with estimated cost (from reservation), actual cost, and refunded difference.
4. Log structured settlement event.
5. Return result.

**`Cancel` implementation:**
1. Call `Ledger.Release(reservationID)`.
2. Log cancellation.

**Files to create:**
- `x402/settlement.go`

**Tests:**
- `x402/settlement_test.go` — settle with less than reserved, settle with exact amount, cancel, settlement on expired reservation.

---

## Phase 7: SecretVMClient

**Goal:** Authenticated HTTP client for SecretVM agent APIs.

### Step 7.1 — Create `x402/secretvm_client.go`

**Request signing:**
```go
func (c *secretVMClient) buildSignedRequest(method, path string, body []byte) (*http.Request, error) {
    timestamp := time.Now().UTC().Format(time.RFC3339)
    canonical := method + "\n" + path + "\n" + string(body) + "\n" + timestamp
    mac := hmac.New(sha256.New, []byte(c.agentKey))
    mac.Write([]byte(canonical))
    signature := hex.EncodeToString(mac.Sum(nil))

    req, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(body))
    // ... set headers:
    // X-Agent-Address: c.agentAddress
    // X-Agent-Signature: signature
    // X-Agent-Timestamp: timestamp
    return req, err
}
```

**Methods:**
- `GetBalance` — `GET /api/agent/balance`, parse JSON response.
- `AddFunds` — `POST /api/agent/add-funds`, handle 402 retry with payment headers.
- `GetVMStatus` — `GET /api/agent/vm/:id`, parse status field.

**HTTP client:** Use a shared `http.Client` with reasonable timeouts (10s connect, 30s total).

**Files to create:**
- `x402/secretvm_client.go`

**Tests:**
- `x402/secretvm_client_test.go` — use `httptest.NewServer` to mock SecretVM. Test signing, balance fetch, add-funds 402 flow.

---

## Phase 8: Reconciler

**Goal:** Background sync of balances from SecretVM / billing backend into the ledger.

### Step 8.1 — Create `x402/reconciler.go`

**`Start` implementation:**
1. Launch goroutine with ticker at `reconcileInterval`.
2. On each tick:
   a. Get list of known agents from `Ledger.Snapshot()`.
   b. For each agent, call `SecretVMClient.GetBalance(agent)`.
   c. Compute delta = `remoteBalance - (ledger.Balance + ledger.Reserved)`.
   d. If delta > 0, call `Ledger.Credit(agent, delta)`.
   e. If delta < 0, log warning (possible overspend, needs investigation).
3. On stop signal, exit cleanly.

**`ForceSync`:** Same logic but for a single agent, called synchronously.

**Error handling:** Individual agent sync failures are logged but don't halt the loop. A persistent failure counter triggers an alert-level log after N consecutive failures.

**Files to create:**
- `x402/reconciler.go`

**Tests:**
- `x402/reconciler_test.go` — mock SecretVMClient, verify credits applied, verify error resilience.

---

## Phase 9: Middleware Integration

**Goal:** Wire all x402 components into `ServeHTTP`.

### Step 9.1 — Add x402 branch to `ServeHTTP`

In `secret_reverse_proxy.go`, after the URL blocking check and before the existing API key extraction, add:

```go
// x402 agent authentication path
if m.authVerifier != nil && m.authVerifier.IsAgentRequest(r) {
    return m.serveX402(w, r, next)
}

// ... existing API key path continues below ...
```

### Step 9.2 — Implement `serveX402` method

New method on `Middleware`:

```go
func (m *Middleware) serveX402(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
    // 1. Read body (reuse existing BodyHandler)
    bodyInfo, err := m.bodyHandler.SafeReadRequestBody(r)
    if err != nil {
        return caddyhttp.Error(http.StatusBadRequest, err)
    }

    // 2. Verify agent signature
    agentAddress, err := m.authVerifier.Verify(r, []byte(bodyInfo.Content))
    if err != nil {
        if errors.Is(err, x402.ErrInvalidSignature) || errors.Is(err, x402.ErrStaleTimestamp) {
            return caddyhttp.Error(http.StatusUnauthorized, err)
        }
        return caddyhttp.Error(http.StatusInternalServerError, err)
    }

    // 3. Detect model and count input tokens
    model := detectModelFromRequestBody(bodyInfo.Content, bodyInfo.ContentType)
    inputTokens := 0
    if m.tokenCounter != nil {
        inputTokens = m.tokenCounter.CountTokensWithModel(bodyInfo.Content, bodyInfo.ContentType, model)
    }

    // 4. Determine output budget
    maxOutput := m.Config.X402DefaultOutputBudget
    if bodyInfo.ParsedJSON != nil {
        if mt, ok := bodyInfo.ParsedJSON["max_tokens"]; ok {
            if mtInt, ok := mt.(float64); ok && int(mtInt) > 0 {
                maxOutput = int(mtInt)
            }
        }
    }

    // 5. Get quote
    quote, err := m.quoteEngine.Estimate(model, inputTokens, maxOutput)
    if err != nil {
        m.logger.Error("Quote estimation failed", zap.Error(err))
        return caddyhttp.Error(http.StatusInternalServerError, err)
    }

    // 6. Reserve
    reservationID, err := m.ledger.Reserve(agentAddress, quote.TotalCost)
    if err != nil {
        if errors.Is(err, x402.ErrInsufficientBalance) {
            return m.challengeBuilder.Build402Response(w, agentAddress, quote.TotalCost)
        }
        return caddyhttp.Error(http.StatusInternalServerError, err)
    }

    // 7. Forward to upstream with response capture
    tw := NewTokenMeteringResponseWriter(w)
    err = next.ServeHTTP(tw, r)

    // 8. Handle upstream errors
    if err != nil || tw.Status() >= 500 {
        m.settlementEngine.Cancel(reservationID)
        return err
    }

    // 9. Count output tokens
    outputTokens := 0
    if m.tokenCounter != nil {
        respBody := tw.Body()
        respContentType := tw.Header().Get("Content-Type")
        outputTokens = m.tokenCounter.CountTokensWithModel(string(respBody), respContentType, model)
    }

    // 10. Settle
    result, err := m.settlementEngine.Settle(reservationID, inputTokens, outputTokens, model)
    if err != nil {
        m.logger.Error("Settlement failed", zap.Error(err), zap.String("reservation", reservationID))
        // Fail-open: response already sent to client
    }

    // 11. Record usage through existing pipeline
    if m.tokenAccumulator != nil && result != nil {
        apiKeyHash := hashString(agentAddress)
        m.tokenAccumulator.RecordUsageWithModel(apiKeyHash, model, inputTokens, outputTokens)
    }

    // 12. Record metrics
    if m.metricsCollector != nil {
        m.metricsCollector.RecordRequest()
        m.metricsCollector.RecordAuthorized()
        m.metricsCollector.RecordTokens(int64(inputTokens), int64(outputTokens))
    }

    return nil
}
```

### Step 9.3 — Helper: `hashString`

```go
func hashString(s string) string {
    h := sha256.Sum256([]byte(s))
    return hex.EncodeToString(h[:])
}
```

**Files to modify:**
- `secret_reverse_proxy.go`

---

## Phase 10: Caddyfile Parsing + Provision + Validate

### Step 10.1 — Extend `UnmarshalCaddyfile`

Add cases for each `x402_*` directive in the existing `switch` block:

```go
case "x402_enabled":
    // parse bool
case "x402_agent_key":
    // parse string, expand env vars
case "x402_agent_address":
    // parse string, expand env vars
case "x402_secretvm_url":
    // parse string, expand env vars
case "x402_payment_url":
    // parse string, expand env vars
case "x402_timestamp_skew":
    // parse duration
case "x402_reconcile_interval":
    // parse duration
case "x402_default_output_budget":
    // parse int
case "x402_pricing_file":
    // parse file path
case "x402_currency":
    // parse string
case "x402_reservation_ttl":
    // parse duration
```

### Step 10.2 — Extend `Provision`

After existing component initialization, add:

```go
if m.Config.X402Enabled {
    m.logger.Info("Initializing x402 payment protocol components")

    m.authVerifier = x402.NewAuthVerifier(m.Config.X402AgentKey, m.Config.X402TimestampSkew, m.logger)

    pricing, err := x402.LoadPricing(m.Config.X402PricingFile)
    if err != nil {
        return fmt.Errorf("failed to load x402 pricing: %w", err)
    }
    m.quoteEngine = x402.NewQuoteEngine(pricing, m.Config.X402Currency, m.Config.X402DefaultOutputBudget)

    m.ledger = x402.NewSpendableLedger(m.Config.X402ReservationTTL, m.logger)
    m.ledger.StartCleanup(1 * time.Minute)

    m.challengeBuilder = x402.NewChallengeBuilder(m.Config.X402PaymentURL, m.Config.X402Currency)

    m.settlementEngine = x402.NewSettlementEngine(m.ledger, m.quoteEngine, m.logger)

    m.secretVMClient = x402.NewSecretVMClient(
        m.Config.X402SecretVMURL,
        m.Config.X402AgentKey,
        m.Config.X402AgentAddress,
        m.logger,
    )

    m.reconciler = x402.NewReconciler(m.ledger, m.secretVMClient, m.logger)
    m.reconciler.Start(m.Config.X402ReconcileInterval)

    m.logger.Info("x402 payment protocol initialized",
        zap.String("agent_address", m.Config.X402AgentAddress),
        zap.String("secretvm_url", m.Config.X402SecretVMURL),
        zap.Duration("reconcile_interval", m.Config.X402ReconcileInterval),
    )
}
```

### Step 10.3 — Extend `Validate`

```go
if m.Config.X402Enabled {
    if m.Config.X402AgentKey == "" {
        return fmt.Errorf("x402_agent_key is required when x402 is enabled")
    }
    if m.Config.X402AgentAddress == "" {
        return fmt.Errorf("x402_agent_address is required when x402 is enabled")
    }
    if m.Config.X402SecretVMURL == "" {
        return fmt.Errorf("x402_secretvm_url is required when x402 is enabled")
    }
    if m.Config.X402PaymentURL == "" {
        return fmt.Errorf("x402_payment_url is required when x402 is enabled")
    }
}
```

### Step 10.4 — Extend `Cleanup`

```go
if m.reconciler != nil {
    m.reconciler.Stop()
}
if m.ledger != nil {
    m.ledger.StopCleanup()
}
```

**Files to modify:**
- `secret_reverse_proxy.go` (UnmarshalCaddyfile, Provision, Validate, Cleanup)

---

## Phase 11: Metrics Extension

### Step 11.1 — Add x402 metrics to MetricsCollector

Add fields to the metrics collector in `factories/factories.go`:

```go
x402Reservations       int64
x402ReservationsDenied int64
x402Settlements        int64
x402TotalEstimated     int64
x402TotalActual        int64
x402TotalRefunded      int64
x402Challenges         int64
x402ReconcileSuccess   int64
x402ReconcileFailed    int64
```

Add corresponding `Record*` methods and extend `GetMetrics()` to include an `x402` key in the output JSON.

### Step 11.2 — Wire metrics into x402 components

- `serveX402`: call `RecordReservation()` / `RecordReservationDenied()` / `RecordSettlement()` / `RecordChallenge()` at appropriate points.
- `Reconciler`: call `RecordReconciliation(success)` after each sync.

**Files to modify:**
- `interfaces/interfaces.go` (extend MetricsCollector interface)
- `factories/factories.go` (implement new methods)
- `secret_reverse_proxy.go` (wire calls in serveX402)
- `x402/reconciler.go` (wire reconciliation metrics)

---

## Phase 12: Tests

### Unit Tests

| File | Tests |
|------|-------|
| `x402/ledger_test.go` | Concurrent reserve/commit/release, insufficient balance, expiry cleanup, credit, snapshot |
| `x402/auth_verifier_test.go` | Valid/invalid signature, stale timestamp, missing headers, constant-time comparison |
| `x402/quote_engine_test.go` | Known model, default fallback, zero tokens, rounding, reload |
| `x402/challenge_test.go` | Status code, JSON body structure, headers |
| `x402/settlement_test.go` | Normal settle, partial refund, cancel, expired reservation |
| `x402/secretvm_client_test.go` | Request signing, balance fetch, add-funds 402 retry (httptest) |
| `x402/reconciler_test.go` | Credit applied, error resilience, force sync |

### Integration Tests

| Test | Description |
|------|-------------|
| `x402_integration_test.go` | Full request lifecycle: auth → quote → reserve → forward → settle. Uses mock upstream and mock SecretVM. |
| `x402_402_flow_test.go` | Insufficient balance → 402 → credit → retry → success |
| `x402_streaming_test.go` | Streaming response with reserve/settle, verify refund on short response |
| `x402_legacy_coexistence_test.go` | Verify legacy API key requests still work when x402 is enabled |
| `x402_provision_test.go` | Config parsing, provision with valid/invalid config, validate errors |

### Test Utilities

Create `x402/testutil_test.go` with:
- Mock SecretVM server (`httptest.Server`)
- Mock upstream AI server
- Helper to build signed requests
- Helper to assert 402 challenge structure

---

## Phase 13: Docker + Deployment

### Step 13.1 — No Dockerfile changes needed

The x402 package is pure Go within the existing module. The existing `xcaddy build` picks it up automatically.

### Step 13.2 — Update `docker-compose.yaml` for testing

Add x402 env vars to the caddy service:

```yaml
environment:
  # ... existing vars ...
  - X402_ENABLED=${X402_ENABLED:-false}
  - X402_AGENT_KEY=${X402_AGENT_KEY}
  - X402_AGENT_ADDRESS=${X402_AGENT_ADDRESS}
  - X402_SECRETVM_URL=${X402_SECRETVM_URL}
  - X402_PAYMENT_URL=${X402_PAYMENT_URL}
```

### Step 13.3 — Update `Caddyfile-test`

Add x402 directives (disabled by default):

```caddyfile
secret_reverse_proxy {
    # ... existing config ...

    # x402 (disabled by default for testing)
    x402_enabled {env.X402_ENABLED}
    x402_agent_key {env.X402_AGENT_KEY}
    x402_agent_address {env.X402_AGENT_ADDRESS}
    x402_secretvm_url {env.X402_SECRETVM_URL}
    x402_payment_url {env.X402_PAYMENT_URL}
}
```

### Step 13.4 — Create sample pricing file

Create `config/pricing.json`:

```json
{
  "default": {
    "input_cost_per_1k_tokens": 10,
    "output_cost_per_1k_tokens": 30
  },
  "models": {
    "llama3.3:70b": {
      "input_cost_per_1k_tokens": 15,
      "output_cost_per_1k_tokens": 45
    },
    "mistral-7b": {
      "input_cost_per_1k_tokens": 8,
      "output_cost_per_1k_tokens": 24
    }
  }
}
```

---

## File Summary

### New Files

| File | Phase | Description |
|------|-------|-------------|
| `x402/types.go` | 1 | Shared types, errors, constants |
| `x402/ledger.go` | 2 | SpendableLedger + ReservationStore |
| `x402/auth_verifier.go` | 3 | Agent signature verification |
| `x402/quote_engine.go` | 4 | Cost estimation |
| `x402/challenge.go` | 5 | 402 response builder |
| `x402/settlement.go` | 6 | Post-response settlement |
| `x402/secretvm_client.go` | 7 | SecretVM API client |
| `x402/reconciler.go` | 8 | Background balance sync |
| `x402/ledger_test.go` | 12 | Ledger unit tests |
| `x402/auth_verifier_test.go` | 12 | AuthVerifier unit tests |
| `x402/quote_engine_test.go` | 12 | QuoteEngine unit tests |
| `x402/challenge_test.go` | 12 | ChallengeBuilder unit tests |
| `x402/settlement_test.go` | 12 | SettlementEngine unit tests |
| `x402/secretvm_client_test.go` | 12 | SecretVMClient unit tests |
| `x402/reconciler_test.go` | 12 | Reconciler unit tests |
| `x402/testutil_test.go` | 12 | Shared test helpers |
| `config/pricing.json` | 13 | Sample pricing file |

### Modified Files

| File | Phase | Change |
|------|-------|--------|
| `config/proxy_config.go` | 1 | Add x402 config fields |
| `interfaces/interfaces.go` | 1, 11 | Add x402 interfaces, extend MetricsCollector |
| `secret_reverse_proxy.go` | 1, 9, 10 | Add struct fields, serveX402 method, caddyfile parsing, provision, validate, cleanup |
| `factories/factories.go` | 11 | Extend MetricsCollector with x402 metrics |
| `docker-compose.yaml` | 13 | Add x402 env vars |
| `Caddyfile-test` | 13 | Add x402 directives |

---

## Implementation Order (Recommended)

For a single developer, the recommended order minimizes context switching:

1. **Phase 1** — Types + config + interfaces (foundation, ~1 session)
2. **Phase 2** — Ledger (core data structure, test heavily, ~1 session)
3. **Phase 3** — AuthVerifier (~0.5 session)
4. **Phase 4** — QuoteEngine (~0.5 session)
5. **Phase 5** — ChallengeBuilder (~0.5 session)
6. **Phase 6** — SettlementEngine (~0.5 session)
7. **Phase 9** — Middleware integration — serveX402 (~1 session)
8. **Phase 10** — Caddyfile parsing + provision + validate (~0.5 session)
9. **Phase 7** — SecretVMClient (~1 session)
10. **Phase 8** — Reconciler (~0.5 session)
11. **Phase 11** — Metrics (~0.5 session)
12. **Phase 12** — Integration tests (~1 session)
13. **Phase 13** — Docker + deployment config (~0.5 session)

Total: ~8 sessions of focused work.

Note: Phases 7–8 (SecretVMClient + Reconciler) are pushed later because the core request lifecycle (phases 2–6, 9–10) can be developed and tested with a manually credited ledger before the reconciler is wired up.
