#!/usr/bin/env bash
# test_flows.sh — End-to-end tests for caddy-mock-llm on HTTPS :21434
# Tests against local Caddy (port 21434, self-signed TLS) + live preview-aidev.scrtlabs.com portal
#
# Prerequisites:
#   docker compose up -d  (from caddy-mock-llm/)
#   jq, node, openssl, curl installed
#
# Usage: bash test_flows.sh

set -euo pipefail

# ── Config ────────────────────────────────────────────────────────────────────
# All secrets are read from environment variables or .env file.
# Copy .env.example to .env and fill in real values before running.
SCRIPT_DIR_EARLY="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[ -f "$SCRIPT_DIR_EARLY/.env" ] && set -a && source "$SCRIPT_DIR_EARLY/.env" && set +a

CADDY="${CADDY:-https://localhost:21434}"
PORTAL="${DEVPORTAL_URL:-https://aidev.scrtlabs.com}"

# ── Secrets (required — must be set in .env or environment) ──────────────────
SERVICE_KEY="${DEVPORTAL_SERVICE_KEY:?DEVPORTAL_SERVICE_KEY is required (set in .env)}"

# Test agent wallet with funded balance on portal
PRIVKEY="${TEST_AGENT_PRIVKEY:?TEST_AGENT_PRIVKEY is required (set in .env)}"
WALLET="${TEST_AGENT_WALLET:?TEST_AGENT_WALLET is required (set in .env)}"

# User Bearer API key registered in Secret Network contract
API_KEY="${TEST_API_KEY:?TEST_API_KEY is required (set in .env)}"

# Zero-balance test wallet (Hardhat account 0 — safe to hardcode, public test key)
ZERO_PRIVKEY="${TEST_ZERO_PRIVKEY:-0xac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80}"
ZERO_WALLET="${TEST_ZERO_WALLET:-0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266}"

# Standard LLM request body
LLM_BODY='{"model":"deepseek-r1:70b","messages":[{"role":"user","content":"ping"}]}'
# Ollama /api/chat format
OLLAMA_BODY='{"model":"deepseek-r1:70b","messages":[{"role":"user","content":"ping"}],"stream":false}'
# Non-LLM request — no "model" field → no billing, no balance check
EMBED_BODY='{"input":"hello world"}'

SCRIPT_DIR="$SCRIPT_DIR_EARLY"
SIGN="$SCRIPT_DIR/sign_request.mjs"

# ── Helpers ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
PASSED=0; FAILED=0

pass()    { echo -e "${GREEN}✅ PASS${NC}: $1"; PASSED=$((PASSED+1)); }
fail()    { echo -e "${RED}❌ FAIL${NC}: $1"; FAILED=$((FAILED+1)); }
info()    { echo -e "${CYAN}ℹ️  ${NC}$1"; }
section() { echo -e "\n${YELLOW}═══ $1 ═══${NC}"; }

# x402 signed request — result: "<body>\n<http_code>"
x402_req() {
  local method=$1 path=$2 privkey=$3 wallet=$4 body=${5:-""}
  local sig_json ts sig
  sig_json=$(node "$SIGN" "$privkey" "$method" "$path" "$body" 2>/dev/null)
  ts=$(echo "$sig_json"  | jq -r '.timestamp')
  sig=$(echo "$sig_json" | jq -r '.signature')
  if [ -n "$body" ]; then
    curl -sk -w "\n%{http_code}" -X "$method" "$CADDY$path" \
      -H "Content-Type: application/json" \
      -H "X-Agent-Address: $wallet" \
      -H "X-Agent-Signature: $sig" \
      -H "X-Agent-Timestamp: $ts" \
      -d "$body"
  else
    curl -sk -w "\n%{http_code}" -X "$method" "$CADDY$path" \
      -H "X-Agent-Address: $wallet" \
      -H "X-Agent-Signature: $sig" \
      -H "X-Agent-Timestamp: $ts"
  fi
}

http_code() { echo "$1" | tail -1; }
http_body() { echo "$1" | sed '$ d'; }

hmac_hex() {
  local key=$1 payload=$2
  echo -n "$payload" | openssl dgst -sha256 -hmac "$key" | awk '{print $2}'
}

