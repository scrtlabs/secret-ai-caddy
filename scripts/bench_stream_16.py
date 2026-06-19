#!/usr/bin/env python3
"""
16 parallel streaming requests to SecretAI endpoint.
Usage: python3 bench_stream_16.py <API_KEY> [URL]
"""
import threading, time, json, ssl, sys
from urllib.request import Request, urlopen

URL = sys.argv[2] if len(sys.argv) > 2 else "https://secretai-lambda.scrtlabs.com:21434/v1/chat/completions"
KEY = sys.argv[1] if len(sys.argv) > 1 else ""
N = 16

PROMPTS = [
    "You are a senior software engineer. Explain in detail how a distributed key-value store like Redis handles data persistence, replication, and failover. Include trade-offs between RDB and AOF persistence modes.",
    "You are a machine learning researcher. Describe the transformer architecture in depth: attention mechanisms, positional encoding, multi-head attention, and why it outperforms RNNs for sequence tasks.",
    "You are a systems programmer. Explain how the Linux kernel manages virtual memory, including page tables, TLB, demand paging, and the OOM killer. What happens during a page fault?",
    "You are a blockchain engineer. Explain the Ethereum consensus mechanism transition from PoW to PoS, how validators are selected, slashing conditions, and finality guarantees.",
    "You are a database expert. Compare PostgreSQL and MySQL in depth: MVCC implementations, indexing strategies, replication approaches, and which workloads each handles better.",
    "You are a network engineer. Explain the TLS 1.3 handshake in detail: what changed from TLS 1.2, how 0-RTT works, forward secrecy, and the cryptographic primitives used.",
    "You are a cloud architect. Design a highly available, globally distributed API serving 100k requests per second. Include load balancing, caching, CDN, database sharding, and failure handling.",
    "You are a security researcher. Explain common memory corruption vulnerabilities: buffer overflows, use-after-free, heap spraying, and modern mitigations like ASLR, stack canaries, and CFI.",
    "You are a compiler engineer. Explain how LLVM IR works, the optimization pipeline (passes), how SSA form enables optimizations, and how LLVM handles register allocation.",
    "You are a distributed systems engineer. Explain the Raft consensus algorithm: leader election, log replication, commit rules, and how it handles network partitions compared to Paxos.",
    "You are a GPU programming expert. Explain CUDA memory hierarchy: global, shared, L1/L2 cache, registers. How do you optimize a matrix multiplication kernel for H100?",
    "You are a Kubernetes expert. Explain how the Kubernetes scheduler works: predicates, priorities, topology awareness, resource requests vs limits, and how to debug scheduling failures.",
    "You are a cryptography engineer. Explain elliptic curve cryptography: the math behind ECDH key exchange, ECDSA signing, why secp256k1 is used in Bitcoin, and quantum resistance concerns.",
    "You are a performance engineer. Explain how CPU branch prediction works, why branch mispredictions are expensive, and how to write branch-friendly code. Include examples in C++.",
    "You are a storage engineer. Explain how modern SSDs work: NAND flash types (SLC/MLC/TLC/QLC), wear leveling, write amplification, FTL, and how these affect database performance.",
    "You are a language model researcher. Explain how large language models are trained: pre-training objectives, tokenization (BPE), gradient checkpointing, mixed precision, and RLHF alignment.",
]

results = [None] * N
lock = threading.Lock()
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE


def send(idx):
    prompt = PROMPTS[idx]
    t0 = time.time()
    ttft = None
    tokens = 0

    payload = json.dumps({
        "model": "gpt-oss:120b",
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
    }).encode()

    req = Request(URL, data=payload, headers={
        "Authorization": f"Bearer {KEY}",
        "Content-Type": "application/json",
    })

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
                    delta = json.loads(data)["choices"][0]["delta"].get("content", "")
                    if delta:
                        if ttft is None:
                            ttft = time.time() - t0
                        tokens += 1
                        with lock:
                            print(f"\033[36m[{idx+1:02d}]\033[0m {delta}", end="", flush=True)
                except Exception:
                    pass

        elapsed = time.time() - t0
        results[idx] = {
            "idx": idx + 1,
            "ttft": ttft or 0,
            "total": elapsed,
            "tokens": tokens,
            "tps": tokens / elapsed if elapsed > 0 else 0,
            "ok": True,
        }
    except Exception as e:
        results[idx] = {"idx": idx + 1, "ok": False, "error": str(e)}
        with lock:
            print(f"\033[31m[{idx+1:02d}] ERROR: {e}\033[0m", flush=True)


print(f"\033[1mLaunching {N} parallel streaming requests → {URL}\033[0m\n")
t_wall = time.time()

threads = [threading.Thread(target=send, args=(i,)) for i in range(N)]
for t in threads:
    t.start()
for t in threads:
    t.join()

wall = time.time() - t_wall

ok = [r for r in results if r and r["ok"]]
fail = [r for r in results if r and not r["ok"]]

print(f"\n\n\033[1m{'='*62}\033[0m")
print(f"\033[1m RESULTS  ({N} parallel requests, wall time: {wall:.1f}s)\033[0m")
print(f"\033[1m{'='*62}\033[0m")
print(f"{'Req':>4}  {'TTFT':>7}  {'Total':>7}  {'Tokens':>7}  {'TPS':>7}  Status")
print(f"{'-'*62}")

for r in results:
    if not r:
        continue
    if r["ok"]:
        print(f"{r['idx']:>4}  {r['ttft']:>6.2f}s  {r['total']:>6.2f}s  {r['tokens']:>7}  {r['tps']:>6.1f}/s  \033[32mOK\033[0m")
    else:
        print(f"{r['idx']:>4}  {'—':>7}  {'—':>7}  {'—':>7}  {'—':>7}  \033[31mFAIL: {r['error'][:30]}\033[0m")

if ok:
    ttfts = sorted(r["ttft"] for r in ok)
    print(f"\n\033[1mSummary ({len(ok)}/{N} OK):\033[0m")
    print(f"  Avg TTFT:     {sum(ttfts)/len(ttfts):.2f}s")
    print(f"  P50 TTFT:     {ttfts[len(ttfts)//2]:.2f}s")
    print(f"  P99 TTFT:     {ttfts[min(int(len(ttfts)*0.99), len(ttfts)-1)]:.2f}s")
    print(f"  Avg TPS/req:  {sum(r['tps'] for r in ok)/len(ok):.1f} tok/s")
    print(f"  Total tokens: {sum(r['tokens'] for r in ok)}")
    print(f"  Wall time:    {wall:.1f}s")
