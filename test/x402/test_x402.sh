#!/usr/bin/env bash
# test_x402.sh — End-to-end tests for x402 payment protocol in Caddy
set -euo pipefail

CADDY_URL="${CADDY_URL:-http://localhost:8080}"
SECRETVM_URL="${SECRETVM_URL:-http://localhost:9100}"
AGENT_KEY="test-x402-shared-secret"
PASS=0
FAIL=0

# ---- helpers ----

red()   { printf "\033[31m%s\033[0m\n" "$*"; }
green() { printf "\033[32m%s\033[0m\n" "$*"; }
bold()  { printf "\033[1m%s\033[0m\n" "$*"; }

sign_request() {
    local method="$1" path="$2" body="$3" timestamp="$4"
    local canonical="${method}
${path}
${body}
${timestamp}"
    echo -n "$canonical" | openssl dgst -sha256 -hmac "$AGENT_KEY" -hex 2>/dev/null | awk '{print $NF}'
}

agent_request() {
    local method="$1" path="$2" body="${3:-}" agent="${4:-agent-funded}"
    local timestamp
    timestamp="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    local sig
    sig="$(sign_request "$method" "$path" "$body" "$timestamp")"

    if [ -n "$body" ]; then
        curl -s -w "\n%{http_code}" -X "$method" "${CADDY_URL}${path}" \
            -H "Content-Type: application/json" \
            -H "X-Agent-Address: ${agent}" \
            -H "X-Agent-Signature: ${sig}" \
            -H "X-Agent-Timestamp: ${timestamp}" \
            -d "$body"
    else
        curl -s -w "\n%{http_code}" -X "$method" "${CADDY_URL}${path}" \
            -H "X-Agent-Address: ${agent}" \
            -H "X-Agent-Signature: ${sig}" \
            -H "X-Agent-Timestamp: ${timestamp}"
    fi
}

assert_status() {
    local test_name="$1" expected="$2" actual="$3" body="$4"
    if [ "$actual" = "$expected" ]; then
        green "  PASS: $test_name (status=$actual)"
        PASS=$((PASS + 1))
    else
        red "  FAIL: $test_name (expected=$expected, got=$actual)"
        red "  Body: $body"
        FAIL=$((FAIL + 1))
    fi
}

assert_contains() {
    local test_name="$1" needle="$2" haystack="$3"
    if echo "$haystack" | grep -q "$needle"; then
        green "  PASS: $test_name (contains '$needle')"
        PASS=$((PASS + 1))
    else
        red "  FAIL: $test_name (missing '$needle')"
        red "  Body: $haystack"
        FAIL=$((FAIL + 1))
    fi
}

# ---- wait for services ----

bold "Waiting for services..."
for i in $(seq 1 30); do
    if curl -sf "${CADDY_URL}/metrics" >/dev/null 2>&1; then
        green "Caddy is ready"
        break
    fi
    if [ "$i" = "30" ]; then
        red "Caddy failed to start"
        exit 1
    fi
    sleep 1
done

for i in $(seq 1 10); do
    if curl -sf "${SECRETVM_URL}/health" >/dev/null 2>&1; then
        green "SecretVM simulator is ready"
        break
    fi
    sleep 1
done

# ---- Pre-fund agent via SecretVM simulator ----

bold ""
bold "=== Pre-funding agents via SecretVM simulator ==="
curl -s -X POST "${SECRETVM_URL}/api/agent/add-funds" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Address: agent-funded" \
    -d '{"agent_address":"agent-funded","amount":100000}' | jq . 2>/dev/null || true

# Give reconciler a cycle to sync
sleep 12

# ============================================================
bold ""
bold "=== TEST 1: Agent request with sufficient balance ==="
# ============================================================

BODY='{"model":"llama3.3:70b","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}'
RESPONSE=$(agent_request "POST" "/v1/chat/completions" "$BODY" "agent-funded")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Funded agent gets 200" "200" "$HTTP_CODE" "$RESP_BODY"
assert_contains "Response has chat completion" "chat.completion" "$RESP_BODY"
assert_contains "Response has model" "llama3.3:70b" "$RESP_BODY"

# ============================================================
bold ""
bold "=== TEST 2: Agent request with insufficient balance (402) ==="
# ============================================================

BODY='{"model":"llama3.3:70b","messages":[{"role":"user","content":"Hello"}],"max_tokens":100}'
RESPONSE=$(agent_request "POST" "/v1/chat/completions" "$BODY" "agent-unfunded")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Unfunded agent gets 402" "402" "$HTTP_CODE" "$RESP_BODY"
assert_contains "402 has payment_url" "payment_url" "$RESP_BODY"
assert_contains "402 has required_amount" "required_amount" "$RESP_BODY"
assert_contains "402 has agent_address" "agent-unfunded" "$RESP_BODY"