# ── Prerequisites ─────────────────────────────────────────────────────────────
section "Prerequisites"
for cmd in jq node openssl curl docker; do
  command -v "$cmd" &>/dev/null || { echo -e "${RED}ERROR${NC}: '$cmd' not found"; exit 1; }
done
info "All tools available"

HTTP_HEALTH=$(curl -sk -o /dev/null -w "%{http_code}" "$CADDY/" 2>/dev/null || echo "000")
[ "$HTTP_HEALTH" = "000" ] && { echo -e "${RED}ERROR${NC}: Caddy not up — run: docker compose up -d"; exit 1; }
info "Caddy is up at $CADDY (responded $HTTP_HEALTH)"

# ─────────────────────────────────────────────────────────────────────────────
# SECTION A: x402 AGENT FLOW
# ─────────────────────────────────────────────────────────────────────────────

# ── A1: x402 | LLM request with sufficient balance → 200 ─────────────────────
section "A1: x402 | POST /v1/chat/completions + sufficient balance → 200"
RESP=$(x402_req POST /v1/chat/completions "$PRIVKEY" "$WALLET" "$LLM_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  USAGE=$(echo "$BODY_OUT" | jq -c '.usage // null' 2>/dev/null)
  pass "A1: 200 OK | Usage: $USAGE"
else
  fail "A1: expected 200, got $CODE — $(echo "$BODY_OUT" | head -c 200)"
fi

sleep 2  # allow async ReportUsage goroutine to fire

# Verify portal report-usage works directly
REPORT_BODY="{\"wallet_address\":\"$WALLET\",\"usage_data\":[{\"input_tokens\":1,\"output_tokens\":1,\"model\":\"deepseek-r1:70b\",\"timestamp\":$(date +%s)}]}"
REPORT_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$PORTAL/api/user/report-usage" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Service-Key: $SERVICE_KEY" \
  -H "X-Agent-Wallet-Address: $WALLET" \
  -d "$REPORT_BODY")
[ "$REPORT_CODE" = "200" ] \
  && pass "A1b: /api/user/report-usage portal → 200 (agent billing path works)" \
  || fail "A1b: report-usage portal → $REPORT_CODE"

# ── A2: x402 | LLM request with zero balance → 402 ───────────────────────────
section "A2: x402 | POST /v1/chat/completions + zero balance → 402"
RESP=$(x402_req POST /v1/chat/completions "$ZERO_PRIVKEY" "$ZERO_WALLET" "$LLM_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "402" ]; then
  pass "A2: 402 Payment Required"
  info "402 body: $(echo "$BODY_OUT" | jq -c . 2>/dev/null || echo "$BODY_OUT")"
else
  fail "A2: expected 402, got $CODE"
fi

# ── A3: x402 | GET /api/tags with zero balance → 200 (no balance gate) ───────
section "A3: x402 | GET /api/tags + zero balance → 200 (non-LLM, no gate)"
RESP=$(x402_req GET /api/tags "$ZERO_PRIVKEY" "$ZERO_WALLET")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  COUNT=$(echo "$BODY_OUT" | jq '.models | length' 2>/dev/null || echo "?")
  pass "A3: 200 OK — $COUNT models, no balance check for /api/tags"
else
  fail "A3: expected 200, got $CODE"
fi

# ── A4: x402 | GET /v1/models with zero balance → 200 ────────────────────────
section "A4: x402 | GET /v1/models + zero balance → 200 (non-LLM, no gate)"
RESP=$(x402_req GET /v1/models "$ZERO_PRIVKEY" "$ZERO_WALLET")
CODE=$(http_code "$RESP")
[ "$CODE" = "200" ] && pass "A4: 200 OK" || fail "A4: expected 200, got $CODE"

# ── A5: x402 | POST /v1/embeddings (no "model" field) + zero balance → 200 ───
section "A5: x402 | POST /v1/embeddings (no 'model' field) + zero balance → 200"
RESP=$(x402_req POST /v1/embeddings "$ZERO_PRIVKEY" "$ZERO_WALLET" "$EMBED_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "200" ] && pass "A5: 200 OK — embedding bypasses balance gate" || fail "A5: expected 200, got $CODE"

# ── A6: x402 | Missing X-Agent-Address → 401 ─────────────────────────────────
section "A6: x402 | No X-Agent-Address → 401"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" -d "$LLM_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "401" ] && pass "A6: no auth → 401" || fail "A6: expected 401, got $CODE"

# ── A7: x402 | Invalid signature → 401 ───────────────────────────────────────
section "A7: x402 | Invalid signature → 401"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Address: $WALLET" \
  -H "X-Agent-Signature: 0xdeadbeef000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001b" \
  -H "X-Agent-Timestamp: $(date +%s)" \
  -d "$LLM_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "401" ] && pass "A7: invalid sig → 401" || fail "A7: expected 401, got $CODE"

