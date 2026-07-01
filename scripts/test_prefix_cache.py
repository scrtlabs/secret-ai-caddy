#!/usr/bin/env python3
"""
Prefix cache verification — sends the same request twice, shows cached_tokens on 2nd hit.

Usage:
  python3 test_prefix_cache.py <API_KEY> [URL] [MODEL]

Examples:
  python3 test_prefix_cache.py sk-xxx
  python3 test_prefix_cache.py sk-xxx https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions gemma4:31b
  python3 test_prefix_cache.py sk-xxx https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions qwen3.6:27b
"""
import json, ssl, sys, time
from urllib.request import Request, urlopen

URL   = sys.argv[2] if len(sys.argv) > 2 else "https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions"
KEY   = sys.argv[1] if len(sys.argv) > 1 else ""
MODEL = sys.argv[3] if len(sys.argv) > 3 else "gemma4:31b"

if not KEY:
    print(__doc__)
    sys.exit(1)

ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

SYSTEM = (
    "You are a helpful assistant specializing in Linux kernel internals. "
    "The Linux kernel is a monolithic, modular, multi-tasking Unix-like operating system kernel. "
    "It was first released by Linus Torvalds on September 17, 1991. The kernel manages the system's "
    "resources and provides an interface between hardware and software. "
    "Virtual memory management in Linux uses a hierarchical page table structure. Each process has its "
    "own virtual address space mapped through page global directories (PGD), page upper directories (PUD), "
    "page middle directories (PMD), and page table entries (PTE). The Translation Lookaside Buffer (TLB) "
    "caches recent virtual-to-physical address translations. A TLB miss causes a page table walk. "
    "The Completely Fair Scheduler (CFS) is the default Linux process scheduler. It maintains a "
    "red-black tree ordered by virtual runtime. The process with the smallest vruntime is scheduled next. "
    "eBPF allows running sandboxed programs in the kernel for networking, observability, and security. "
    "NUMA systems have multiple memory nodes. The kernel migrates pages to reduce cross-node latency. "
) * 4  # ~800 tokens system prompt — large enough to trigger caching

MESSAGES = [
    {"role": "system", "content": SYSTEM},
    {"role": "user", "content": "What is eBPF?"},
]


def send(label: str) -> dict:
    payload = json.dumps({
        "model": MODEL,
        "messages": MESSAGES,
        "max_tokens": 80,
        "stream": False,
    }).encode()

    req = Request(URL, data=payload, headers={
        "Authorization": f"Bearer {KEY}",
        "Content-Type": "application/json",
    })

    t0 = time.time()
    with urlopen(req, context=ctx, timeout=120) as resp:
        body = json.loads(resp.read())
    elapsed = time.time() - t0

    usage = body.get("usage", {})
    details = usage.get("prompt_tokens_details") or {}
    cached = details.get("cached_tokens", 0)
    pt = usage.get("prompt_tokens", "?")
    ct = usage.get("completion_tokens", "?")

    print(f"\n\033[1m{label}\033[0m  ({elapsed:.2f}s)")
    print(f"  prompt_tokens:     {pt}")
    print(f"  completion_tokens: {ct}")
    if cached:
        pct = f"{cached/pt*100:.0f}%" if isinstance(pt, int) and pt > 0 else ""
        print(f"  cached_tokens:     \033[32m{cached} ({pct}) ✅\033[0m")
    else:
        print(f"  cached_tokens:     \033[33m0 (no hit)\033[0m")

    answer = body.get("choices", [{}])[0].get("message", {}).get("content", "")
    print(f"  answer snippet:    {answer[:80].strip()!r}")
    return {"elapsed": elapsed, "cached": cached, "pt": pt}


print(f"\033[1;34m{'─'*60}\033[0m")
print(f"\033[1mPrefix cache test\033[0m  model={MODEL}")
print(f"System prompt: {len(SYSTEM)} chars (~{len(SYSTEM)//4} tokens)")
print(f"\033[1;34m{'─'*60}\033[0m")

r1 = send("Request 1 (cold — populates cache)")
print(f"\n  Waiting 1s before 2nd request...")
time.sleep(1)
r2 = send("Request 2 (should hit cache)")

print(f"\n\033[1;34m{'─'*60}\033[0m")
if r2["cached"]:
    speedup = r1["elapsed"] / r2["elapsed"] if r2["elapsed"] > 0 else 1
    print(f"\033[1;32m✅ Prefix caching works! cached={r2['cached']} tokens, speedup={speedup:.1f}x TTFT\033[0m")
else:
    print(f"\033[1;31m⚠  No cache hit on 2nd request. Possible reasons:\033[0m")
    print(f"   - vLLM prefix caching may need larger prompt (try longer SYSTEM)")
    print(f"   - Cache block granularity: vLLM caches in blocks of 16-32 tokens")
    print(f"   - Check --enable-prefix-caching is set")
print(f"\033[1;34m{'─'*60}\033[0m")
