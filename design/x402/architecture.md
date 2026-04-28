# x402 Payment Protocol — Architecture

This document defines the architectural changes to the Secret AI Caddy middleware required to support the x402 prepaid metering and payment protocol as described in [design.md](./design.md).

## 1. Design Principles

1. **Hot-path latency matters** — The SpendableLedger is an in-memory cache. Caddy never calls SecretVM balance APIs on the critical request path.
2. **Reserve-then-settle** — Every request reserves estimated cost before forwarding. Actual cost is settled after the response completes. Unused reserve is refunded atomically.
3. **Caddy does not hold wallets** — Payment resolution is delegated to the Portal/Agent SDK. Caddy only issues 402 challenges and enforces balance.
4. **Existing auth is preserved** — The current API key validation pipeline (master key → file → cache → contract) remains. x402 agent auth is an additional, parallel authentication path selected by header presence.
5. **Fail-open for metering, fail-closed for balance** — Token counting errors are logged but don't block requests. Insufficient balance always blocks.

## 2. Component Map

The x402 subsystem introduces seven new components alongside the existing middleware. Each maps directly to the module layout proposed in design.md.

```
secret-reverse-proxy/
├── x402/                          # NEW package
│   ├── auth_verifier.go           # Agent signature verification
│   ├── quote_engine.go            # Request cost estimation
│   ├── reservation.go             # Reserve / commit / release lifecycle
│   ├── ledger.go                  # In-memory spendable balance store
│   ├── settlement.go              # Post-response charge + refund
│   ├── challenge.go               # 402 response builder
│   ├── secretvm_client.go         # Authenticated SecretVM API client
│   ├── reconciler.go              # Background sync: billing → ledger
│   └── types.go                   # Shared types, errors, constants
├── config/
│   └── proxy_config.go            # Extended with x402 fields
├── interfaces/
│   └── interfaces.go              # Extended with x402 interfaces
├── factories/
│   └── factories.go               # Extended with x402 factory methods
└── secret_reverse_proxy.go        # Extended ServeHTTP with x402 branch
```

## 3. Component Specifications

### 3.1 AuthVerifier

**Purpose:** Validate `x-agent-address`, `x-agent-signature`, and `x-agent-timestamp` headers on incoming requests.

**Responsibilities:**
- Detect whether a request uses x402 agent auth (presence of `x-agent-address` header) vs. legacy API key auth.
- Reconstruct the canonical signing payload: `METHOD + "\n" + PATH + "\n" + BODY + "\n" + TIMESTAMP`.
- Verify the HMAC-SHA256 signature against the agent's known secret (resolved via SecretVM or local cache).
- Reject requests with stale timestamps (configurable window, default 5 minutes).
- Return the verified agent address for downstream ledger lookups.

**Interface:**
```go
type AuthVerifier interface {
    // IsAgentRequest returns true if the request carries x-agent-* headers.
    IsAgentRequest(r *http.Request) bool

    // Verify validates the agent signature and returns the agent address.
    // Returns an error if signature is invalid, timestamp is stale, or
    // the agent is unknown.
    Verify(r *http.Request, body []byte) (agentAddress string, err error)
}
```

**Integration point:** Called at the top of `ServeHTTP`, before the existing API key extraction. If `IsAgentRequest` returns true, the request takes the x402 path; otherwise the legacy path runs unchanged.

### 3.2 QuoteEngine

**Purpose:** Estimate the cost of a request before it is forwarded upstream.

**Responsibilities:**
- Parse the request body to extract model name and input content.
- Look up per-model pricing (tokens-per-unit cost, stored in config or fetched from a pricing source).
- Estimate input token count using the existing `TokenCounter`.
- Estimate output token budget from `max_tokens` in the request body, or fall back to a configurable default.
- Return a `Quote` containing the estimated total cost in the ledger's unit of account.