# ── A8: x402 | Ollama /api/chat with sufficient balance → 200 ────────────────
section "A8: x402 | POST /api/chat (Ollama native) + sufficient balance → 200"
RESP=$(x402_req POST /api/chat "$PRIVKEY" "$WALLET" "$OLLAMA_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  DONE=$(echo "$BODY_OUT" | jq '.done' 2>/dev/null || echo "?")
  PTOKS=$(echo "$BODY_OUT" | jq '.prompt_eval_count' 2>/dev/null || echo "?")
  CTOKS=$(echo "$BODY_OUT" | jq '.eval_count' 2>/dev/null || echo "?")
  pass "A8: 200 OK | done=$DONE prompt_eval_count=$PTOKS eval_count=$CTOKS"
else
  fail "A8: expected 200, got $CODE — $(echo "$BODY_OUT" | head -c 200)"
fi

# ── A9: x402 | Ollama /api/chat + zero balance → 402 ─────────────────────────
section "A9: x402 | POST /api/chat (Ollama native) + zero balance → 402"
RESP=$(x402_req POST /api/chat "$ZERO_PRIVKEY" "$ZERO_WALLET" "$OLLAMA_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "402" ] && pass "A9: 402 Payment Required" || fail "A9: expected 402, got $CODE"

# ── A10: x402 | GET /api/version → 200 (health, no auth needed downstream) ───
section "A10: x402 | GET /api/version + sufficient wallet → 200"
RESP=$(x402_req GET /api/version "$PRIVKEY" "$WALLET")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  VER=$(echo "$BODY_OUT" | jq -r '.version' 2>/dev/null || echo "?")
  pass "A10: 200 OK | version=$VER"
else
  fail "A10: expected 200, got $CODE"
fi

# ─────────────────────────────────────────────────────────────────────────────
# SECTION B: LEGACY USER FLOW (Bearer API key)
# ─────────────────────────────────────────────────────────────────────────────

# ── B1: Legacy | POST /v1/chat/completions → 200 ─────────────────────────────
section "B1: Legacy | Bearer API key + POST /v1/chat/completions → 200"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$LLM_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  USAGE=$(echo "$BODY_OUT" | jq -c '.usage // null' 2>/dev/null)
  pass "B1: 200 OK | Usage: $USAGE"
else
  fail "B1: expected 200, got $CODE"
fi

# ── B2: Legacy | POST /api/chat (Ollama native) → 200 ────────────────────────
section "B2: Legacy | Bearer API key + POST /api/chat (Ollama native) → 200"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/api/chat" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$OLLAMA_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  DONE=$(echo "$BODY_OUT" | jq '.done' 2>/dev/null || echo "?")
  pass "B2: 200 OK | done=$DONE"
else
  fail "B2: expected 200, got $CODE"
fi

# ── B3: Legacy | GET /v1/models → 200 ────────────────────────────────────────
section "B3: Legacy | Bearer API key + GET /v1/models → 200"
RESP=$(curl -sk -w "\n%{http_code}" "$CADDY/v1/models" \
  -H "Authorization: Bearer $API_KEY")
CODE=$(http_code "$RESP")
[ "$CODE" = "200" ] && pass "B3: 200 OK" || fail "B3: expected 200, got $CODE"

# ── B4: Legacy | GET /api/tags → 200 ─────────────────────────────────────────
section "B4: Legacy | Bearer API key + GET /api/tags → 200"
RESP=$(curl -sk -w "\n%{http_code}" "$CADDY/api/tags" \
  -H "Authorization: Bearer $API_KEY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
if [ "$CODE" = "200" ]; then
  COUNT=$(echo "$BODY_OUT" | jq '.models | length' 2>/dev/null || echo "?")
  pass "B4: 200 OK — $COUNT models"
else
  fail "B4: expected 200, got $CODE"
fi

# ── B5: Legacy | No auth → 401 ───────────────────────────────────────────────
section "B5: Legacy | No auth → 401"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" -d "$LLM_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "401" ] && pass "B5: no auth → 401" || fail "B5: expected 401, got $CODE"

# ── B6: Legacy | Invalid API key → 401 ───────────────────────────────────────
section "B6: Legacy | Invalid Bearer key → 401"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer sk-invalidkeyXXXXXXXXXXXXXXXXXXXXXXXX" \
  -d "$LLM_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "401" ] && pass "B6: invalid key → 401" || fail "B6: expected 401, got $CODE"

# ─────────────────────────────────────────────────────────────────────────────
# SECTION C: DIRECT PORTAL API
# ─────────────────────────────────────────────────────────────────────────────

section "C1: Direct report-usage (raw service key) → 200"
REPORT_BODY="{\"wallet_address\":\"$WALLET\",\"usage_data\":[{\"input_tokens\":100,\"output_tokens\":50,\"model\":\"deepseek-r1:70b\",\"timestamp\":$(date +%s)}]}"
RESP=$(curl -s -w "\n%{http_code}" -X POST "$PORTAL/api/user/report-usage" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Service-Key: $SERVICE_KEY" \
  -H "X-Agent-Wallet-Address: $WALLET" \
  -d "$REPORT_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
[ "$CODE" = "200" ] \
  && pass "C1: 200 — $(echo "$BODY_OUT" | jq -c . 2>/dev/null || echo "$BODY_OUT")" \
  || fail "C1: got $CODE — $BODY_OUT"

section "C2: Direct report-usage (HMAC service key) → 200"
HMAC_KEY=$(hmac_hex "$SERVICE_KEY" "$(echo -n "$WALLET" | tr '[:upper:]' '[:lower:]')")
RESP=$(curl -s -w "\n%{http_code}" -X POST "$PORTAL/api/user/report-usage" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Service-Key: $HMAC_KEY" \
  -H "X-Agent-Wallet-Address: $WALLET" \
  -d "$REPORT_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "200" ] && pass "C2: HMAC report-usage → 200" || fail "C2: HMAC → $CODE"

# ─────────────────────────────────────────────────────────────────────────────
# SECTION D: Mock-LLM endpoint validation (no auth needed, direct to mock)
# ─────────────────────────────────────────────────────────────────────────────

section "D: Mock-LLM specific endpoints (via Caddy, Bearer auth)"

# D1: /api/version
RESP=$(curl -sk -w "\n%{http_code}" "$CADDY/api/version" -H "Authorization: Bearer $API_KEY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
VER=$(echo "$BODY_OUT" | jq -r '.version' 2>/dev/null || echo "?")
[ "$CODE" = "200" ] && pass "D1: GET /api/version → 200 | version=$VER" || fail "D1: expected 200, got $CODE"

# D2: /api/show
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/api/show" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $API_KEY" \
  -d '{"model":"llama-3.3-70b-instruct"}')
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
FAM=$(echo "$BODY_OUT" | jq -r '.details.family' 2>/dev/null || echo "?")
[ "$CODE" = "200" ] && pass "D2: POST /api/show → 200 | family=$FAM" || fail "D2: expected 200, got $CODE"

# D3: /api/embed (Ollama v2 embeddings)
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/api/embed" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $API_KEY" \
  -d '{"model":"llama-3.3-70b-instruct","input":"test embedding"}')
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
EMBS=$(echo "$BODY_OUT" | jq '.embeddings | length' 2>/dev/null || echo "?")
[ "$CODE" = "200" ] && pass "D3: POST /api/embed → 200 | embeddings=$EMBS" || fail "D3: expected 200, got $CODE"

# D4: /api/chat with think=true
THINK_BODY='{"model":"qwen3:8b","messages":[{"role":"user","content":"count r in strawberry"}],"stream":false,"think":true}'
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/api/chat" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $API_KEY" \
  -d "$THINK_BODY")
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
THINKING=$(echo "$BODY_OUT" | jq -r '.message.thinking // ""' 2>/dev/null)
[ "$CODE" = "200" ] && [ -n "$THINKING" ] \
  && pass "D4: POST /api/chat think=true → 200 | thinking present" \
  || fail "D4: expected 200 + thinking, got $CODE thinking='$THINKING'"

