#!/usr/bin/env python3
"""
Simple pretty streaming test — one request, live output, summary at end.
Usage: python3 test_stream_pretty.py <API_KEY> [URL] [MODEL] [PROMPT]

Examples:
  python3 test_stream_pretty.py sk-xxx
  python3 test_stream_pretty.py sk-xxx https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions gemma4:31b
  python3 test_stream_pretty.py sk-xxx https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions qwen3.6:27b
"""
import json, ssl, sys, time
from urllib.request import Request, urlopen

URL   = sys.argv[2] if len(sys.argv) > 2 else "https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions"
KEY   = sys.argv[1] if len(sys.argv) > 1 else ""
MODEL = sys.argv[3] if len(sys.argv) > 3 else "gemma4:31b"
PROMPT = sys.argv[4] if len(sys.argv) > 4 else (
    "Explain in 3 paragraphs why transformers replaced RNNs for NLP tasks."
)

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

payload = json.dumps({
    "model": MODEL,
    "messages": [{"role": "user", "content": PROMPT}],
    "stream": True,
    "stream_options": {"include_usage": True},
}).encode()

req = Request(URL, data=payload, headers={
    "Authorization": f"Bearer {KEY}",
    "Content-Type": "application/json",
})

print(f"\033[1;34m{'─'*60}\033[0m")
print(f"\033[1mModel:\033[0m  {MODEL}")
print(f"\033[1mPrompt:\033[0m {PROMPT}")
print(f"\033[1;34m{'─'*60}\033[0m\n")

t0 = time.time()
ttft = None
token_count = 0
usage_reported = None

try:
    with urlopen(req, context=ctx, timeout=120) as resp:
        for raw in resp:
            line = raw.decode().strip()
            if not line.startswith("data:"):
                continue
            data = line[5:].strip()
            if data == "[DONE]":
                break
            try:
                obj = json.loads(data)
            except Exception:
                continue

            # Capture usage if present
            if obj.get("usage"):
                usage_reported = obj["usage"]

            choices = obj.get("choices", [])
            if not choices:
                continue
            delta = choices[0].get("delta", {}).get("content", "")
            if delta:
                if ttft is None:
                    ttft = time.time() - t0
                token_count += 1
                print(delta, end="", flush=True)

except Exception as e:
    print(f"\n\033[31mERROR: {e}\033[0m")
    sys.exit(1)

elapsed = time.time() - t0
tps = token_count / elapsed if elapsed > 0 else 0

print(f"\n\n\033[1;34m{'─'*60}\033[0m")
print(f"\033[1mStats:\033[0m")
print(f"  TTFT:          {ttft:.2f}s" if ttft else "  TTFT:         —")
print(f"  Total time:    {elapsed:.2f}s")
print(f"  Tokens (est):  {token_count}")
print(f"  Speed:         {tps:.1f} tok/s")

if usage_reported:
    pt = usage_reported.get("prompt_tokens", "?")
    ct = usage_reported.get("completion_tokens", "?")
    tt = usage_reported.get("total_tokens", "?")
    details = usage_reported.get("prompt_tokens_details") or {}
    cached = details.get("cached_tokens", 0)
    print(f"\n  \033[1mUsage (from backend):\033[0m")
    print(f"    prompt_tokens:     {pt}")
    print(f"    completion_tokens: {ct}")
    print(f"    total_tokens:      {tt}")
    if cached:
        pct = f"{cached/pt*100:.0f}%" if isinstance(pt, int) and pt > 0 else ""
        print(f"    cached_tokens:     \033[32m{cached} ({pct} of prompt) ✅\033[0m")
    else:
        print(f"    cached_tokens:     0  (first request or no cache hit)")
    if isinstance(ct, int) and ct > 0:
        ratio = token_count / ct
        match = "✅ close" if 0.9 <= ratio <= 1.1 else f"⚠  ratio {ratio:.2f}"
        print(f"    vs counted:        {token_count} SSE deltas ({match})")
else:
    print(f"\n  \033[33m⚠  No usage in response (stream_options not injected?)\033[0m")

print(f"\033[1;34m{'─'*60}\033[0m")