**Interface:**
```go
type Quote struct {
    EstimatedInputTokens  int
    EstimatedOutputTokens int
    InputCost             int64   // in smallest unit (e.g., uscrt or credits)
    OutputCost            int64
    TotalCost             int64
    Model                 string
    Currency              string
}

type QuoteEngine interface {
    // Estimate returns a cost estimate for the given request.
    Estimate(model string, inputTokens int, maxOutputTokens int) (*Quote, error)
}
```

**Design decision:** Pricing data is loaded from config at startup and can be refreshed by the Reconciler. The QuoteEngine is stateless — it reads pricing tables but does not mutate them.

### 3.3 SpendableLedger

**Purpose:** In-memory, thread-safe store of per-agent spendable balances. This is the single authority Caddy consults on the hot path.

**Responsibilities:**
- Track available (spendable) balance per agent address — this is the amount not currently held by any reservation.
- Support atomic credit (top-up sync), reserve, commit, and release operations.
- Provide a read-only snapshot for the metrics endpoint.

**Balance semantics:** `Balance` represents the **available** funds (excluding any reserved amounts). When a reservation is created, `Balance` is decremented by the reserved amount. When a reservation is released (cancelled), `Balance` is incremented back. When a reservation is committed, the difference between the reserved amount and the actual charge is refunded to `Balance`. The `Reserved` field in `LedgerEntry` is a read-only sum of all outstanding reservation amounts, computed on demand for observability.

**Interface:**
```go
type LedgerEntry struct {
    Balance    int64     // available funds (not including reserved amounts)
    Reserved   int64     // sum of outstanding reservations (read-only, for observability)
    UpdatedAt  time.Time
}

type SpendableLedger interface {
    // Credit adds funds to an agent's available balance (called by Reconciler).
    Credit(agentAddress string, amount int64) error

    // Reserve attempts to hold `amount` from the agent's available balance.
    // Checks `balance >= amount`, deducts from balance, creates reservation.
    // Returns a reservation ID or an ErrInsufficientBalance.
    Reserve(agentAddress string, amount int64) (reservationID string, err error)

    // Commit finalizes a reservation. Refunds `reservedAmount - actualAmount`
    // back to the agent's available balance, then deletes the reservation.
    Commit(reservationID string, actualAmount int64) error

    // Release cancels a reservation, returning the full reserved amount
    // back to the agent's available balance.
    Release(reservationID string) error

    // GetBalance returns the current balance and reserved total for an agent.
    GetBalance(agentAddress string) (*LedgerEntry, error)

    // Snapshot returns a copy of all balances for metrics/debugging.
    Snapshot() map[string]LedgerEntry
}
```

**Concurrency:** Uses `sync.RWMutex` for balance reads and a fine-grained per-agent lock for reserve/commit/release to avoid global contention under high RPS.

**Persistence:** The ledger is volatile. On restart, the Reconciler re-syncs balances from the billing backend and agents are lazily hydrated on first request (see §3.8). This is acceptable because reservations are short-lived (request duration) and the billing backend is the durable source of truth.

### 3.4 ReservationStore

**Purpose:** Track individual in-flight reservations so they can be committed or released after the upstream response completes.

**Responsibilities:**
- Generate unique reservation IDs.
- Map reservation ID → (agent address, reserved amount, created timestamp).
- Expire stale reservations (safety net for dropped connections).
- Provide stats for metrics.

**Embedded within SpendableLedger** — not a separate service, but a sub-structure. Reservations are stored in a `map[string]*Reservation` guarded by the ledger's lock.

```go
type Reservation struct {
    ID            string
    AgentAddress  string
    Amount        int64
    CreatedAt     time.Time
    Model         string
}
```

### 3.5 SettlementEngine

**Purpose:** Post-response settlement — charges the actual cost and refunds unused reserves.

**Responsibilities:**
- Accept the reservation ID, actual input/output token counts, and the model.
- Re-price using the QuoteEngine to get the actual cost.
- Call `Ledger.Commit(reservationID, actualCost)`.
- Emit a structured usage event for the audit trail.
- Record token metrics via the existing `MetricsCollector` and `TokenAccumulator`.