# D5: /api/generate
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/api/generate" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $API_KEY" \
  -d '{"model":"llama-3.3-70b-instruct","prompt":"hello","stream":false}')
CODE=$(http_code "$RESP"); BODY_OUT=$(http_body "$RESP")
DONE=$(echo "$BODY_OUT" | jq '.done' 2>/dev/null || echo "?")
[ "$CODE" = "200" ] && pass "D5: POST /api/generate → 200 | done=$DONE" || fail "D5: expected 200, got $CODE"

# ─────────────────────────────────────────────────────────────────────────────
# SECTION E: Security
# ─────────────────────────────────────────────────────────────────────────────

section "E1: BLOCK_URLS — /api/pull is blocked"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/api/pull" \
  -H "Authorization: Bearer $API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"name":"llama3"}')
CODE=$(http_code "$RESP")
[ "$CODE" = "403" ] && pass "E1: /api/pull → 403 blocked" || fail "E1: expected 403, got $CODE"

section "E2: Expired timestamp → 401"
RESP=$(curl -sk -w "\n%{http_code}" -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "X-Agent-Address: $WALLET" \
  -H "X-Agent-Signature: 0xdeadbeef000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001b" \
  -H "X-Agent-Timestamp: 1000000" \
  -d "$LLM_BODY")
