# 🔄 x402 Payment Flow - Visual Guide

## 🏗️ System Architecture

```
┌────────────────────────────────────────────────────────────────────────────┐
│                          x402 Payment System                               │
├──────────────────────┬──────────────────────┬──────────────────────────────┤
│                      │                      │                              │
│   🤖 Agent           │   🔀 Caddy           │   💰 DevPortal               │
│   (AI Client)        │   (LLM Proxy)        │   (Billing Service)          │
│                      │                      │                              │
│  • Wallet:           │  • Routes requests   │  • Manages balances          │
│    0x018b...bd0C     │  • Checks balance    │  • Processes x402 payments   │
│  • Makes LLM         │  • Reports usage     │  • Tracks usage              │
│    requests          │  • Returns 402 when  │  • Auto-creates agents       │
│  • Pays via x402     │    insufficient      │                              │
│                      │                      │                              │
└──────────────────────┴──────────────────────┴──────────────────────────────┘
```

## 📋 Flow Summary

```
┌─────────┐                                              ┌────────────┐
│  Agent  │                                              │ DevPortal  │
└────┬────┘                                              └─────┬──────┘
     │                                                         │
     │  1️⃣  Request LLM → Caddy                               │
     │  2️⃣  Caddy checks balance → DevPortal                  │
     │  3️⃣  DevPortal returns balance: $0.00                  │
     │  4️⃣  Caddy returns 402 Payment Required                │
     │  5️⃣  Agent calls /api/agent/add-funds (no payment) ───>│
     │  6️⃣  DevPortal returns 402 with x402 requirements      │
     │  7️⃣  Agent creates x402 payment signature              │
     │  8️⃣  Agent retries /api/agent/add-funds (with payment)>│
     │  9️⃣  DevPortal validates & credits balance             │
     │  🔟  Agent retries LLM request → Caddy                  │
     │  1️⃣1️⃣  Caddy checks balance → DevPortal (now $0.02)    │
     │  1️⃣2️⃣  Caddy processes LLM & reports usage             │
     │  1️⃣3️⃣  Agent receives LLM response                     │
     │                                                         │
```

## 🔐 Authentication Methods

### Method 1: Agent Direct Auth (EIP-191 Signature)

Used when Agent calls DevPortal directly:

```
┌───────────────────────────────────────────────────────────────┐
│ Headers:                                                      │
│ ┌───────────────────────────────────────────────────────────┐ │
│ │ x-agent-address: 0x018b1623D14d75ca271A9F9b7324183035E... │ │
│ │ x-agent-signature: 0xbe092635...c6341b                    │ │
│ │ x-agent-timestamp: 1746115257407                          │ │
│ └───────────────────────────────────────────────────────────┘ │
│                                                               │
│ Signature Calculation:                                        │
│ message = `${method}:${path}:${timestamp}:${sha256(body)}`   │
│ signature = wallet.signMessage(message)                      │
└───────────────────────────────────────────────────────────────┘
```

### Method 2: Service-to-Service Auth

Used when Caddy calls DevPortal:

```
┌───────────────────────────────────────────────────────────────┐
│ Headers:                                                      │
│ ┌───────────────────────────────────────────────────────────┐ │
│ │ x-agent-service-key: caddy-secret-key-123                 │ │
│ │ x-agent-wallet-address: 0x018b1623D14d75ca271A9F9b73...  │ │
│ └───────────────────────────────────────────────────────────┘ │
│                                                               │
│ No signature required - uses shared secret                   │
└───────────────────────────────────────────────────────────────┘
```

## 📊 Detailed Flow Diagram

### Step 1: Agent Requests LLM (Insufficient Balance)

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ POST /v1/chat/       │                        │
  │ completions          │                        │
  ├─────────────────────>│                        │
  │                      │                        │
  │ Headers:             │                        │
  │ • Authorization:     │                        │
  │   Bearer <api_key>   │                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "model": "gpt-4",  │                        │
  │   "messages": [...]  │                        │
  │ }                    │                        │
  │                      │                        │
```

### Step 2: Caddy Checks Balance

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │                      │ GET /api/agent/balance │
  │                      ├───────────────────────>│
  │                      │                        │
  │                      │ Headers:               │
  │                      │ • x-agent-service-key: │
  │                      │   caddy-key-123        │
  │                      │ • x-agent-wallet-      │
  │                      │   address:             │
  │                      │   0x018b...bd0C        │
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ Auto-create  │
  │                      │                        │ │ agent if not │
  │                      │                        │ │ exists with  │
  │                      │                        │ │ balance = 0  │
  │                      │                        │ └──────────────┘
  │                      │                        │
  │                      │ 200 OK                 │
  │                      │<───────────────────────┤
  │                      │                        │
  │                      │ {                      │
  │                      │   "balance": "0",      │
  │                      │   "vms": []            │
  │                      │ }                      │
  │                      │                        │
  │                      │ ┌────────────────────┐ │
  │                      │ │ Balance Check:     │ │
  │                      │ │ $0.00 < $0.01      │ │
  │                      │ │ ❌ INSUFFICIENT    │ │
  │                      │ └────────────────────┘ │
```

