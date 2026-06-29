#!/usr/bin/env python3
"""
QPS benchmark — different request profiles, concurrency sweep.

Usage:
  python3 bench_qps.py <API_KEY> [URL] [options]

Options:
  --profile  tiny|short|medium|long|rag|ctx4k|ctx8k|ctx32k|all   (default: all)
  --conc     concurrency levels, comma-separated  (default: 1,4,8,16)
  --n        requests per concurrency level        (default: 16)
  --no-stream  use non-streaming (higher QPS, no TTFT)
  --unique   prepend unique UUID to each request to defeat prefix caching

Examples:
  python3 bench_qps.py sk-xxx
  python3 bench_qps.py sk-xxx --profile short --conc 8,16,32
  python3 bench_qps.py sk-xxx --profile rag --n 8
  python3 bench_qps.py sk-xxx https://secretai-lambda.scrtlabs.com:21434/v1/chat/completions --profile medium
  python3 bench_qps.py sk-xxx --profile medium --unique   # no prefix cache hits
"""
import json, ssl, sys, time, threading, argparse, uuid
from urllib.request import Request, urlopen
from urllib.error import URLError

# ── Long context base text (repeated to hit target token count) ───────────────
_LONG_DOC = (
    "The Linux kernel is a monolithic, modular, multi-tasking Unix-like operating system kernel. "
    "It was first released by Linus Torvalds on September 17, 1991. The kernel manages the system's "
    "resources and provides an interface between hardware and software. "
    "Virtual memory management in Linux uses a hierarchical page table structure. Each process has its "
    "own virtual address space mapped through page global directories (PGD), page upper directories (PUD), "
    "page middle directories (PMD), and page table entries (PTE). The Translation Lookaside Buffer (TLB) "
    "caches recent virtual-to-physical address translations. A TLB miss causes a page table walk, which "
    "is expensive. Huge pages (2MB or 1GB) reduce TLB pressure for large allocations. "
    "The Completely Fair Scheduler (CFS) is the default Linux process scheduler. It maintains a "
    "red-black tree ordered by virtual runtime. The process with the smallest vruntime is scheduled next. "
    "CFS uses nanosecond granularity and adjusts vruntime based on process priority (nice value). "
    "Control groups (cgroups) limit CPU, memory, I/O, and network resources per process group. "
    "Linux networking uses a layered socket buffer (sk_buff) structure. Network packets traverse "
    "the netfilter hooks (PREROUTING, INPUT, FORWARD, OUTPUT, POSTROUTING) where iptables and "
    "nftables rules are evaluated. The kernel supports TCP, UDP, SCTP, DCCP, and raw sockets. "
    "TCP implements Nagle's algorithm, slow start, congestion avoidance, fast retransmit, and "
    "fast recovery. BBR (Bottleneck Bandwidth and RTT) is a modern congestion control algorithm "
    "that estimates bandwidth and RTT rather than reacting to packet loss. "
    "The ext4 filesystem uses a journal for crash consistency. It supports extents, delayed "
    "allocation, and online defragmentation. XFS is better suited for large files and parallel I/O. "
    "Btrfs adds copy-on-write, snapshots, checksumming, and RAID support at the filesystem level. "
    "io_uring is a modern Linux async I/O interface that uses shared ring buffers between kernel "
    "and userspace, avoiding syscall overhead for high-throughput I/O workloads. "
    "eBPF (extended Berkeley Packet Filter) allows running sandboxed programs in the kernel. "
    "It is used for networking (XDP), observability (tracing), and security (seccomp). "
    "eBPF programs are verified by the kernel verifier before loading to ensure safety. "
    "NUMA (Non-Uniform Memory Access) systems have multiple memory nodes. The kernel's NUMA "
    "balancer migrates pages and tasks to reduce cross-node memory access latency. "
    "Memory zones in Linux: ZONE_DMA (0-16MB), ZONE_DMA32 (0-4GB), ZONE_NORMAL, ZONE_HIGHMEM. "
    "The SLUB allocator manages kernel object caches efficiently with per-CPU free lists. "
    "Kernel module loading uses ELF format. Modules export symbols via EXPORT_SYMBOL macros. "
    "The /proc and /sys virtual filesystems expose kernel internals to userspace. "
    "Namespaces (PID, NET, MNT, UTS, IPC, USER, CGROUP) provide isolation for containers. "
    "Seccomp restricts which syscalls a process can invoke, reducing attack surface. "
    "KVM (Kernel-based Virtual Machine) turns Linux into a hypervisor using hardware virtualization "
    "extensions (Intel VT-x, AMD-V). QEMU provides device emulation on top of KVM. "
    "SELinux and AppArmor implement mandatory access control (MAC) policies in the kernel. "
)