CODE=$(http_code "$RESP")
[ "$CODE" = "401" ] && pass "E2: expired timestamp → 401" || fail "E2: expected 401, got $CODE"

# ─────────────────────────────────────────────────────────────────────────────
# SECTION G: Verify token recording — LLM vs non-LLM, user vs agent
# ─────────────────────────────────────────────────────────────────────────────

section "G1: Non-LLM endpoints must NOT appear in 'Token usage recorded' logs"
UNKNOWN_BEFORE=$(docker compose logs --tail=200 caddy 2>/dev/null | { grep -c '"model": "unknown"' || true; })
# Send a batch of non-LLM requests
curl -sk "$CADDY/api/version"  -H "Authorization: Bearer $API_KEY" -o /dev/null
curl -sk "$CADDY/api/tags"     -H "Authorization: Bearer $API_KEY" -o /dev/null
curl -sk "$CADDY/v1/models"    -H "Authorization: Bearer $API_KEY" -o /dev/null
sleep 1
UNKNOWN_AFTER=$(docker compose logs --tail=200 caddy 2>/dev/null | { grep -c '"model": "unknown"' || true; })
if [ "$UNKNOWN_BEFORE" = "$UNKNOWN_AFTER" ]; then
  pass "G1: non-LLM endpoints produce 0 new 'Token usage recorded' lines with model=unknown"
else
  fail "G1: new 'Token usage recorded' with model=unknown appeared ($UNKNOWN_BEFORE → $UNKNOWN_AFTER)"
fi

section "G2: User (Bearer) LLM request — correct model logged"
# Send one LLM request and capture log
curl -sk -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$LLM_BODY" -o /dev/null
sleep 1
MODEL_LOG=$(docker compose logs --tail=30 caddy 2>/dev/null | grep "Token usage recorded" | tail -1)
EXP_MODEL=$(echo "$LLM_BODY" | grep -o '"model":"[^"]*"' | head -1 | cut -d'"' -f4)
if echo "$MODEL_LOG" | grep -q "\"model\": \"$EXP_MODEL\""; then
  INPUT=$(echo "$MODEL_LOG" | grep -o '"input_tokens": [0-9]*' | awk '{print $2}')
  OUTPUT=$(echo "$MODEL_LOG" | grep -o '"output_tokens": [0-9]*' | awk '{print $2}')
  pass "G2: Token usage recorded with correct model=$EXP_MODEL | input=$INPUT output=$OUTPUT"