**Interface:**
```go
type SettlementResult struct {
    ReservationID    string
    AgentAddress     string
    EstimatedCost    int64
    ActualCost       int64
    Refunded         int64
    InputTokens      int
    OutputTokens     int
    Model            string
}

type SettlementEngine interface {
    // Settle finalizes a reservation with actual usage.
    Settle(reservationID string, inputTokens, outputTokens int, model string) (*SettlementResult, error)

    // Cancel releases a reservation without charging (e.g., upstream error).
    Cancel(reservationID string) error
}
```

### 3.6 ChallengeBuilder

**Purpose:** Construct machine-readable HTTP 402 responses when a reservation is denied.

**Responsibilities:**
- Build a JSON body with funding instructions, required amount, agent address, and a challenge reference.
- Set appropriate headers (`Content-Type: application/json`, `X-Payment-Required: true`).
- Include a `payment_url` pointing to the Portal's funding endpoint.

**Interface:**
```go
type Challenge struct {
    AgentAddress   string `json:"agent_address"`
    RequiredAmount int64  `json:"required_amount"`
    Currency       string `json:"currency"`
    PaymentURL     string `json:"payment_url"`
    ChallengeRef   string `json:"challenge_ref"`
    Message        string `json:"message"`
}

type ChallengeBuilder interface {
    // Build402Response writes a 402 response to the writer.
    Build402Response(w http.ResponseWriter, agentAddress string, requiredAmount int64) error
}
```

### 3.7 SecretVMClient

**Purpose:** Authenticated HTTP client for SecretVM agent APIs. Used by the Reconciler (control plane), not on the hot path.

**Responsibilities:**
- Build signed requests with `x-agent-address`, `x-agent-signature`, `x-agent-timestamp` headers.
- Implement `GetBalance()`, `AddFunds()`, `CreateVM()`, `GetVMStatus()`.
- Handle the add-funds 402 retry flow (first call → 402 → attach payment → retry).

**Interface:**
```go
type SecretVMClient interface {
    GetBalance(agentAddress string) (balance int64, err error)
    AddFunds(agentAddress string, amount int64) error
    GetVMStatus(vmID string) (status string, err error)
    // ListAgents returns all agent addresses with funded balances.
    // Used by the Reconciler on startup to pre-populate the ledger.
    ListAgents() ([]string, error)
}
```

**Signing:** Constructs canonical payload per the SecretVM spec: `METHOD + "\n" + PATH_ONLY + "\n" + EXACT_BODY + "\n" + TIMESTAMP`. Signs with the agent's private key. The private key is loaded from config (env var or file path) and held in memory.

### 3.8 Reconciler

**Purpose:** Background goroutine that keeps the SpendableLedger in sync with the billing backend / SecretVM balance API.

**Responsibilities:**
- On startup: fetch balances for all known agents and credit the ledger.
- Periodically (configurable interval, default 30s): re-sync balances for agents already in the ledger.
- Listen for credit events from the billing backend (webhook or polling).
- Log discrepancies between ledger state and upstream balance.
- **Lazy hydration:** When `ForceSync` is called for an agent not yet in the ledger, fetch their balance from the billing backend and credit the ledger. This handles the cold-start problem — after a restart the ledger is empty, so the first request from any agent triggers a `ForceSync` before a 402 is issued.

**Interface:**
```go
type Reconciler interface {
    Start(interval time.Duration)
    Stop()
    // ForceSync triggers an immediate reconciliation for a specific agent.
    // If the agent is not yet in the ledger, fetches their balance from
    // the billing backend and credits the ledger (lazy hydration).
    ForceSync(agentAddress string) error
}
```

**Integration:** Started in `Provision()`, stopped in `Cleanup()`. Runs alongside the existing `ResilientReporter`.

## 4. Revised Request Lifecycle

