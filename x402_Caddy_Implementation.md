# x402 Caddy Implementation

This document describes the portal-based x402 payment protocol implementation on the Caddy side. It covers the architecture, request flow, configuration, design decisions, and test plan.

## 1. Architecture Overview

Caddy acts as a **stateless payment-aware proxy** between AI agents and upstream LLM services. It delegates all balance management and billing to DevPortal via a simple service-to-service API.

```
Agent                          Caddy                          DevPortal
  |                              |                                |
  | POST /v1/chat/completions   |                                |
  | Authorization: Bearer <key> |                                |
  |----------------------------->|                                |
  |                              | GET /api/agent/balance         |
  |                              | X-Agent-Service-Key: <svc>    |
  |                              | X-Api-Key: <agent's key>      |
  |                              |------------------------------->|
  |                              |                                |
  |                              | 200 {"balance": "20000"}      |
  |                              |<-------------------------------|
  |                              |                                |
  |                              | [balance >= threshold?]        |
  |                              |   YES -> proxy to LLM         |
  |                              |   NO  -> return 402           |
  |                              |                                |
  | 200 LLM response             |                                |
  |<-----------------------------|                                |
  |                              | POST /api/user/report-usage   |
  |                              | (async, non-blocking)         |
  |                              |------------------------------->|
```

### Components

| File | Component | Responsibility |
|------|-----------|----------------|
| `x402/portal_client.go` | `PortalClient` | HTTP client for DevPortal balance + usage APIs |
| `x402/challenge.go` | `ChallengeBuilder` | Constructs 402 Payment Required responses |
| `x402/types.go` | Types | `BalanceResponse`, `UsageReport`, `Challenge`, header constants, sentinel errors |
| `secret_reverse_proxy.go` | `serveX402()` | Main x402 request handler orchestrating the flow |
| `config/proxy_config.go` | Config fields | `DevPortalURL`, `DevPortalServiceKey`, `X402MinBalanceUSDC`, `X402TopupURL` |
| `validators/api_key_validator.go` | `IsMasterKey()` | Fast master-key-only check (skips contract query) |

## 2. Request Flow (detailed)

### When x402 is enabled

```
1. Request arrives: Authorization: Bearer <api_key>

2. Is it a master key? (IsMasterKey — checks tier 1 + tier 2 only, no contract query)
   ├── YES → Standard forwarding path (metering, proxy, done)
   └── NO  → Enter x402 portal path (serveX402)

3. serveX402:
   a. GET /api/agent/balance from DevPortal
      - Headers: X-Agent-Service-Key, X-Api-Key
      - Portal auto-creates agent with 0 balance if unknown
   b. Compare balance against x402_min_balance_usdc threshold
      - balance < threshold → 402 Payment Required
      - balance >= threshold → continue
   c. Read request body, detect model, count input tokens
   d. Forward to upstream LLM
   e. Count output tokens from response
   f. POST /api/user/report-usage to DevPortal (async goroutine)
   g. Record metrics + accumulate usage for legacy pipeline
```

### When x402 is disabled

The standard API key validation flow applies (master key -> file -> cache -> contract).

### Error Handling

| Condition | Caddy Response |
|-----------|---------------|
| Portal unreachable (network error) | **503** Service Temporarily Unavailable (fail-closed) |
| Portal returns non-200 | **503** Service Temporarily Unavailable |
| Balance < threshold | **402** Payment Required with challenge payload |
| Upstream LLM error | Error propagated to agent |
| Usage report fails | Logged, does not affect agent response (fire-and-forget) |

## 3. API Contract with DevPortal

### Balance Check

```
GET /api/agent/balance

Request Headers:
  X-Agent-Service-Key: <caddy's service key>
  X-Api-Key:           <agent's API key / bearer token>

Response 200:
  { "balance": "20000" }    // USDC minor units as string

Notes:
  - DevPortal maps the API key to the agent's wallet address internally
  - Auto-creates agent record with 0 balance if the API key is unknown
```