### Step 3: Caddy Returns 402

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ 402 Payment Required │                        │
  │<─────────────────────┤                        │
  │                      │                        │
  │ Headers:             │                        │
  │ • Payment-Required:  │                        │
  │   x402               │                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "error":           │                        │
  │     "Insufficient    │                        │
  │      balance",       │                        │
  │   "balance_usdc":    │                        │
  │     "0.00",          │                        │
  │   "required_usdc":   │                        │
  │     "0.01",          │                        │
  │   "topup_url":       │                        │
  │     "https://        │                        │
  │      devportal.com/  │                        │
  │      api/agent/      │                        │
  │      add-funds",     │                        │
  │   "topup_amount_     │                        │
  │    usdc": "0.02"     │                        │
  │ }                    │                        │
```

### Step 4: Agent Requests Topup (Initial - No Payment)

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ POST /api/agent/add-funds                    │
  ├─────────────────────────────────────────────>│
  │                      │                        │
  │ Headers:             │                        │
  │ • x-agent-address:   │                        │
  │   0x018b...bd0C      │                        │
  │ • x-agent-signature: │                        │
  │   0xabc123...        │                        │
  │ • x-agent-timestamp: │                        │
  │   1746115257407      │                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "amount_usdc":     │                        │
  │     "0.02"           │                        │
  │ }                    │                        │
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ Verify EIP-  │
  │                      │                        │ │ 191 signature│
  │                      │                        │ └──────────────┘
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ No x402      │
  │                      │                        │ │ payment found│
  │                      │                        │ └──────────────┘
```

### Step 5: DevPortal Returns 402 with x402 Requirements

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ 402 Payment Required │                        │
  │<─────────────────────────────────────────────┤
  │                      │                        │
  │ Headers:             │                        │
  │ • Payment-Required:  │                        │
  │   eyJ4NDAyVmVyc2lvbi│                        │
  │   I6MiwiZXJyb3IiOi..│                        │
  │   (base64 encoded)   │                        │
  │ • Accept-Payment:    │                        │
  │   exact-evm          │                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "status":          │                        │
  │     "payment_        │                        │
  │      required",      │                        │
  │   "amount_usdc":     │                        │
  │     0.02,            │                        │
  │   "message":         │                        │
  │     "x402 USDC       │                        │
  │      payment         │                        │
  │      required"       │                        │
  │ }                    │                        │
```

### Step 6: Payment-Required Header Decoded

```
┌───────────────────────────────────────────────────────────────┐
│ Payment-Required Header (decoded):                           │
│                                                               │
│ {                                                             │
│   "x402Version": 2,                                           │
│   "resource": {                                               │
│     "url": "http://localhost:3000/api/agent/add-funds",      │
│     "description": "Agent top-up via x402 USDC payment"      │
│   },                                                          │
│   "accepts": [{                                               │
│     "scheme": "exact",                                        │
│     "network": "eip155:8453",        // Base mainnet          │
│     "amount": "20000",               // 0.02 USDC             │
│     "asset": "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",   │
│     "payTo": "0x959257a565d5Fb44724Bc322e83CAF8c8AaB3E8b",   │
│     "maxTimeoutSeconds": 300,                                 │
│     "extra": {                                                │
│       "symbol": "USDC",                                       │
│       "name": "USD Coin",                                     │
│       "version": "2",                                         │
│       "decimals": 6,                                          │
│       "assetTransferMethod": "eip3009"                        │
│     }                                                         │
│   }]                                                          │
│ }                                                             │
└───────────────────────────────────────────────────────────────┘
```

### Step 7: Agent Creates x402 Payment Signature

```
┌───────────────────────────────────────────────────────────────┐
│ Agent creates EIP-3009 transferWithAuthorization signature:   │
│                                                               │
│ 1. Parse payment requirements from Payment-Required header   │
│ 2. Create authorization object:                              │
│    {                                                          │
│      from: "0x018b1623D14d75ca271A9F9b7324183035E5bd0C",     │
│      to: "0x959257a565d5Fb44724Bc322e83CAF8c8AaB3E8b",       │
│      value: "20000",                                          │
│      validAfter: "1746115257",                                │
│      validBefore: "1746115857",                               │
│      nonce: "0x2335acf7b2479b279d61a52c41bb35b291a703ab..."  │
│    }                                                          │
│ 3. Sign with wallet private key                              │
│ 4. Encode as base64 PAYMENT-SIGNATURE header                 │
└───────────────────────────────────────────────────────────────┘
```

### Step 8: Agent Retries with Payment Signature

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ POST /api/agent/add-funds                    │
  ├─────────────────────────────────────────────>│
  │                      │                        │
  │ Headers:             │                        │
  │ • x-agent-address:   │                        │
  │   0x018b...bd0C      │                        │
  │ • x-agent-signature: │                        │
  │   0xdef456...        │                        │
  │ • x-agent-timestamp: │                        │
  │   1746115258500      │                        │
  │ • PAYMENT-SIGNATURE: │                        │
  │   eyJ4NDAyVmVyc2lvbi│                        │
  │   I6MiwicGF5bG9hZCI6│                        │
  │   ...                │                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "amount_usdc":     │                        │
  │     "0.02"           │                        │
  │ }                    │                        │
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ 1. Verify    │
  │                      │                        │ │    EIP-191   │
  │                      │                        │ │    signature │
  │                      │                        │ └──────────────┘
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ 2. Validate  │
  │                      │                        │ │    x402      │
  │                      │                        │ │    payment   │
  │                      │                        │ └──────────────┘
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ 3. Verify    │
  │                      │                        │ │    USDC auth │
  │                      │                        │ │    on Base   │
  │                      │                        │ └──────────────┘
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ 4. Credit    │
  │                      │                        │ │    balance:  │
  │                      │                        │ │    0 + 20000 │
  │                      │                        │ │    = 20000   │
  │                      │                        │ └──────────────┘
```

