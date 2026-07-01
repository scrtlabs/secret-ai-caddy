#!/usr/bin/env python3
"""
Diagnostic: shows which SSE chunk carries 'usage' and what values it contains.
Tests B1 (first vs last chunk) and B2 (prompt_tokens=0 case).

Usage: python3 test_sse_chunks.py <API_KEY> [URL]
"""
import json, ssl, sys
from urllib.request import Request, urlopen

URL = sys.argv[2] if len(sys.argv) > 2 else "https://secretai-lambda.scrtlabs.com:21434/v1/chat/completions"
KEY = sys.argv[1] if len(sys.argv) > 1 else ""

PROMPT = "Count from 1 to 20, one number per line."

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

payload = json.dumps({
    "model": "gpt-oss:120b",
    "messages": [{"role": "user", "content": PROMPT}],
    "stream": True,
    "max_tokens": 200,
}).encode()

req = Request(URL, data=payload, headers={
    "Authorization": f"Bearer {KEY}",
    "Content-Type": "application/json",
})

print(f"→ {URL}")
print(f"  Prompt: {PROMPT!r}\n")

chunk_idx = 0
usage_chunks = []
content_chunks_with_usage = 0

with urlopen(req, context=ctx, timeout=60) as resp:
    for raw in resp:
        line = raw.decode().strip()
        if not line.startswith("data:"):
            continue
        data = line[5:].strip()
        if data == "[DONE]":
            print(f"\n[chunk {chunk_idx:04d}] [DONE]")
            break

        try:
            obj = json.loads(data)
        except Exception:
            continue

        chunk_idx += 1
        usage = obj.get("usage")
        choices = obj.get("choices", [])
        delta_content = ""
        finish_reason = None
        if choices:
            delta_content = choices[0].get("delta", {}).get("content", "")
            finish_reason = choices[0].get("finish_reason")

        if usage is not None:
            usage_chunks.append((chunk_idx, usage, bool(delta_content), finish_reason))
            marker = " ◄ USAGE"
        else:
            marker = ""

        # Print only chunks that have usage or finish_reason (not every token chunk)
        if usage is not None or finish_reason is not None:
            print(f"[chunk {chunk_idx:04d}] finish={finish_reason!r:8}  usage={json.dumps(usage)}{marker}")

print(f"\n{'='*60}")
print(f"Total chunks: {chunk_idx}")
print(f"Chunks with usage: {len(usage_chunks)}")
print()

if not usage_chunks:
    print("⚠  NO USAGE found in any chunk!")
    print("   This means stream_options.include_usage was NOT injected,")
    print("   OR the backend does not support it.")
else:
    first_idx, first_usage, first_has_content, _ = usage_chunks[0]
    last_idx, last_usage, last_has_content, _ = usage_chunks[-1]

    print(f"First usage chunk: #{first_idx}")
    print(f"  prompt_tokens:     {first_usage.get('prompt_tokens')}")
    print(f"  completion_tokens: {first_usage.get('completion_tokens')}")
    print(f"  has delta.content: {first_has_content}")

    if len(usage_chunks) > 1:
        print(f"\nLast usage chunk:  #{last_idx}")
        print(f"  prompt_tokens:     {last_usage.get('prompt_tokens')}")
        print(f"  completion_tokens: {last_usage.get('completion_tokens')}")
        print(f"  has delta.content: {last_has_content}")
    print()

    # B1 check
    if len(usage_chunks) > 1:
        if first_usage != last_usage:
            print("🔴 B1 CONFIRMED: multiple chunks have usage, first ≠ last")
            print("   Caddy will bill based on the first (wrong) chunk.")
        else:
            print("✅ B1 OK: all usage chunks carry identical values (or only one chunk has usage)")
    else:
        print("✅ B1 OK: only one chunk carries usage (the last one)")

    # B2 check
    pt = last_usage.get("prompt_tokens", 0)
    ct = last_usage.get("completion_tokens", 0)
    if pt == 0 and ct > 0:
        print("🔴 B2 CONFIRMED: final chunk has prompt_tokens=0, completion_tokens>0")
        print("   Caddy will overwrite inputTokens with 0 → input billing zeroed.")
    elif pt == 0 and ct == 0:
        print("⚠  Both prompt_tokens=0 and completion_tokens=0 in final chunk.")
        print("   extractUsageFromResponse will return ok=false (fallback to tokenizer).")
    else:
        print(f"✅ B2 OK: final usage prompt={pt}, completion={ct} (both non-zero)")