### Usage Reporting

```
POST /api/user/report-usage

Request Headers:
  X-Agent-Service-Key: <caddy's service key>
  Content-Type: application/json

Request Body:
  {
    "api_key": "agent-bearer-token",
    "model": "llama-3",
    "input_tokens": 500,
    "output_tokens": 1000
  }

Response 200:
  { "success": true }

Notes:
  - Caddy sends token counts + model only; DevPortal computes cost
  - Sent asynchronously — does not block the LLM response
```

### 402 Payment Required Response (Caddy -> Agent)

```
HTTP/1.1 402 Payment Required
Content-Type: application/json
Payment-Required: x402

{
  "error": "Insufficient balance",
  "balance_usdc": "0.005000",
  "required_usdc": "0.010000",
  "topup_url": "https://devportal.example.com/api/agent/add-funds",
  "topup_amount_usdc": "0.005000"
}

Fields:
  - balance_usdc:      Agent's current balance in USDC
  - required_usdc:     Minimum balance required (from x402_min_balance_usdc config)
  - topup_url:         URL the agent should call to add funds
  - topup_amount_usdc: Deficit (required - balance), minimum needed for next request
```

## 4. Design Gaps in x402-portal.md and Decisions Made

The original design document ([x402-portal.md](./x402-portal.md)) describes the full system flow across Agent, Caddy, and DevPortal. During implementation of the Caddy side, several gaps were identified and addressed.

### Gap 1: Agent Identity Resolution

**Design doc assumption** (Steps 2, 11 in x402-portal.md):
> Caddy sends `x-agent-wallet-address: 0x018b...bd0C` when calling DevPortal.

**Gap**: The design does not specify how Caddy obtains the agent's wallet address. Agents authenticate to Caddy with `Authorization: Bearer <api_key>`, not with wallet-specific headers. Caddy has no mechanism to map API keys to wallet addresses.

**Decision**: Caddy identifies agents by API key, not wallet address. Caddy sends the agent's bearer token to DevPortal via the `X-Api-Key` header. DevPortal resolves the wallet address internally.

**Impact on DevPortal**: The `/api/agent/balance` and `/api/user/report-usage` endpoints must accept `X-Api-Key` as an agent identifier (in addition to or instead of `x-agent-wallet-address`).

### Gap 2: Service-to-Service Authentication Method

**Design doc** (Method 2, lines 69-80):
> Caddy uses `x-agent-service-key` header with a shared secret. No signature required.

**Decision**: Implemented as specified. Caddy sends the `X-Agent-Service-Key` header on every call to DevPortal. This is simpler than the HMAC-SHA256 approach used in the previous implementation and sufficient for service-to-service auth over a private network.

### Gap 3: Usage Reporting Payload

**Design doc** (Step 11, lines 399-428):
> Caddy reports `wallet_address`, `tokens`, and `cost_usdc`.

**Gap**: (a) Caddy does not know the wallet address (see Gap 1). (b) Caddy should not compute `cost_usdc` — the design explicitly places pricing knowledge in DevPortal so prices can be adjusted without redeploying Caddy.

**Decision**: Caddy reports `api_key`, `model`, `input_tokens`, and `output_tokens`. DevPortal computes cost based on its own pricing tables. This keeps Caddy stateless with respect to pricing.

### Gap 4: Balance Threshold

**Design doc** (Step 2, lines 136-139):
> Shows `$0.00 < $0.01 → INSUFFICIENT` as an example but does not specify whether the threshold is configurable.

**Decision**: Made configurable via the `x402_min_balance_usdc` Caddyfile directive. Operators can set the minimum balance required to serve a request (e.g., `"0.01"` = $0.01 = 10,000 USDC minor units).

### Gap 5: Topup Amount Calculation

**Design doc** (Step 3, lines 142-171):
> Shows a fixed `topup_amount_usdc: "0.02"` in the 402 response without explaining how it is calculated.

