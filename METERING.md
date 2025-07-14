
# ⏱️ **Metering: Extend the Custom Caddy Module (Best for API Key Association)**

**How it works:**

* Enhance your existing custom Caddy module to:

  1. **Extract the request body** (i.e., the prompt).
  2. Inject a unique `request_id` header into the forwarded request.
  3. Log the prompt size (in tokens or characters) and associate it with the API key.
* On the response from the `auth-gateway`, intercept and log the number of tokens in the output.

**Pros:**

* Full control at the entry point.
* Direct API key mapping (since Caddy handles auth).
* One place to implement rate limits, billing, and audit logging.

**Cons:**

* Caddy plugin API is Go-based — token counting logic needs to be efficient.

**Suggested enhancement:**

* Use a Go tokenizer library like `github.com/pkoukk/tiktoken-go` or `github.com/samber/go-gpt-tokenizer` inside the plugin.
* Alternatively, use approximate character count to tokens conversion for simplicity.


## 🔍 Token Counting:

| Model                                | Lib                        | Notes                                                                              |
| ------------------------------------ | -------------------------- | ---------------------------------------------------------------------------------- |
| OpenAI-compatible (LLaMA, GPT, etc.) | `tiktoken`, `transformers` | Use tokenizer model: `cl100k_base`                                                 |
| Approximate method                   | `len(text.split()) * 1.33` | \~75% accurate, useful for fast estimation                                         |
| Ollama                               | No direct token output     | Use wrapper around response stream or use `/api/generate`'s `eval_count` from logs |


Excellent — given your robust custom Caddy middleware, we can now design a **high-level architecture** to implement **AI Token Metering** with smart contract integration. This design assumes you're processing API traffic that includes natural language prompts and completions — typically JSON payloads destined for services like `auth-gateway`, `ollama`, or `torchserve`.

---

## 🧩 High-Level Design: Token Metering System in Caddy Middleware

### Core Components

#### 1. **TokenCounter**

New internal Go struct responsible for:

* Extracting prompt and completion texts from request/response.
* Calculating token counts.
* Buffering usage per API key.
* Periodically submitting token usage to smart contract.

#### 2. **Middleware Enhancements**

Extend the existing `ServeHTTP` method to:

* Intercept request/response bodies (wrap `http.ResponseWriter`).
* Parse JSON bodies for `prompt`, `messages`, or similar fields.
* Count tokens in both directions.
* Associate counts with the validated API key.

#### 3. **Background Reporter**

A goroutine (launched in `Provision`) that:

* Flushes accumulated token counts every X seconds.
* Batches and submits token usage to Secret Network smart contract (e.g. `report_usage`).
* Optionally persists to local file if submission fails.

---

### ⚙️ Architecture Diagram

```
[Caddy Reverse Proxy]
    |
    |--> [secret_reverse_proxy middleware]
             |
             |--> [Extract API Key]
             |--> [Count Prompt Tokens]
             |--> [Forward Request to auth-gateway]
             |<-- [Capture Completion]
             |--> [Count Completion Tokens]
             |--> [Accumulate Usage: Map[APIKey]TokenStats]
             |
             +--> [Background Goroutine]
                      |
                      +--> [Every 5 mins] --> [Report usage to Smart Contract]
```

---

## Data Model

```go
type TokenUsage struct {
    APIKeyHash string  // Hash of API key
    InputTokens  int
    OutputTokens int
    Timestamp    time.Time
}
```

In-memory accumulator:

```go
type TokenAccumulator struct {
    mu      sync.Mutex
    usage   map[string]*TokenUsage  // key = hashed API key
}
```

### Token Counting

Use a fast tokenizer for LLM-like models (fallback: word count × 1.3):

* **Preferred**: [`github.com/samber/go-gpt-tokenizer`](https://github.com/samber/go-gpt-tokenizer)
* **Fallback**: Count characters or words

Example:

```go
tokenizer := gpt.NewTokenizer(gpt.Cl100kBase)
numTokens := tokenizer.CountTokens(text)
```

---

### 🔐 Smart Contract Submission

Enhance your existing `querycontract` package to support:

```json
{
  "report_usage": {
    "records": [
      { "api_key_hash": "abc123...", "input_tokens": 75, "output_tokens": 310, "timestamp": 17238523 }
    ]
  }
}
```

Batch on a `map[string]TokenUsage`, then reset the buffer.

## Implementation Tasks

| Task                                | Description                                           |
| ----------------------------------- | ----------------------------------------------------- |
| ✅ Add `TokenAccumulator`            | Shared state inside middleware instance               |
| ✅ Intercept request bodies          | Wrap request and extract prompt tokens                |
| ✅ Intercept response body           | Capture streamed or complete response to count tokens |
| ✅ Accumulate usage                  | Use `APIKeyHash` as index                             |
| ✅ Goroutine for reporting           | `go reportLoop()` in `Provision()`                    |
| ✅ Extend smart contract (if needed) | Add `report_usage` method onchain                     |
| ✅ Expose metrics (optional)         | Use Prometheus or file output for local testing       |

---

### Middleware Config in Caddyfile

```caddy
secret_reverse_proxy {
    API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
    contract_address "secret1..."
    master_keys_file "/etc/caddy/master_keys.txt"
    metering_interval 300s
}
```