```
                         ┌─────────────────────────────────────────────┐
                         │              ServeHTTP                       │
                         └──────────────────┬──────────────────────────┘
                                            │
                                 ┌──────────▼──────────┐
                                 │  Metrics endpoint?   │──yes──▶ ServeMetrics
                                 └──────────┬──────────┘
                                            │ no
                                 ┌──────────▼──────────┐
                                 │  Blocked URL?        │──yes──▶ 403
                                 └──────────┬──────────┘
                                            │ no
                                 ┌──────────▼──────────┐
                                 │  IsAgentRequest?     │
                                 └───┬─────────────┬───┘
                                     │             │
                                yes  │             │  no
                                     ▼             ▼
                         ┌───────────────┐  ┌──────────────────┐
                         │ x402 Path     │  │ Legacy API Key   │
                         │               │  │ Path (unchanged) │
                         └───────┬───────┘  └──────────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  AuthVerifier.Verify()   │
                    └────────────┬────────────┘
                                 │
                          ┌──────▼──────┐
                          │ valid?      │──no──▶ 401 Unauthorized
                          └──────┬──────┘
                                 │ yes
                    ┌────────────▼────────────┐
                    │  Read body + detect     │
                    │  model + count input    │
                    │  tokens                 │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  QuoteEngine.Estimate() │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  Ledger.Reserve()       │
                    └────────────┬────────────┘
                                 │
                          ┌──────▼──────┐
                          │ reserved?   │──no──▶ ChallengeBuilder → 402
                          └──────┬──────┘
                                 │ yes
                    ┌────────────▼────────────┐
                    │  Forward to upstream    │
                    │  (next.ServeHTTP)       │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  Count output tokens    │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  SettlementEngine       │
                    │  .Settle()              │
                    │  (charge actual,        │
                    │   refund unused)        │
                    └────────────┬────────────┘
                                 │
                    ┌────────────▼────────────┐
                    │  Record metrics +       │
                    │  audit event            │
                    └────────────┬────────────┘
                                 │
                                 ▼
                            Return response
```

## 5. Streaming Support

For streaming endpoints (`/chat/completions` with `stream: true`), the flow has two differences:

1. **Output budget estimation:** The reserve uses `max_tokens` from the request (or a configurable cap) as the output estimate, since we don't know the final output size.

2. **Chunk-level metering:** The `TokenMeteringResponseWriter` already buffers response bytes. For streaming, we continue to buffer and count after the stream ends. We do **not** do mid-stream balance checks — the initial reservation covers the maximum output. If the actual output is less, the difference is refunded at settlement.

3. **Memory bound:** To prevent unbounded memory growth on large streaming responses, the response writer applies a configurable buffer cap (default: `MaxBodySize`, typically 5MB). Token counting operates on the buffered portion; if the response exceeds the cap, the buffer is marked as truncated and output tokens are estimated from the buffered content. This trades accuracy for safety on very large responses.

This avoids the complexity of mid-stream cancellation while still bounding cost exposure via the reservation cap.

## 6. Configuration Additions

New fields in `Config` (all optional — x402 is disabled if `x402_enabled` is not set):

| Directive | Type | Description | Default |
|-----------|------|-------------|---------|
| `x402_enabled` | bool | Enable x402 payment enforcement | `false` |
| `x402_agent_key` | string | Agent private key (hex) or `{env.VAR}` | Required if enabled |
| `x402_agent_address` | string | Agent public address | Required if enabled |
| `x402_secretvm_url` | string | SecretVM API base URL | Required if enabled |
| `x402_payment_url` | string | Portal payment URL for 402 challenges | Required if enabled |
| `x402_timestamp_skew` | duration | Max allowed timestamp drift | `5m` |
| `x402_reconcile_interval` | duration | Ledger sync interval | `30s` |
| `x402_default_output_budget` | int | Default max output tokens for quoting | `4096` |
| `x402_pricing_file` | path | JSON file with per-model pricing | `""` |
| `x402_currency` | string | Unit of account for pricing | `"uscrt"` |
| `x402_reservation_ttl` | duration | Max reservation lifetime before auto-release | `5m` |