def _make_long_prompt(target_chars: int, question: str) -> str:
    repeated = (_LONG_DOC * (target_chars // len(_LONG_DOC) + 1))[:target_chars]
    return (
        "Below is a long technical document. Read it carefully and answer the question "
        "at the end in 2-3 sentences only.\n\n"
        + "=" * 60 + "\n"
        + repeated
        + "\n" + "=" * 60 + "\n\n"
        + question
    )

# ── Profiles ────────────────────────────────────────────────────────────────
PROFILES = {
    "tiny": {
        "desc": "Tiny (5→50 tok)  — max QPS test",
        "prompt": "Say exactly: 'OK'",
        "max_tokens": 10,
    },
    "short": {
        "desc": "Short (50→200 tok) — simple Q&A",
        "prompt": "What is the difference between TCP and UDP? Answer in 3 bullet points.",
        "max_tokens": 200,
    },
    "medium": {
        "desc": "Medium (100→600 tok) — realistic chat",
        "prompt": (
            "You are a senior engineer. Explain the CAP theorem and give a concrete example "
            "of a distributed system that trades consistency for availability. "
            "Include when you would choose each trade-off."
        ),
        "max_tokens": 600,
    },
    "long": {
        "desc": "Long (150→1500 tok) — detailed generation",
        "prompt": (
            "You are a systems architect. Design a fault-tolerant event streaming pipeline "
            "that processes 1 million events per second. Cover: ingestion, partitioning, "
            "consumer groups, backpressure, exactly-once semantics, and failure recovery. "
            "Be specific with technologies and configuration values."
        ),
        "max_tokens": 1500,
    },
    "rag": {
        "desc": "RAG  (1000 ctx→200 tok) — long input, short output",
        "prompt": (
            "Below is a technical document. Answer the question at the end in 2-3 sentences only.\n\n"
            + "=" * 60 + "\n"
            + (
                "Apache Kafka is a distributed event streaming platform. It stores records in topics, "
                "which are partitioned and replicated across brokers for durability and throughput. "
                "Producers append records to the end of a partition log; consumers read from an offset "
                "they maintain independently. Kafka guarantees at-least-once delivery by default. "
                "Exactly-once semantics (EOS) require idempotent producers and transactional APIs. "
                "Consumer groups allow horizontal scaling: each partition is assigned to exactly one "
                "consumer within a group. Lag is the difference between the latest offset and the "
                "consumer's current offset. High lag indicates the consumer cannot keep up. "
                "Kafka retains data for a configurable period (default 7 days), enabling replay. "
                "ZooKeeper was historically used for coordination; KRaft mode (Kafka Raft) removes "
                "this dependency in modern versions. Kafka Streams is a client library for stateful "
                "stream processing directly on Kafka topics without an external cluster. "
            ) * 4  # repeat to make a ~800 token context
            + "\n" + "=" * 60 + "\n\n"
            "Question: What happens when a Kafka consumer cannot keep up with the producer rate?"
        ),
        "max_tokens": 150,
    },
    "ctx4k": {
        "desc": "Ctx-4k (~4k in→200 tok) — long context, short answer",
        "prompt": _make_long_prompt(
            16_000,  # ~4k tokens (1 token ≈ 4 chars)
            "Question: What is eBPF and what are its main use cases in the Linux kernel?"
        ),
        "max_tokens": 200,
        "timeout": 120,
    },
    "ctx8k": {
        "desc": "Ctx-8k (~8k in→200 tok) — long context stress test",
        "prompt": _make_long_prompt(
            32_000,  # ~8k tokens
            "Question: Explain the difference between SLUB and SLOB memory allocators in Linux."
        ),
        "max_tokens": 200,
        "timeout": 180,
    },
    "ctx32k": {
        "desc": "Ctx-32k (~32k in→300 tok) — large context window",
        "prompt": _make_long_prompt(
            128_000,  # ~32k tokens
            "Question: Summarize the key differences between XFS, ext4, and Btrfs filesystems."
        ),
        "max_tokens": 300,
        "timeout": 600,
    },
}
ALL_PROFILES = list(PROFILES.keys())

# ── Args ─────────────────────────────────────────────────────────────────────
def parse_args():
    p = argparse.ArgumentParser(add_help=False)
    p.add_argument("key",         nargs="?", default="")
    p.add_argument("url",         nargs="?",
                   default="https://secretai-lambda4.scrtlabs.com:21434/v1/chat/completions")
    p.add_argument("--model",     default="gemma4:31b",
                   help="Model name to benchmark (default: gemma4:31b)")
    p.add_argument("--profile",   default="all")
    p.add_argument("--conc",      default="1,4,8,16")
    p.add_argument("--n",         type=int, default=16)
    p.add_argument("--no-stream", action="store_true")
    p.add_argument("--unique",    action="store_true",
                   help="Prepend unique UUID to each request to defeat prefix caching")
    p.add_argument("-h", "--help", action="store_true")
    return p.parse_args()

# ── HTTP ──────────────────────────────────────────────────────────────────────
ctx = ssl.create_default_context()
ctx.check_hostname = False
ctx.verify_mode = ssl.CERT_NONE

def do_request(url, key, profile_cfg, stream=True, model="gemma4:31b", unique=False):
    prompt = profile_cfg["prompt"]
    if unique:
        prompt = f"[{uuid.uuid4()}]\n{prompt}"
    payload = json.dumps({
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "max_tokens": profile_cfg["max_tokens"],
        "stream": stream,
    }).encode()

    req = Request(url, data=payload, headers={
        "Authorization": f"Bearer {key}",
        "Content-Type": "application/json",
    })

    t0 = time.time()
    ttft = None
    tokens = 0

    try:
        timeout = max(180, profile_cfg.get("timeout", 180))
        with urlopen(req, context=ctx, timeout=timeout) as resp:
            if stream:
                for raw in resp:
                    line = raw.decode().strip()
                    if not line.startswith("data:"):
                        continue
                    data = line[5:].strip()
                    if data == "[DONE]":
                        break
                    try:
                        obj = json.loads(data)
                        delta = obj["choices"][0]["delta"].get("content", "")
                        if delta:
                            if ttft is None:
                                ttft = time.time() - t0
                            tokens += 1
                    except Exception:
                        pass
            else:
                body = json.loads(resp.read())
                ttft = time.time() - t0
                tokens = body.get("usage", {}).get("completion_tokens", 0)

        elapsed = time.time() - t0
        return {"ok": True, "ttft": ttft or elapsed, "total": elapsed,
                "tokens": tokens, "tps": tokens / elapsed if elapsed > 0 else 0}
    except Exception as e:
        return {"ok": False, "error": str(e), "total": time.time() - t0}

# ── Concurrency runner ────────────────────────────────────────────────────────
def run_concurrent(url, key, profile_cfg, n, stream, model="gemma4:31b", unique=False):
    results = [None] * n
    lock = threading.Lock()

    def worker(i):
        try:
            r = do_request(url, key, profile_cfg, stream, model, unique)
        except Exception as e:
            r = {"ok": False, "error": f"{type(e).__name__}: {e}", "total": 0}
        results[i] = r
        status = "\033[32m✓\033[0m" if r["ok"] else "\033[31m✗\033[0m"
        with lock:
            print(f"  {status}", end="", flush=True)

    t0 = time.time()
    threads = [threading.Thread(target=worker, args=(i,)) for i in range(n)]
    for t in threads: t.start()
    for t in threads: t.join()
    wall = time.time() - t0
    print()
    return results, wall

# ── Stats ─────────────────────────────────────────────────────────────────────
def stats(results, wall, n):
    ok = [r for r in results if r and r["ok"]]
    fail = len(results) - len(ok)
    if not ok:
        none_count = sum(1 for r in results if r is None)
        if none_count:
            print(f"    {none_count} worker(s) crashed silently")
        for r in results:
            if r and not r["ok"]:
                print(f"    ERROR: {r.get('error', '?')[:200]}")
                break
        return None
    ttfts = sorted(r["ttft"] for r in ok)
    total_tok = sum(r["tokens"] for r in ok)
    avg_tps = sum(r["tps"] for r in ok) / len(ok)
    qps = len(ok) / wall
    return {
        "ok": len(ok), "fail": fail, "n": n,
        "qps": qps,
        "ttft_p50": ttfts[len(ttfts) // 2],
        "ttft_p99": ttfts[min(int(len(ttfts) * 0.99), len(ttfts) - 1)],
        "ttft_avg": sum(ttfts) / len(ttfts),
        "avg_tps": avg_tps,
        "total_tok": total_tok,
        "tok_per_sec": total_tok / wall,
        "wall": wall,
    }

# ── Main ──────────────────────────────────────────────────────────────────────
def main():
    args = parse_args()
    if args.help or not args.key:
        print(__doc__)
        sys.exit(0)

    profiles = ALL_PROFILES if args.profile == "all" else [p.strip() for p in args.profile.split(",")]
    conc_levels = [int(c) for c in args.conc.split(",")]
    stream = not args.no_stream

    print(f"\033[1mQPS Benchmark → {args.url}\033[0m")
    cache_mode = "unique (no prefix cache)" if args.unique else "shared (prefix cache ON)"
    print(f"Model: {args.model} | Mode: {'streaming' if stream else 'non-streaming'} | "
          f"N per level: {args.n} | Concurrency: {conc_levels} | Cache: {cache_mode}\n")

    for pname in profiles:
        if pname not in PROFILES:
            print(f"Unknown profile: {pname}. Choose: {', '.join(ALL_PROFILES)}")
            continue
        pcfg = PROFILES[pname]
        print(f"\033[1;34m{'━'*64}\033[0m")
        print(f"\033[1m{pcfg['desc']}\033[0m  (max_tokens={pcfg['max_tokens']})")
        print(f"\033[1;34m{'━'*64}\033[0m")
        print(f"{'Conc':>5}  {'QPS':>6}  {'TTFT p50':>9}  {'TTFT p99':>9}  "
              f"{'TPS/req':>8}  {'Tok/s':>8}  {'OK/N':>6}")
        print(f"{'─'*64}")

        for conc in conc_levels:
            n = max(args.n, conc)  # at least one round
            print(f"  c={conc:2d}  ", end="", flush=True)
            results, wall = run_concurrent(args.url, args.key, pcfg, n, stream, args.model, args.unique)
            s = stats(results, wall, n)
            if not s:
                print(f"{'ALL FAILED':>60}")
                continue
            print(f"{conc:>5}  {s['qps']:>5.2f}/s  {s['ttft_p50']:>7.2f}s  "
                  f"{s['ttft_p99']:>7.2f}s  {s['avg_tps']:>7.1f}/s  "
                  f"{s['tok_per_sec']:>7.0f}/s  {s['ok']}/{n}")
        print()

    print("\033[1mDone.\033[0m")

if __name__ == "__main__":
    main()