**Decision**: Caddy computes `topup_amount_usdc` as the deficit: `required_usdc - balance_usdc`. This is the minimum amount the agent needs to add so that their balance meets the threshold. The agent or DevPortal may choose to add more.

### Gap 6: What Happens When DevPortal Is Unreachable

**Design doc**: Does not address this scenario.

**Decision**: Caddy fails **closed** — returns `503 Service Temporarily Unavailable`. No requests are proxied when the billing backend is down. This prevents unpaid usage.

### Gap 7: Master Key Bypass

**Design doc**: Does not address operator/admin access.

**Decision**: Master keys (configured via `API_MASTER_KEY` or `master_keys_file`) bypass the portal balance check entirely. This allows operators to test and monitor without needing a funded agent account. Master key detection uses a fast path (`IsMasterKey`) that checks only tier 1 (config) and tier 2 (file) — no contract queries.

### Gap 8: Pricing and Cost Estimation on Caddy

**Design doc** (Step 10-11):
> Implies Caddy knows the cost of a request (`Cost: $0.01`).

**Decision**: Caddy does **not** compute costs. It reports raw token counts and model name. DevPortal owns all pricing logic. This means Caddy has no pricing config, no quote engine, and no per-model rate tables — a significant simplification over the previous implementation.

## 5. Configuration Reference

### Caddyfile Directives

```caddyfile
secret_reverse_proxy {
    # --- x402 Payment Protocol (portal-based) ---
    x402_enabled          true                                # Enable x402 portal balance checking
    devportal_url         {$DEVPORTAL_URL}                    # DevPortal base URL
    devportal_service_key {$DEVPORTAL_SERVICE_KEY}            # Shared secret for service-to-service auth
    x402_min_balance_usdc "0.01"                              # Minimum balance to serve a request (USDC)
    x402_topup_url        "https://custom.example.com/fund"   # (Optional) Override topup URL in 402 responses
}
```

| Directive | Required | Default | Description |
|-----------|----------|---------|-------------|
| `x402_enabled` | No | `false` | Enable portal-based x402 payment protocol |
| `devportal_url` | Yes (if x402 enabled) | — | Base URL of the DevPortal service |
| `devportal_service_key` | Yes (if x402 enabled) | — | Shared secret sent as `X-Agent-Service-Key` header |
| `x402_min_balance_usdc` | Yes (if x402 enabled) | — | Minimum agent balance in USDC (e.g., `"0.01"` = $0.01) |
| `x402_topup_url` | No | `{devportal_url}/api/agent/add-funds` | URL included in 402 responses for agent top-up |

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `DEVPORTAL_URL` | DevPortal base URL | `https://devportal.example.com` |
| `DEVPORTAL_SERVICE_KEY` | Service-to-service shared secret | `caddy-secret-key-123` |

### Example: Minimal x402 Configuration

```caddyfile
:8080 {
    secret_reverse_proxy {
        API_MASTER_KEY {$SECRET_API_MASTER_KEY}
        x402_enabled true
        devportal_url {$DEVPORTAL_URL}
        devportal_service_key {$DEVPORTAL_SERVICE_KEY}
        x402_min_balance_usdc "0.01"
    }

    reverse_proxy llm-backend:8000
}
```

### Example: Production with All Options

```caddyfile
:443 {
    tls /etc/caddy/cert.pem /etc/caddy/key.pem

    secret_reverse_proxy {
        # Authentication
        API_MASTER_KEY {$SECRET_API_MASTER_KEY}
        master_keys_file /etc/caddy/master_keys.txt

        # x402 portal-based payment
        x402_enabled true
        devportal_url {$DEVPORTAL_URL}
        devportal_service_key {$DEVPORTAL_SERVICE_KEY}
        x402_min_balance_usdc "0.01"
        x402_topup_url "https://pay.example.com/api/agent/add-funds"

        # Metering
        metering true
        metering_interval 5m
        metering_url {$METERING_URL}

        # Metrics
        enable_metrics true
        metrics_path /metrics
    }

    reverse_proxy llm-backend:8000
}
```

