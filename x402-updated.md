# x402 Update — Implementation Summary

## Overview

This update introduces the **x402 payment protocol** into the Caddy reverse proxy middleware.  
x402 is an agent-native billing layer: instead of a user API key, an AI agent signs each request  
with an EIP-191 Ethereum signature and pays per-request from its on-chain wallet balance.

---

## Architecture: Two Request Paths

```
                ┌─────────────────────────────────────────────────────────┐
                │                  Caddy middleware                        │
                │                                                          │
  Agent request │  X-Agent-Address header present?                        │
  ─────────────►│          Yes → serveX402()                              │
                │          No  → ServeHTTP() (legacy user API key path)   │
                └─────────────────────────────────────────────────────────┘
```

### x402 Agent Path (`serveX402`)

```
 1. Read raw request body (needed for EIP-191 signature)
 2. Verify EIP-191 signature  →  401 if invalid
 3. Detect model from body    →  if no "model" field: skip billing, proxy directly
 4. [LLM requests only] Check agent balance via portal  →  402 if insufficient
 5. Forward request to upstream LLM
 6. Count tokens from response (prompt_tokens + completion_tokens)
 7. Report usage to portal (async goroutine, non-blocking)
```

### Legacy User Path (`ServeHTTP`)

```
 1. Validate API key (master key or permit contract)
 2. Detect model from body    →  if no "model" field: skip balance check
 3. [LLM requests only] Check user balance via portal  →  402 if insufficient
 4. Forward request to upstream LLM
 5. Count tokens from response
 6. Report usage to portal (metering pipeline, batched)
```

---

## Key Design Decisions

### Model detection gates balance check

Non-LLM requests (`GET /v1/models`, health checks, `/api/tags`, embeddings without a `model` field)  
bypass the balance check entirely. The model is detected from the raw request body  
**before** any portal API call is made.

- Request body contains `"model": "llama-..."` → balance check applies
- No `"model"` field (or non-JSON body) → balance check skipped, request proxied directly

This means a zero-balance agent wallet can always call `GET /v1/models` and get a 200.

### EIP-191 signature validation

The agent signs: `keccak256(method + path + sha256(body) + timestamp)`  
Timestamp must be within ±60 seconds of server time to prevent replay attacks.  
Signature is recovered on-chain-style (secp256k1) and compared to `X-Agent-Address`.

### Async usage reporting

`ReportUsage` is called in a goroutine after the response is written — it does not block  
the user-facing latency. Failed reports are retried (up to `max_retries` attempts).

---

## New Caddyfile Directives

Add these to each `secret_reverse_proxy` block that should enforce x402 billing:

```caddyfile
secret_reverse_proxy {
    # ... existing config ...

    # x402 Payment Protocol (add these four lines)
    x402_enabled       {env.X402_ENABLED}
    devportal_url      {env.DEVPORTAL_URL}
    devportal_service_key {env.DEVPORTAL_SERVICE_KEY}
    x402_min_balance_usd {env.X402_MIN_BALANCE_USD}
}
```

---

## New Environment Variables

Add to the Caddy service's env file (`usr/.env` on SecretVM, `local/.env` locally):

| Variable                | Description                                      | Example                          |
|-------------------------|--------------------------------------------------|----------------------------------|
| `X402_ENABLED`          | Enable x402 agent payment path (`true`/`false`) | `true`                           |
| `DEVPORTAL_URL`         | Base URL of the DevPortal API                    | `https://aidev.scrtlabs.com`     |
| `DEVPORTAL_SERVICE_KEY` | Service key for portal balance/report-usage APIs | `<secret — never commit>`        |
| `X402_MIN_BALANCE_USD`  | Minimum agent balance required per request (USD) | `0.001`                          |

---

## Portal API Integration

### Balance check (per request, before forwarding)

`GET {DEVPORTAL_URL}/api/agent/balance`  
Auth: `X-Agent-Service-Key: hmac_sha256(DEVPORTAL_SERVICE_KEY, wallet.toLowerCase())`

Response:
```json
{ "balance": 10.52 }
```
Returns 402 if `balance < X402_MIN_BALANCE_USD`.

### Usage reporting (after response, async)

`POST {DEVPORTAL_URL}/api/user/report-usage`  
Auth: `X-Agent-Service-Key: {DEVPORTAL_SERVICE_KEY}` (raw key)  
Body:
```json
{
  "wallet_address": "0xABCD...",
  "usage_data": [{
    "model": "llama-3.3-70b-instruct",
    "input_tokens": 25,
    "output_tokens": 28,
    "timestamp": 1700000000
  }]
}
```

---

## New Files

| File | Description |
|------|-------------|
| `secret-reverse-proxy/x402/portal_client.go` | HTTP client for DevPortal balance + report-usage APIs |
| `secret-reverse-proxy/x402/challenge.go` | EIP-191 signature verification |
| `secret-reverse-proxy/x402/types.go` | Request/response types |
| `secret-reverse-proxy/x402_integration_test.go` | Integration tests for x402 flow |
| `test/mock-llm/server.mjs` | Local mock LLM (Node.js) — realistic OpenAI responses with token counts |
| `test/mock-llm/Dockerfile` | Container for mock LLM (used in `docker-compose.yaml`) |

---

## Modified Files

| File | Change |
|------|--------|
| `secret-reverse-proxy/secret_reverse_proxy.go` | `serveX402()` added; `ServeHTTP()` extended with user balance check; model detection gating |
| `secret-reverse-proxy/config/proxy_config.go` | New fields: `X402Enabled`, `DevportalURL`, `DevportalServiceKey`, `X402MinBalanceUSD` |
| `Caddyfile-test` | x402 directives added; `permit_file` removed (uses env vars) |
| `docker-compose.yaml` | mock-llm built from `./test/mock-llm`; x402 env vars added (no hardcoded defaults) |

---

## What Was NOT Changed

- STT (`:25436`) and TTS (`:25435`) endpoints — x402 billing applies to LLM only
- Existing legacy API key validation — runs as-before when `X-Agent-Address` is absent
- Metering pipeline — still batches token usage and reports to portal on interval
- Permit/contract-based auth — still supported alongside x402

---

## Security Notes

- `DEVPORTAL_SERVICE_KEY` must never be committed to git — always use env file
- Signature replay protection: ±60s timestamp window
- Balance check result is not cached — checked fresh on every LLM request
- `ReportUsage` failure is logged but does not cause a 5xx to the agent (fail-open billing)