# ============================================================
bold ""
bold "=== TEST 3: Invalid signature => 401 ==="
# ============================================================

BODY='{"model":"test","messages":[{"role":"user","content":"hi"}]}'
TIMESTAMP="$(date -u +"%Y-%m-%dT%H:%M:%SZ")"
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${CADDY_URL}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Address: agent-funded" \
    -H "X-Agent-Signature: deadbeef0000000000000000000000000000000000000000000000000000dead" \
    -H "X-Agent-Timestamp: ${TIMESTAMP}" \
    -d "$BODY")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Invalid signature gets 401" "401" "$HTTP_CODE" "$RESP_BODY"

# ============================================================
bold ""
bold "=== TEST 4: Stale timestamp => 401 ==="
# ============================================================

BODY='{"model":"test","messages":[{"role":"user","content":"hi"}]}'
OLD_TS="2020-01-01T00:00:00Z"
SIG=$(sign_request "POST" "/v1/chat/completions" "$BODY" "$OLD_TS")
RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${CADDY_URL}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Address: agent-funded" \
    -H "X-Agent-Signature: ${SIG}" \
    -H "X-Agent-Timestamp: ${OLD_TS}" \
    -d "$BODY")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Stale timestamp gets 401" "401" "$HTTP_CODE" "$RESP_BODY"

# ============================================================
bold ""
bold "=== TEST 5: Legacy API key auth still works ==="
# ============================================================

RESPONSE=$(curl -s -w "\n%{http_code}" -X POST "${CADDY_URL}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer test-master-key-for-x402" \
    -d '{"model":"test","messages":[{"role":"user","content":"Hello from legacy"}]}')
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Legacy API key gets 200" "200" "$HTTP_CODE" "$RESP_BODY"

# ============================================================
bold ""
bold "=== TEST 6: Fund agent, then retry => 200 ==="
# ============================================================

# Create a new agent with zero balance
BODY='{"model":"test","messages":[{"role":"user","content":"after funding"}],"max_tokens":50}'
RESPONSE=$(agent_request "POST" "/v1/chat/completions" "$BODY" "agent-new")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
assert_status "New agent initially gets 402" "402" "$HTTP_CODE" "$(echo "$RESPONSE" | sed '$d')"

# Fund via SecretVM simulator
curl -s -X POST "${SECRETVM_URL}/api/agent/add-funds" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Address: agent-new" \
    -d '{"agent_address":"agent-new","amount":50000}' >/dev/null

# Wait for reconciler to sync
sleep 12

# Retry — should succeed now
RESPONSE=$(agent_request "POST" "/v1/chat/completions" "$BODY" "agent-new")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Funded agent-new gets 200 after top-up" "200" "$HTTP_CODE" "$RESP_BODY"

# ============================================================
bold ""
bold "=== TEST 7: Multiple requests drain balance ==="
# ============================================================

# Fund a test agent with a small balance
curl -s -X POST "${SECRETVM_URL}/api/agent/add-funds" \
    -H "Content-Type: application/json" \
    -H "X-Agent-Address: agent-drain" \
    -d '{"agent_address":"agent-drain","amount":50}' >/dev/null

sleep 12

BODY='{"model":"llama3.3:70b","messages":[{"role":"user","content":"drain test with a longer message to increase input token count and cost for the drain test scenario"}],"max_tokens":4096}'
GOT_402=false
for i in $(seq 1 50); do
    RESPONSE=$(agent_request "POST" "/v1/chat/completions" "$BODY" "agent-drain")
    HTTP_CODE=$(echo "$RESPONSE" | tail -1)
    if [ "$HTTP_CODE" = "402" ]; then
        GOT_402=true
        green "  PASS: Balance drained after $i requests (got 402)"
        PASS=$((PASS + 1))
        break
    fi
done

if [ "$GOT_402" = "false" ]; then
    red "  FAIL: Never got 402 after 20 requests (balance should have drained)"
    FAIL=$((FAIL + 1))
fi

# ============================================================
bold ""
bold "=== TEST 8: Metrics endpoint shows x402 data ==="
# ============================================================

RESPONSE=$(curl -s -w "\n%{http_code}" "${CADDY_URL}/metrics")
HTTP_CODE=$(echo "$RESPONSE" | tail -1)
RESP_BODY=$(echo "$RESPONSE" | sed '$d')

assert_status "Metrics endpoint returns 200" "200" "$HTTP_CODE" "$RESP_BODY"

# ============================================================
bold ""
bold "========================================="
bold "  Results: $PASS passed, $FAIL failed"
bold "========================================="

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