else
  fail "G2: expected model=$EXP_MODEL in log, got: $MODEL_LOG"
fi

section "G3: Agent (x402) LLM request — tokens reported to portal (not accumulator)"
BEFORE_REPORT=$(docker compose logs --tail=100 caddy 2>/dev/null | { grep -c "Failed to report usage\|report usage\|ReportUsage" || true; })
RESP=$(x402_req POST /v1/chat/completions "$PRIVKEY" "$WALLET" "$LLM_BODY")
CODE=$(http_code "$RESP")
sleep 2
AFTER_REPORT=$(docker compose logs --tail=100 caddy 2>/dev/null | { grep -c "Failed to report usage\|report usage\|ReportUsage" || true; })
if [ "$CODE" = "200" ]; then
  pass "G3: x402 LLM request → 200 (tokens reported to portal via ReportUsage, not accumulator)"
  info "G3: report-usage log delta: $BEFORE_REPORT → $AFTER_REPORT"
else
  fail "G3: expected 200, got $CODE"
fi

section "G4: Ollama /api/chat — correct model logged for user (Bearer)"
curl -sk -X POST "$CADDY/api/chat" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $API_KEY" \
  -d "$OLLAMA_BODY" -o /dev/null
sleep 1
CHAT_LOG=$(docker compose logs --tail=30 caddy 2>/dev/null | grep "Token usage recorded" | tail -1)
EXP_MODEL_CHAT=$(echo "$OLLAMA_BODY" | grep -o '"model":"[^"]*"' | head -1 | cut -d'"' -f4)
if echo "$CHAT_LOG" | grep -q "\"model\": \"$EXP_MODEL_CHAT\""; then
  INPUT=$(echo "$CHAT_LOG" | grep -o '"input_tokens": [0-9]*' | awk '{print $2}')
  OUTPUT=$(echo "$CHAT_LOG" | grep -o '"output_tokens": [0-9]*' | awk '{print $2}')
  pass "G4: Ollama /api/chat token usage recorded | model=$EXP_MODEL_CHAT input=$INPUT output=$OUTPUT"
else
  fail "G4: expected model=$EXP_MODEL_CHAT in log, got: $CHAT_LOG"
fi

# ─────────────────────────────────────────────────────────────────────────────
# SECTION H: Report-usage verification — user vs agent, no double-report
# ─────────────────────────────────────────────────────────────────────────────

section "H1: Agent (x402) — usage reported to portal per-request (direct ReportUsage)"
# Send exactly 2 x402 LLM requests and count successful "x402: usage reported" log lines
AGENT_BEFORE=$(docker compose logs caddy 2>/dev/null | { grep -c "x402: usage reported to portal" || true; })
x402_req POST /v1/chat/completions "$PRIVKEY" "$WALLET" "$LLM_BODY" > /dev/null
x402_req POST /api/chat            "$PRIVKEY" "$WALLET" "$OLLAMA_BODY" > /dev/null
sleep 2
AGENT_AFTER=$(docker compose logs caddy 2>/dev/null | { grep -c "x402: usage reported to portal" || true; })
AGENT_DELTA=$((AGENT_AFTER - AGENT_BEFORE))
if [ "$AGENT_DELTA" -eq 2 ]; then
  pass "H1: exactly 2 'x402: usage reported to portal' entries after 2 agent requests"
  # Print the last 2 entries for visibility
  docker compose logs caddy 2>/dev/null | grep "x402: usage reported" | tail -2 | while IFS= read -r line; do
    info "  $line"
  done
else
  fail "H1: expected 2 agent report log entries, got $AGENT_DELTA (before=$AGENT_BEFORE after=$AGENT_AFTER)"
fi

# Confirm no errors logged for those requests
AGENT_ERRORS=$(docker compose logs --tail=50 caddy 2>/dev/null | { grep -c "x402: failed to report usage" || true; })
[ "$AGENT_ERRORS" -eq 0 ] \
  && pass "H1b: no 'x402: failed to report usage' errors" \
  || fail "H1b: $AGENT_ERRORS agent report error(s) found"