## 6. What Changed from the Previous x402 Implementation

The previous x402 implementation used HMAC-SHA256 agent authentication, an in-memory ledger, and a background reconciler. This has been fully replaced.

### Removed Components

| Component | File | Reason |
|-----------|------|--------|
| `AuthVerifierImpl` | `auth_verifier.go` | Agent auth moved to DevPortal (EIP-191 signatures); Caddy uses service key |
| `LedgerImpl` | `ledger.go` | No in-memory balance tracking; DevPortal is source of truth |
| `ReconcilerImpl` | `reconciler.go` | No background sync needed; balance checked per-request |
| `SettlementEngineImpl` | `settlement.go` | No reserve/commit pattern; DevPortal handles billing |
| `SecretVMClientImpl` | `secretvm_client.go` | Replaced by `PortalClient` with simpler service-key auth |
| `QuoteEngineImpl` | `quote_engine.go` | Pricing moved to DevPortal |

### Removed Config Fields

`x402_agent_key`, `x402_agent_address`, `x402_secretvm_url`, `x402_timestamp_skew`, `x402_reconcile_interval`, `x402_default_output_budget`, `x402_pricing_file`, `x402_currency`, `x402_reservation_ttl`

### Removed Headers

`X-Agent-Address`, `X-Agent-Signature`, `X-Agent-Timestamp` (agent-to-Caddy HMAC headers)

### Added Components

| Component | File | Purpose |
|-----------|------|---------|
| `PortalClient` | `portal_client.go` | Simple HTTP client with service-key auth for DevPortal |
| `ChallengeBuilder` | `challenge.go` (rewritten) | USDC-denominated 402 responses with `topup_url` |
| `IsMasterKey()` | `api_key_validator.go` | Fast master-key check to bypass portal for admin access |

## 7. Test Plan

### 7.1 Unit Tests

Located in `secret-reverse-proxy/x402/`:

```bash
cd secret-reverse-proxy
go test -v ./x402/
```

| Test | What It Verifies |
|------|------------------|
| `TestPortalClient_GetBalance_Funded` | Retrieves balance for a funded agent |
| `TestPortalClient_GetBalance_Zero` | Auto-created agent returns 0 balance |
| `TestPortalClient_GetBalance_Unreachable` | Returns `ErrPortalUnreachable` when portal is down |
| `TestPortalClient_ReportUsage` | Usage report delivery with correct payload |
| `TestPortalClient_ServiceKeyHeader` | Service key sent in `X-Agent-Service-Key` header |
| `TestChallengeBuilder_Build402Response` | Correct 402 body, headers, and USDC formatting |
| `TestChallengeBuilder_PartialBalance` | Deficit calculation when agent has partial balance |
| `TestMinorToUSDC` | USDC minor-to-string conversion across edge cases |

### 7.2 Integration Tests

Located in `secret-reverse-proxy/x402_integration_test.go`:

```bash
cd secret-reverse-proxy
go test -v -run TestX402
```

Each test creates a mock portal HTTP server and exercises the full middleware `ServeHTTP` path:

| Test | Scenario | Expected |
|------|----------|----------|
| `TestX402_InsufficientBalance_Returns402` | Agent with 0 balance | 402 + challenge JSON + `Payment-Required: x402` header |
| `TestX402_SufficientBalance_ProxiesRequest` | Agent with $0.02 balance | 200 + LLM response proxied + usage report sent to portal |
| `TestX402_MasterKey_BypassesPortal` | Request with master key (even no portal balance) | 200 + portal never contacted for usage |
| `TestX402_PortalUnreachable_Returns503` | Portal at dead URL | 503 + next handler not called |
| `TestX402_PartialBalance_Returns402WithCorrectDeficit` | Agent with $0.005 (below $0.01 threshold) | 402 + `topup_amount_usdc: "0.005000"` |
| `TestParseUSDCToMinor` | USDC string parsing | `"0.01"` -> 10000, `"1.00"` -> 1000000, etc. |