### Step 9: Payment Successful

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ 200 OK               │                        │
  │<─────────────────────────────────────────────┤
  │                      │                        │
  │ Headers:             │                        │
  │ • Payment-Settle:    │                        │
  │   eyJzdWNjZXNzIjp0  │                        │
  │   cnVlLCJoZWFkZXJzI │                        │
  │   jp7fX0=            │                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "balance": "20000",│                        │
  │   "payment_method":  │                        │
  │     "x402"           │                        │
  │ }                    │                        │
  │                      │                        │
  │ ┌──────────────────┐ │                        │
  │ │ ✅ Payment       │ │                        │
  │ │    Success!      │ │                        │
  │ │ Balance: $0.02   │ │                        │
  │ └──────────────────┘ │                        │
```

### Step 10: Agent Retries LLM Request

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ POST /v1/chat/       │                        │
  │ completions          │                        │
  ├─────────────────────>│                        │
  │                      │                        │
  │                      │ GET /api/agent/balance │
  │                      ├───────────────────────>│
  │                      │                        │
  │                      │ 200 OK                 │
  │                      │<───────────────────────┤
  │                      │                        │
  │                      │ {                      │
  │                      │   "balance": "20000"   │
  │                      │ }                      │
  │                      │                        │
  │                      │ ┌────────────────────┐ │
  │                      │ │ $0.02 >= $0.01     │ │
  │                      │ │ ✅ SUFFICIENT      │ │
  │                      │ └────────────────────┘ │
  │                      │                        │
  │                      │ ┌────────────────────┐ │
  │                      │ │ 🤖 Process LLM     │ │
  │                      │ │ Tokens: 1500       │ │
  │                      │ │ Cost: $0.01        │ │
  │                      │ └────────────────────┘ │
```

### Step 11: Caddy Reports Usage

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │                      │ POST /api/user/        │
  │                      │ report-usage           │
  │                      ├───────────────────────>│
  │                      │                        │
  │                      │ Headers:               │
  │                      │ • x-agent-service-key: │
  │                      │   caddy-key-123        │
  │                      │                        │
  │                      │ Body:                  │
  │                      │ {                      │
  │                      │   "wallet_address":    │
  │                      │     "0x018b...bd0C",   │
  │                      │   "tokens": 1500,      │
  │                      │   "cost_usdc": 0.01    │
  │                      │ }                      │
  │                      │                        │
  │                      │                        │ ┌──────────────┐
  │                      │                        │ │ Debit:       │
  │                      │                        │ │ 20000 - 10000│
  │                      │                        │ │ = 10000      │
  │                      │                        │ └──────────────┘
  │                      │                        │
  │                      │ 200 OK                 │
  │                      │<───────────────────────┤
```

### Step 12: Agent Receives LLM Response

```
Agent                  Caddy                  DevPortal
  │                      │                        │
  │ 200 OK               │                        │
  │<─────────────────────┤                        │
  │                      │                        │
  │ Body:                │                        │
  │ {                    │                        │
  │   "id":              │                        │
  │     "chatcmpl-123",  │                        │
  │   "choices": [{      │                        │
  │     "message": {     │                        │
  │       "role":        │                        │
  │         "assistant", │                        │
  │       "content":     │                        │
  │         "Hello! ..." │                        │
  │     }                │                        │
  │   }],                │                        │
  │   "usage": {         │                        │
  │     "total_tokens":  │                        │
  │       1500,          │                        │
  │     "cost_usdc":     │                        │
  │       0.01           │                        │
  │   }                  │                        │
  │ }                    │                        │
  │                      │                        │
  │ ┌──────────────────┐ │                        │
  │ │ ✅ LLM Response  │ │                        │
  │ │ Remaining: $0.01 │ │                        │
  │ └──────────────────┘ │                        │