**Caddyfile example:**
```caddyfile
secret_reverse_proxy {
    # Existing config...
    API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
    metering true

    # x402 config
    x402_enabled true
    x402_agent_key {env.X402_AGENT_KEY}
    x402_agent_address {env.X402_AGENT_ADDRESS}
    x402_secretvm_url {env.X402_SECRETVM_URL}
    x402_payment_url {env.X402_PAYMENT_URL}
    x402_timestamp_skew 5m
    x402_reconcile_interval 30s
    x402_default_output_budget 4096
    x402_pricing_file /etc/caddy/pricing.json
    x402_currency uscrt
    x402_reservation_ttl 5m
}
```

## 7. Pricing File Format

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

## 8. Error Handling

| Scenario | Behavior |
|----------|----------|
| Invalid agent signature | 401 Unauthorized |
| Stale timestamp (> skew window) | 401 Unauthorized |
| Unknown agent (no ledger entry) | Lazy hydration: ForceSync from billing backend, retry reserve; if still insufficient → 402 Payment Required |
| Insufficient balance | 402 Payment Required |
| Upstream error (5xx) | Release reservation (no charge), forward error to client |
| Upstream timeout | Release reservation, 504 to client |
| Settlement fails (internal) | Log error, release reservation, return response (fail-open for the client, reconcile later) |
| Reconciler sync failure | Log error, keep stale ledger, retry on next interval |
| Reservation TTL expired | Auto-release via background cleanup |

## 9. Metrics Additions

The existing `MetricsCollector` interface is extended with:

```go
// x402 metrics
RecordReservation()
RecordReservationDenied()
RecordSettlement(estimatedCost, actualCost int64)
RecordChallenge()
RecordReconciliation(success bool)
```

Exposed at `/metrics` under a new `x402` key:

```json
{
  "x402": {
    "reservations_total": 15420,
    "reservations_denied": 23,
    "settlements_total": 15397,
    "total_estimated_cost": 462000,
    "total_actual_cost": 389000,
    "total_refunded": 73000,
    "challenges_issued": 23,
    "reconciliations_success": 4820,
    "reconciliations_failed": 2,
    "active_reservations": 12,
    "ledger_agents": 847
  }
}
```

## 10. Interaction with Existing Components

| Existing Component | Change Required |
|--------------------|-----------------|
| `Config` | Add x402 fields (backward compatible — all optional) |
| `ServeHTTP` | Add `IsAgentRequest` branch at top; rest of legacy path untouched |
| `TokenCounter` | No change — reused by QuoteEngine for input estimation |
| `BodyHandler` | No change — reused for body reading |
| `TokenAccumulator` | No change — settlement records usage through it |
| `ResilientReporter` | No change — continues to report accumulated usage |
| `MetricsCollector` | Extend interface with x402 metrics |
| `APIKeyValidator` | No change — legacy path only |
| `UnmarshalCaddyfile` | Add parsing for `x402_*` directives |
| `Provision` | Initialize x402 components when enabled |
| `Cleanup` | Stop Reconciler |
| `Validate` | Validate x402 config when enabled |

## 11. Security Considerations

1. **Agent key storage:** The `x402_agent_key` should be passed via environment variable, never hardcoded. The middleware holds it in memory only.
2. **Replay protection:** Timestamp skew window limits replay attacks. Each signature is bound to the exact request body, so body modification invalidates it.
3. **Ledger manipulation:** The SpendableLedger is only credited by the Reconciler (which authenticates against the billing backend). There is no external API to directly credit the ledger.
4. **Reservation exhaustion:** The reservation TTL and per-agent balance cap prevent an attacker from locking up funds indefinitely.
5. **Audit trail:** Every settlement produces a structured log event with reservation ID, agent, estimated/actual cost, and model — enabling forensic analysis.