### 7.3 End-to-End Test with Mock Portal

For testing with Docker against a real Caddy build:

#### Step 1: Create a mock portal server

A minimal Go server that implements the two DevPortal endpoints. The test suite in `x402/portal_client_test.go` contains a `mockPortalServer` that can be extracted into a standalone binary.

Alternatively, use any HTTP server (e.g., `json-server`, a simple Express app, or a Python Flask app) that responds to:

```
GET  /api/agent/balance        -> {"balance": "20000"}
POST /api/user/report-usage    -> {"success": true}
```

#### Step 2: Configure Caddy

Create a `Caddyfile-x402-test`:

```caddyfile
:8080 {
    secret_reverse_proxy {
        API_MASTER_KEY "test-master-key"
        x402_enabled true
        devportal_url "http://mock-portal:3000"
        devportal_service_key "test-service-key"
        x402_min_balance_usdc "0.01"
    }

    reverse_proxy mock-llm:9000
}
```

#### Step 3: Docker Compose test environment

```yaml
version: '3.8'
services:
  caddy:
    build: .
    ports:
      - "8080:8080"
    volumes:
      - ./Caddyfile-x402-test:/etc/caddy/Caddyfile
    depends_on:
      - mock-portal
      - mock-llm

  mock-portal:
    image: your-mock-portal-image
    ports:
      - "3000:3000"

  mock-llm:
    image: your-mock-llm-image
    ports:
      - "9000:9000"
```

#### Step 4: Run test scenarios

```bash
# Test 1: Insufficient balance (mock portal returns balance: "0")
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer unfunded-agent-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama-3","messages":[{"role":"user","content":"hello"}]}' \
  http://localhost:8080/v1/chat/completions
# Expected: 402 with JSON challenge body

# Test 2: Sufficient balance (mock portal returns balance: "20000")
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer funded-agent-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama-3","messages":[{"role":"user","content":"hello"}]}' \
  http://localhost:8080/v1/chat/completions
# Expected: 200 with LLM response

# Test 3: Master key bypass
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer test-master-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"llama-3","messages":[{"role":"user","content":"hello"}]}' \
  http://localhost:8080/v1/chat/completions
# Expected: 200 (no portal balance check)

# Test 4: Portal down (stop the mock portal container)
docker stop mock-portal
curl -s -w "\n%{http_code}" \
  -H "Authorization: Bearer any-key" \
  http://localhost:8080/v1/chat/completions
# Expected: 503
```

### 7.4 Testing with Real DevPortal

Once the DevPortal implementation is ready:

1. **Update Caddyfile** to point `devportal_url` at the real DevPortal
2. **Configure the service key** — the same key must be registered in DevPortal's `AGENT_BALANCE_SERVICE_KEYS`
3. **Create a test agent** via DevPortal (or let Caddy auto-create one by making a request with a new API key)
4. **Fund the agent** through the x402 payment flow (or manually via DevPortal admin)
5. **Send LLM requests** and verify:
   - Unfunded agent gets 402 with correct `topup_url`
   - Funded agent's request is proxied and usage is reported
   - DevPortal shows the reported usage and balance deduction

## 8. Source File Reference

```
secret-reverse-proxy/
  x402/
    types.go              # BalanceResponse, UsageReport, Challenge, headers, errors
    portal_client.go      # PortalClient — HTTP client for DevPortal
    challenge.go          # ChallengeBuilder — 402 response construction
    portal_client_test.go # Unit tests for PortalClient
    challenge_test.go     # Unit tests for ChallengeBuilder + minorToUSDC
  config/
    proxy_config.go       # Config struct with x402 fields
  validators/
    api_key_validator.go  # IsMasterKey() method
  interfaces/
    interfaces.go         # PortalClient + ChallengeBuilder interfaces
  secret_reverse_proxy.go # serveX402(), Provision, Validate, Caddyfile parsing
  x402_integration_test.go # Integration tests with mock portal
```