```

## 💾 Balance Storage

```
┌───────────────────────────────────────────────────────────────┐
│ Balance Units:                                                │
│                                                               │
│ Agent.balance (Database)                                      │
│ ├─ Stored in: USDC minor units (6 decimals)                  │
│ ├─ Example: 20000 = 0.02 USDC                                │
│ └─ Used for: Agent global balance                            │
│                                                               │
│ AgentVmInstances.balance (Database)                           │
│ ├─ Stored in: USD (decimal)                                  │
│ ├─ Example: 0.02 = $0.02                                     │
│ └─ Used for: VM-specific balances                            │
│                                                               │
│ API Responses                                                 │
│ ├─ balance: "20000" (minor units as string)                  │
│ └─ balance_usdc: "0.02" (USD as string)                      │
└───────────────────────────────────────────────────────────────┘
```

## 🔑 API Endpoints Reference

### DevPortal Endpoints

#### 1. Check Balance
```
GET /api/agent/balance

Auth: Service key OR Agent signature
Response: { "balance": "20000", "vms": [] }
```

#### 2. Add Funds
```
POST /api/agent/add-funds

Auth: Agent signature + x402 payment (on retry)
Body: { "amount_usdc": "0.02" }
Response (402): { "status": "payment_required", ... }
Response (200): { "balance": "20000", "payment_method": "x402" }
```

#### 3. Report Usage
```
POST /api/user/report-usage

Auth: Service key
Body: { "wallet_address": "0x...", "tokens": 1500, "cost_usdc": 0.01 }
Response: { "success": true }
```

### Caddy Endpoints

#### 1. Chat Completions
```
POST /v1/chat/completions

Auth: Bearer token (API key)
Body: { "model": "gpt-4", "messages": [...] }
Response (402): { "error": "Insufficient balance", "topup_url": "...", ... }
Response (200): { "id": "...", "choices": [...], "usage": {...} }
```

## ⚙️ Configuration

### DevPortal Environment Variables

```bash
# x402 Payment Configuration
AGENT_TREASURY_ADDRESS=0x959257a565d5Fb44724Bc322e83CAF8c8AaB3E8b
AGENT_USDC_TOKEN_ADDRESS=0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913
AGENT_USDC_DECIMALS=6
AGENT_USDC_TOKEN_NAME="USD Coin"
AGENT_USDC_TOKEN_VERSION=2
AGENT_X402_NETWORK=eip155:8453
AGENT_X402_ASSET_TRANSFER_METHOD=eip3009

# Service Authentication
AGENT_BALANCE_SERVICE_KEYS=caddy-secret-key-123
```

### Caddy Environment Variables

```bash
DEVPORTAL_URL=https://devportal.com
DEVPORTAL_SERVICE_KEY=caddy-secret-key-123
```

## 🎯 Key Features

### ✅ Auto-Create Agent
- Agent records are automatically created with 0 balance on first balance check
- No manual registration required

### ✅ No Rate Limiting on Payment Endpoint
- `/api/agent/add-funds` is exempt from rate limiting
- Allows x402 two-step flow (402 → payment → success)

### ✅ Prepaid Model
- Agents top up balance first
- Use balance for multiple LLM requests
- No payment signature required per request

### ✅ Service-to-Service Auth
- Caddy uses service key to check balances
- No agent signature required for service calls
- Secure shared secret authentication

## 🚨 Error Codes

```
┌──────┬───────────────────────────────────────────────────────┐
│ Code │ Description                                           │
├──────┼───────────────────────────────────────────────────────┤
│ 401  │ Invalid authentication (signature or service key)     │
│ 402  │ Payment required (insufficient balance or x402)       │
│ 404  │ Resource not found                                    │
│ 429  │ Rate limit exceeded (not applied to add-funds)        │
│ 500  │ Server error                                          │
└──────┴───────────────────────────────────────────────────────┘
```

## 📝 Notes

1. **x402 Flow**: Requires 2 requests (initial 402 + retry with payment)
2. **Balance Units**: Agent.balance uses minor units (6 decimals), AgentVmInstances uses USD
3. **EIP-3009**: Uses `transferWithAuthorization` for USDC payments on Base
4. **Base Mainnet**: Network ID `eip155:8453`
5. **Charge Amount**: Default $0.01 per LLM request (configurable)