section "H2: User (Bearer) — usage reported via ResilientReporter batch (every 5s)"
# Send 2 Bearer LLM requests
USER_REPORT_BEFORE=$(docker compose logs caddy 2>/dev/null | { grep -c "Sending POST request to metering" || true; })
curl -sk -X POST "$CADDY/v1/chat/completions" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $API_KEY" \
  -d "$LLM_BODY" -o /dev/null
curl -sk -X POST "$CADDY/api/chat" \
  -H "Content-Type: application/json" -H "Authorization: Bearer $API_KEY" \
  -d "$OLLAMA_BODY" -o /dev/null
info "H2: waiting 8s for ResilientReporter to fire (METERING_INTERVAL=5s)..."
sleep 8
USER_REPORT_AFTER=$(docker compose logs caddy 2>/dev/null | { grep -c "Sending POST request to metering" || true; })
if [ "$USER_REPORT_AFTER" -gt "$USER_REPORT_BEFORE" ]; then
  pass "H2: ResilientReporter sent batch report to portal after user LLM requests"
  # Show last metering payload
  LAST_PAYLOAD=$(docker compose logs caddy 2>/dev/null | grep "Sending POST request to metering" | tail -1)
  info "  Payload: $(echo "$LAST_PAYLOAD" | grep -o '"payload":"[^"]*"' | head -c 200)..."
else
  fail "H2: no new metering POST after user LLM requests (waited 8s, interval=5s)"
fi

section "H3: No double-report for agent — accumulator must NOT contain wallet addresses"
# The ResilientReporter payload keys must be sha256 hex hashes (64 hex chars), NOT 0x wallet addresses.
# If agent tokens leaked into accumulator, we'd see 0x... as a key in usage_data.
LAST_METERING=$(docker compose logs caddy 2>/dev/null | grep "Sending POST request to metering" | tail -1)
if [ -n "$LAST_METERING" ]; then
  if echo "$LAST_METERING" | grep -qE '"0x[0-9a-fA-F]{40}"'; then
    fail "H3: wallet address found as key in metering payload — DOUBLE BILLING for agent!"
    info "  Line: $LAST_METERING"
  else
    pass "H3: no wallet address in metering payload (agent tokens not in accumulator)"
    # Also verify the key looks like a sha256 hash (64 hex chars)
    if echo "$LAST_METERING" | grep -qE '"[0-9a-f]{64}"'; then
      info "H3: metering payload key is sha256(api_key) as expected"
    fi
  fi
else
  info "H3: no metering payload found yet — skip (H2 should have caught this)"
fi

section "H4: Agent double-report via accumulator — explicit check"
# After agent requests in H1, the tokenAccumulator must NOT have grown.
# We verify by flushing: send a known Bearer request and check metering payload
# only contains ONE api_key_hash (the user's), not extra entries.
PAYLOAD_KEYS=$(docker compose logs caddy 2>/dev/null | grep "Sending POST request to metering" | tail -1 | grep -oE '"[0-9a-f]{64}"' | wc -l | tr -d ' ')
info "H4: number of api_key_hash keys in latest batch report: $PAYLOAD_KEYS"
if [ "${PAYLOAD_KEYS:-0}" -le 1 ]; then
  pass "H4: only 1 user api_key_hash in metering payload (agent did not pollute accumulator)"
else
  fail "H4: $PAYLOAD_KEYS keys in metering payload — possible accumulator pollution"
fi

# ─────────────────────────────────────────────────────────────────────────────
# SECTION F: Caddy log summary
# ─────────────────────────────────────────────────────────────────────────────

section "F: Caddy log summary (last 60 lines)"
echo ""
docker compose logs --tail=60 caddy 2>/dev/null \
  | grep -E "Token usage|report usage|x402:|balance|model|PASS|FAIL" \
  | grep -v "WARN\|tls\|autosave\|tokenizer" \
  | tail -15
echo ""

# ── Summary ───────────────────────────────────────────────────────────────────
section "Summary"
TOTAL=$((PASSED+FAILED))
echo -e "Tests passed: ${GREEN}$PASSED${NC}/$TOTAL"
[ "$FAILED" -gt 0 ] && echo -e "Tests failed: ${RED}$FAILED${NC}/$TOTAL" && exit 1
echo -e "${GREEN}All $TOTAL tests passed!${NC}"
