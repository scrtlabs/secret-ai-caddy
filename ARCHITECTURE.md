# Secret AI Caddy - Architecture Documentation

## Overview

The Secret AI Caddy is a comprehensive Caddy middleware that provides secure API key authentication, intelligent token usage metering, and comprehensive metrics collection for AI/ML API gateways. It validates API keys against multiple sources including local master keys, file-based keys, and Secret Network smart contracts with performance-optimized caching.

## High-Level Architecture

```mermaid
graph TB
    subgraph "Client Layer"
        C[AI/ML Clients<br/>with API Keys]
        WEB[Web Applications]
        CLI[CLI Tools]
        SDKs[Language SDKs]
    end
    
    subgraph "Load Balancing"
        LB[Load Balancer<br/>nginx/cloudflare]
    end
    
    subgraph "Caddy Gateway Layer"
        subgraph "Middleware Chain"
            CORS[CORS Handler]
            SRP[Secret Reverse Proxy<br/>Middleware]
            RP[Reverse Proxy<br/>Handler]
        end
        
        subgraph "Enhanced Features"
            AUTH[API Key Authentication]
            METER[Token Metering System]
            METRICS[Metrics Collection]
            CACHE[Response Caching]
        end
    end
    
    subgraph "Authentication Sources"
        MK[Master Key<br/>Configuration]
        MKF[Master Keys<br/>File Storage]
        CACHE_AUTH[In-Memory<br/>Auth Cache]
    end
    
    subgraph "External Services"
        SC[Secret Network<br/>Smart Contract]
        
        subgraph "AI/ML Backends"
            OPENAI[OpenAI API]
            OLLAMA[Ollama Server]
            TORCH[TorchServe]
            CUSTOM[Custom ML APIs]
        end
    end
    
    subgraph "Data & Analytics"
        BLOCKCHAIN[Secret Network<br/>Usage Reporting]
        METRICS_DB[Metrics Storage]
        LOG_STORE[Log Aggregation]
    end
    
    C --> LB
    WEB --> LB
    CLI --> LB
    SDKs --> LB
    
    LB --> CORS
    CORS --> SRP
    SRP --> AUTH
    AUTH --> METER
    METER --> METRICS
    METRICS --> RP
    
    AUTH --> MK
    AUTH --> MKF
    AUTH --> CACHE_AUTH
    AUTH -->|Cache Miss| SC
    
    RP --> OPENAI
    RP --> OLLAMA
    RP --> TORCH
    RP --> CUSTOM
    
    METRICS --> BLOCKCHAIN
    METRICS --> METRICS_DB
    SRP --> LOG_STORE
```

## Core Module Architecture

```mermaid
classDiagram
    class Middleware {
        -config: *Config
        -validator: *APIKeyValidator
        -quitChan: chan struct
        -meteringRunning: bool
        -tokenCounter: TokenCounter
        -bodyHandler: BodyHandler
        -tokenAccumulator: *TokenAccumulator
        -resilientReporter: *ResilientReporter
        -metricsCollector: MetricsCollector
        -authVerifier: *x402.AuthVerifierImpl
        -quoteEngine: *x402.QuoteEngineImpl
        -ledger: *x402.LedgerImpl
        -settlementEngine: *x402.SettlementEngineImpl
        -challengeBuilder: *x402.ChallengeBuilderImpl
        -secretVMClient: *x402.SecretVMClientImpl
        -reconciler: *x402.ReconcilerImpl
        +CaddyModule() ModuleInfo
        +Provision(ctx: Context) error
        +Validate() error
        +ServeHTTP(w, r, next) error
        +UnmarshalCaddyfile(d) error
        +Cleanup() error
        -serveX402(w, r, next) error
    }
    
    class Config {
        +APIKey: string
        +MasterKeysFile: string
        +PermitFile: string
        +ContractAddress: string
        +SecretNode: string
        +SecretChainID: string
        +CacheTTL: Duration
        +Metering: bool
        +MeteringInterval: Duration
        +MeteringURL: string
        +MaxBodySize: int64
        +TokenCountingMode: string
        +MaxRetries: int
        +RetryBackoff: Duration
        +EnableMetrics: bool
        +MetricsPath: string
    }
    
    class APIKeyValidator {
        -config: *Config
        -cache: map[string]bool
        -cacheMutex: RWMutex
        -lastUpdate: Time
        +ValidateAPIKey(key: string) (bool, error)
        +CheckMasterKeys(key: string) (bool, error)
        +UpdateAPIKeyCache() error
        +CleanupCache()
        +CacheSize() int
        +LastUpdate() Time
    }
    
    class TokenCounter {
        -logger: *Logger
        +CountTokens(content, contentType: string) int
        +ValidateTokenCount(tokens, contentLength: int) int
        -countJSONTokens(content: string) int
        -countTextTokens(text: string) int
        -extractTextFromJSON(data: interface{}) []string
    }
    
    class BodyHandler {
        -maxBodySize: int64
        -maxBufferSize: int64
        -logger: *Logger
        -compressionEnabled: bool
        -bufferPool: *sync.Pool
        +SafeReadRequestBody(r: *Request) (*RequestBodyInfo, error)
        +GetContentType(r: *Request) string
        +IsTokenCountableContent(contentType: string) bool
        +ValidateRequestSize(r: *Request) error
    }
    
    class TokenAccumulator {
        -mu: Mutex
        -usage: map[string]*TokenUsage
        +RecordUsage(apiKeyHash: string, input, output: int)
        +FlushUsage() map[string]TokenUsage
        +PeekUsage() map[string]TokenUsage
    }
    
    class ResilientReporter {
        -config: *Config
        -accumulator: *TokenAccumulator
        -logger: *Logger
        -failedReportsDir: string
        -maxRetries: int
        -retryBackoff: Duration
        -stopChan: chan struct
        -running: bool
        +StartReportingLoop(interval: Duration)
        +Stop()
        +GetFailedReportsCount() int
        -processCurrentUsage()
        -submitWithRetry(records: []map, attempt: int) error
        -persistFailedReport(records: []map)
    }
    
    class MetricsCollector {
        -config: Config
        -mu: RWMutex
        -requestCount: int64
        -authorizedCount: int64
        -rejectedCount: int64
        -totalInputTokens: int64
        -totalOutputTokens: int64
        -startTime: Time
        +RecordRequest()
        +RecordAuthorized()
        +RecordRejected()
        +RecordTokens(input, output: int64)
        +GetMetrics() map[string]any
        +ServeMetrics(w: ResponseWriter, r: *Request)
    }
    
    class QueryContract {
        +QueryContract(address: string, query: map) (map, error)
        +NewWASMContext(context: map) *WASMContext
        +fetchCodeHash(address: string) (string, error)
    }
    
    class WASMContext {
        -cliContext: map[string]string
        -testKeyPairPath: string
        -nonce: []byte
        +Encrypt(data: []byte) ([]byte, error)
        +Decrypt(data: []byte) ([]byte, error)
        -getTxSenderKeyPair() ([]byte, []byte, error)
        -getConsensusIOPubKey() ([]byte, error)
        -getTxEncryptionKey(privKey: []byte) ([]byte, error)
    }
    
    Middleware --> Config
    Middleware --> APIKeyValidator
    Middleware --> TokenCounter
    Middleware --> BodyHandler
    Middleware --> TokenAccumulator
    Middleware --> ResilientReporter
    Middleware --> MetricsCollector
    APIKeyValidator --> Config
    APIKeyValidator --> QueryContract
    ResilientReporter --> TokenAccumulator
    QueryContract --> WASMContext
```

## x402 Payment Protocol Architecture

The x402 subsystem adds prepaid metering and payment enforcement for AI agent requests. It runs as a parallel authentication path — requests with `X-Agent-Address` headers take the x402 path; all other requests use legacy API key validation.

### x402 Component Map

```
secret-reverse-proxy/
├── x402/
│   ├── types.go              # Shared types, errors, header constants
│   ├── auth_verifier.go      # HMAC-SHA256 signature verification
│   ├── quote_engine.go       # Per-model cost estimation
│   ├── ledger.go             # In-memory per-agent balance store
│   ├── challenge.go          # 402 Payment Required response builder
│   ├── settlement.go         # Post-response charge + refund
│   ├── secretvm_client.go    # Authenticated SecretVM API client
│   └── reconciler.go         # Background balance sync from billing backend
```

### x402 Request Flow

```mermaid
sequenceDiagram
    participant Client as Agent
    participant Caddy as Caddy (serveX402)
    participant Ledger as SpendableLedger
    participant Upstream as AI Backend
    participant Reconciler as Reconciler

    Client->>Caddy: POST /v1/chat/completions<br/>X-Agent-Address + Signature + Timestamp
    Caddy->>Caddy: Verify HMAC-SHA256 signature
    alt Invalid signature or stale timestamp
        Caddy-->>Client: 401 Unauthorized
    end
    Caddy->>Caddy: Estimate cost (QuoteEngine)
    Caddy->>Ledger: Reserve(agent, estimatedCost)
    alt Insufficient balance
        Caddy->>Reconciler: ForceSync(agent) [lazy hydration]
        Caddy->>Ledger: Retry Reserve
        alt Still insufficient
            Caddy-->>Client: 402 Payment Required (challenge JSON)
        end
    end
    Caddy->>Upstream: Forward request
    Upstream-->>Caddy: Response
    Caddy->>Caddy: Count actual output tokens
    Caddy->>Ledger: Commit(reservation, actualCost)<br/>Refund unused reserve
    Caddy-->>Client: 200 OK + response
```

### Balance Semantics

The `SpendableLedger` uses an **available balance** model:

- `Balance` = funds available for new reservations (excludes reserved amounts)
- `Reserve` deducts from balance immediately
- `Release` refunds the full reserved amount to balance
- `Commit` refunds `(reserved - actual)` to balance
- `Reserved` is a read-only sum of outstanding reservations, for observability

Concurrency: `sync.RWMutex` for the top-level agent map, plus a per-agent `sync.Mutex` for reserve/commit/release to avoid global contention.

### Reconciler & Cold Start

The Reconciler runs as a background goroutine:
- On startup: discovers all funded agents via `SecretVMClient.ListAgents()` and syncs balances
- Periodically: re-syncs known agents (configurable interval, default 30s)
- Lazy hydration: `ForceSync(agent)` handles the first request from an unknown agent after a restart

### Interaction with Existing Components

| Existing Component | Change |
|--------------------|--------|
| `Config` | Added 11 `X402*` fields (all optional, backward compatible) |
| `ServeHTTP` | Added `IsAgentRequest` branch before legacy API key path |
| `TokenCounter` | Reused by QuoteEngine for input token estimation |
| `BodyHandler` | Reused for request body reading |
| `TokenAccumulator` | x402 records usage through it |
| `MetricsCollector` | x402 records authorized/token metrics through it |
| `Provision` | Initializes x402 components when enabled |
| `Cleanup` | Stops Reconciler and ledger cleanup |
| `Validate` | Validates required x402 fields when enabled |
| `UnmarshalCaddyfile` | Parses 11 `x402_*` directives |

For full x402 documentation see [x402.md](./x402.md). For detailed design documents see [design/x402/](./design/x402/).

## Enhanced Token Metering System

```mermaid
graph TB
    subgraph "Request Processing"
        REQ[Incoming Request]
        AUTH_CHECK[API Key Validation]
        BODY_READ[Request Body Reading]
        TOKEN_COUNT_IN[Input Token Counting]
    end
    
    subgraph "Token Counting Engine"
        subgraph "Content Analysis"
            JSON_PARSE[JSON Parser]
            TEXT_EXTRACT[Text Extractor]
            CONTENT_TYPE[Content-Type Analysis]
        end
        
        subgraph "Counting Algorithms"
            ACCURATE[Accurate Mode<br/>JSON + Field Analysis]
            FAST[Fast Mode<br/>Heuristic Estimation]
            HEURISTIC[Heuristic Mode<br/>Character-based]
        end
        
        subgraph "Token Validation"
            SANITY_CHECK[Sanity Checks]
            RANGE_VALIDATION[Range Validation]
            ERROR_CORRECTION[Error Correction]
        end
    end
    
    subgraph "Response Processing"
        RESP_CAPTURE[Response Capture]
        RESP_BUFFER[Response Buffering]
        TOKEN_COUNT_OUT[Output Token Counting]
        USAGE_RECORD[Usage Recording]
    end
    
    subgraph "Usage Accumulation"
        KEY_HASH[API Key Hashing]
        USAGE_MAP[Usage Map<br/>Per API Key]
        THREAD_SAFE[Thread-Safe Updates]
    end
    
    subgraph "Resilient Reporting"
        TIMER[Reporting Timer]
        BATCH_PREPARE[Batch Preparation]
        CONTRACT_SUBMIT[Contract Submission]
        RETRY_LOGIC[Retry Logic]
        PERSISTENCE[Failed Report Persistence]
    end
    
    REQ --> AUTH_CHECK
    AUTH_CHECK -->|✓ Valid| BODY_READ
    BODY_READ --> TOKEN_COUNT_IN
    
    TOKEN_COUNT_IN --> JSON_PARSE
    JSON_PARSE --> TEXT_EXTRACT
    TEXT_EXTRACT --> CONTENT_TYPE
    
    CONTENT_TYPE --> ACCURATE
    CONTENT_TYPE --> FAST
    CONTENT_TYPE --> HEURISTIC
    
    ACCURATE --> SANITY_CHECK
    FAST --> SANITY_CHECK
    HEURISTIC --> SANITY_CHECK
    
    SANITY_CHECK --> RANGE_VALIDATION
    RANGE_VALIDATION --> ERROR_CORRECTION
    
    ERROR_CORRECTION --> RESP_CAPTURE
    RESP_CAPTURE --> RESP_BUFFER
    RESP_BUFFER --> TOKEN_COUNT_OUT
    TOKEN_COUNT_OUT --> USAGE_RECORD
    
    USAGE_RECORD --> KEY_HASH
    KEY_HASH --> USAGE_MAP
    USAGE_MAP --> THREAD_SAFE
    
    THREAD_SAFE --> TIMER
    TIMER --> BATCH_PREPARE
    BATCH_PREPARE --> CONTRACT_SUBMIT
    CONTRACT_SUBMIT -->|Failure| RETRY_LOGIC
    RETRY_LOGIC -->|Max Retries| PERSISTENCE
    CONTRACT_SUBMIT -->|Success| TIMER
```

## Authentication Flow Sequence

```mermaid
sequenceDiagram
    participant Client
    participant Middleware
    participant Validator
    participant MasterKeys
    participant Cache
    participant Contract
    participant Backend
    participant Reporter
    
    Client->>Middleware: HTTP Request with API Key
    Middleware->>Middleware: Extract API Key from Header
    
    alt Invalid/Missing Header
        Middleware-->>Client: 401 Unauthorized
    end
    
    Middleware->>Validator: ValidateAPIKey(key)
    
    Note over Validator: Tier 1: Master Key Check
    Validator->>Validator: Check Configured Master Key
    alt Master Key Match
        Validator-->>Middleware: true, nil
        Middleware->>Middleware: Record Authentication Success
        Middleware->>Backend: Forward Request
        Backend-->>Middleware: Response
        Middleware->>Middleware: Count Response Tokens
        Middleware->>Reporter: Record Usage
        Middleware-->>Client: Response
    end
    
    Note over Validator: Tier 2: Master Keys File
    Validator->>MasterKeys: Check File Contents
    MasterKeys-->>Validator: Found/Not Found
    alt File Key Match
        Validator-->>Middleware: true, nil
        Middleware->>Backend: Forward Request
        Backend-->>Middleware: Response
        Middleware->>Reporter: Record Usage
        Middleware-->>Client: Response
    end
    
    Note over Validator: Tier 3: Cache Check
    Validator->>Cache: Lookup Hashed Key
    Cache-->>Validator: Cached Result + Freshness
    alt Cache Hit & Fresh
        Validator-->>Middleware: cached_result, nil
        alt Valid Key
            Middleware->>Backend: Forward Request
            Backend-->>Middleware: Response
            Middleware->>Reporter: Record Usage
            Middleware-->>Client: Response
        else Invalid Key
            Middleware-->>Client: 401 Unauthorized
        end
    end
    
    Note over Validator: Tier 4: Contract Query
    Validator->>Contract: Query Valid Keys
    Contract-->>Validator: Valid Key Hashes
    Validator->>Cache: Update Cache
    Validator->>Cache: Lookup Updated Result
    Cache-->>Validator: Final Result
    
    alt Valid Key
        Validator-->>Middleware: true, nil
        Middleware->>Backend: Forward Request
        Backend-->>Middleware: Response
        Middleware->>Reporter: Record Usage
        Middleware-->>Client: Response
    else Invalid Key
        Validator-->>Middleware: false, nil
        Middleware-->>Client: 401 Unauthorized
    end
```

## Token Counting Pipeline

```mermaid
flowchart TB
    subgraph "Input Processing"
        A[HTTP Request] --> B[Extract Content-Type]
        B --> C[Read Request Body]
        C --> D[Decompress if Needed]
    end
    
    subgraph "Content Analysis"
        D --> E{Content Type?}
        E -->|application/json| F[Parse JSON]
        E -->|text/*| G[Extract Plain Text]
        E -->|other| H[Generic Processing]
        
        F --> I[Extract Text Fields]
        I --> J[Identify Prompt Fields]
        J --> K[Extract Message Content]
        
        G --> L[Clean Text]
        H --> M[Convert to String]
    end
    
    subgraph "Token Counting"
        K --> N{Counting Mode?}
        L --> N
        M --> N
        
        N -->|accurate| O[JSON Field Analysis]
        N -->|fast| P[Word Count × 1.33]
        N -->|heuristic| Q[Character Count ÷ 4]
        
        O --> R[Sum All Text Fields]
        P --> S[Apply Multiplier]
        Q --> T[Apply Division]
    end
    
    subgraph "Validation & Adjustment"
        R --> U[Validate Token Count]
        S --> U
        T --> U
        
        U --> V{Count > Content Length?}
        V -->|Yes| W[Apply Conservative Fallback]
        V -->|No| X{Count < 1 but Content > 0?}
        X -->|Yes| Y[Set Minimum = 1]
        X -->|No| Z[Return Count]
        
        W --> Z
        Y --> Z
    end
    
    subgraph "Response Processing"
        Z --> AA[Process Backend Response]
        AA --> BB[Apply Same Logic to Response]
        BB --> CC[Record Input + Output Tokens]
    end
    
    CC --> DD[Update Usage Map]
```

## Metrics Collection Architecture

```mermaid
graph TB
    subgraph "Data Collection Points"
        subgraph "Request Metrics"
            REQ_COUNT[Request Count]
            AUTH_SUCCESS[Authorization Success]
            AUTH_FAILURE[Authorization Failure]
            RATE_LIMITED[Rate Limited]
        end
        
        subgraph "Token Metrics"
            INPUT_TOKENS[Input Token Count]
            OUTPUT_TOKENS[Output Token Count]
            TOKEN_ERRORS[Token Count Errors]
        end
        
        subgraph "Performance Metrics"
            PROCESSING_TIME[Processing Time]
            TOKEN_COUNT_TIME[Token Counting Time]
            CACHE_PERFORMANCE[Cache Hit/Miss]
            CONTRACT_LATENCY[Contract Query Time]
        end
        
        subgraph "Error Metrics"
            VALIDATION_ERRORS[Validation Errors]
            REPORTING_ERRORS[Reporting Errors]
            SYSTEM_ERRORS[System Errors]
        end
    end
    
    subgraph "Metrics Aggregation"
        COLLECTOR[MetricsCollector]
        THREAD_SAFE_STORAGE[Thread-Safe Storage]
        CALCULATION_ENGINE[Calculation Engine]
    end
    
    subgraph "Metrics Exposure"
        HTTP_ENDPOINT[/metrics HTTP Endpoint]
        JSON_FORMAT[JSON Format Response]
        REAL_TIME[Real-Time Updates]
    end
    
    subgraph "Analytics & Monitoring"
        DASHBOARD[Monitoring Dashboard]
        ALERTS[Alert System]
        ANALYTICS[Usage Analytics]
    end
    
    REQ_COUNT --> COLLECTOR
    AUTH_SUCCESS --> COLLECTOR
    AUTH_FAILURE --> COLLECTOR
    RATE_LIMITED --> COLLECTOR
    
    INPUT_TOKENS --> COLLECTOR
    OUTPUT_TOKENS --> COLLECTOR
    TOKEN_ERRORS --> COLLECTOR
    
    PROCESSING_TIME --> COLLECTOR
    TOKEN_COUNT_TIME --> COLLECTOR
    CACHE_PERFORMANCE --> COLLECTOR
    CONTRACT_LATENCY --> COLLECTOR
    
    VALIDATION_ERRORS --> COLLECTOR
    REPORTING_ERRORS --> COLLECTOR
    SYSTEM_ERRORS --> COLLECTOR
    
    COLLECTOR --> THREAD_SAFE_STORAGE
    THREAD_SAFE_STORAGE --> CALCULATION_ENGINE
    
    CALCULATION_ENGINE --> HTTP_ENDPOINT
    HTTP_ENDPOINT --> JSON_FORMAT
    JSON_FORMAT --> REAL_TIME
    
    REAL_TIME --> DASHBOARD
    REAL_TIME --> ALERTS
    REAL_TIME --> ANALYTICS
```

## Cache Update Flow

```mermaid
sequenceDiagram
    participant Validator
    participant PermitFile
    participant Contract
    participant Cache
    participant Logger
    participant Metrics
    
    Note over Validator: Cache Update Triggered (TTL Expired)
    Validator->>Logger: Debug: Starting cache update
    Validator->>Metrics: Record Cache Miss
    
    alt Permit File Configured
        Validator->>PermitFile: Read JSON Permit
        PermitFile-->>Validator: Permit Data
    else Default Permit
        Validator->>Validator: Generate Default Permit
    end
    
    Validator->>Contract: QueryContract(address, query)
    
    Note over Contract: Encrypted Communication<br/>with Secret Network
    Contract->>Contract: Fetch Code Hash
    Contract->>Contract: Encrypt Query
    Contract->>Contract: Send HTTP Request
    Contract->>Contract: Decrypt Response
    
    Contract-->>Validator: API Keys Response
    
    alt Query Success
        Validator->>Validator: Parse API Keys Array
        
        loop For Each Entry
            Validator->>Validator: Extract hashed_key
            Validator->>Validator: Validate Hash Format
            Validator->>Cache: Store in New Cache Map
        end
        
        Validator->>Cache: Atomically Replace Old Cache
        Validator->>Validator: Update lastUpdate Timestamp
        Validator->>Logger: Info: Cache update completed
        Validator->>Metrics: Record Successful Cache Update
        Validator->>Metrics: Update Cache Size Metric
        
    else Query Failed
        Validator->>Logger: Error: Contract query failed
        Validator->>Metrics: Record Cache Update Error
        Validator-->>Validator: Return Error (Keep Old Cache)
    end
```

## Configuration and Initialization

```mermaid
stateDiagram-v2
    [*] --> ModuleLoad: Package Import
    
    ModuleLoad --> CaddyfileParse: Caddy Starts
    CaddyfileParse --> ConfigCreate: Parse Directives
    
    state ConfigCreate {
        [*] --> DefaultConfig: Create Default Config
        DefaultConfig --> ParseDirectives: Apply Caddyfile Settings
        
        state ParseDirectives {
            [*] --> APIKey: API_MASTER_KEY
            [*] --> MasterFile: master_keys_file
            [*] --> PermitFile: permit_file
            [*] --> Contract: contract_address
            [*] --> SecretNode: secret_node
            [*] --> ChainID: secret_chain_id
            [*] --> MeteringConfig: metering settings
            [*] --> AdvancedConfig: advanced settings
            
            APIKey --> ConfigReady
            MasterFile --> ConfigReady
            PermitFile --> ConfigReady
            Contract --> ConfigReady
            SecretNode --> ConfigReady
            ChainID --> ConfigReady
            MeteringConfig --> ConfigReady
            AdvancedConfig --> ConfigReady
        }
        
        ConfigReady --> [*]
    }
    
    ConfigCreate --> Provision: Config Complete
    
    state Provision {
        [*] --> CreateValidator: Initialize APIKeyValidator
        CreateValidator --> InitializeEnhancedComponents: Create Metering Components
        
        state InitializeEnhancedComponents {
            [*] --> TokenCounter: Create TokenCounter
            TokenCounter --> BodyHandler: Create BodyHandler
            BodyHandler --> MetricsCollector: Create MetricsCollector
            MetricsCollector --> TokenAccumulator: Create TokenAccumulator
            TokenAccumulator --> ResilientReporter: Create ResilientReporter
            ResilientReporter --> StartReporting: Start Background Reporting
            StartReporting --> [*]
        }
        
        InitializeEnhancedComponents --> LogConfiguration: Log Initialization Success
        LogConfiguration --> [*]
    }
    
    Provision --> Validate: Provision Success
    
    state Validate {
        [*] --> CheckConfig: Validate Config Not Nil
        CheckConfig --> CheckContract: Validate Contract Address
        CheckContract --> CheckSecretNode: Validate Secret Node
        CheckSecretNode --> CheckTTL: Validate Cache TTL
        CheckTTL --> CheckFiles: Check File Accessibility
        CheckFiles --> CheckMeteringConfig: Validate Metering Settings
        CheckMeteringConfig --> ValidationComplete: All Checks Pass
        
        CheckConfig --> ValidationFailed: Config Nil
        CheckContract --> ValidationFailed: No Contract
        CheckSecretNode --> ValidationFailed: No Secret Node
        CheckTTL --> ValidationFailed: TTL <= 0
        CheckFiles --> ValidationFailed: Files Inaccessible
        CheckMeteringConfig --> ValidationFailed: Invalid Metering Config
    }
    
    Validate --> Ready: Validation Success
    Validate --> [*]: Validation Failed
    Ready --> ServeHTTP: Request Received
    
    ServeHTTP --> ProcessRequest: Handle HTTP Request
    
    state ProcessRequest {
        [*] --> ExtractAPIKey: Get Authorization Header
        ExtractAPIKey --> ValidateKey: Validate Against Sources
        ValidateKey --> CountTokens: Read & Count Request Tokens
        CountTokens --> ForwardRequest: Send to Backend
        ForwardRequest --> CaptureResponse: Capture Response
        CaptureResponse --> CountResponseTokens: Count Output Tokens
        CountResponseTokens --> RecordUsage: Update Usage Statistics
        RecordUsage --> RecordMetrics: Update System Metrics
        RecordMetrics --> [*]
    }
```

## Deployment Architecture

```mermaid
graph TB
    subgraph "Load Balancing Tier"
        LB1[Load Balancer 1<br/>nginx/cloudflare]
        LB2[Load Balancer 2<br/>nginx/cloudflare]
    end
    
    subgraph "Caddy Gateway Tier"
        C1[Caddy Instance 1<br/>secret-ai-caddy]
        C2[Caddy Instance 2<br/>secret-ai-caddy]
        C3[Caddy Instance 3<br/>secret-ai-caddy]
        
        subgraph "Shared Storage"
            MKS[Master Keys<br/>Shared Volume]
            PERMITS[Permit Files<br/>Shared Volume]
        end
    end
    
    subgraph "Backend Services Tier"
        subgraph "AI/ML Services"
            API1[OpenAI Proxy 1]
            API2[OpenAI Proxy 2]
            OLLAMA1[Ollama Instance 1]
            OLLAMA2[Ollama Instance 2]
            TORCH1[TorchServe 1]
            TORCH2[TorchServe 2]
        end
        
        subgraph "Support Services"
            ECHO[Echo Server<br/>Testing]
            HEALTH[Health Check<br/>Service]
        end
    end
    
    subgraph "Authentication Infrastructure"
        SN[Secret Network<br/>Blockchain]
        subgraph "Network Nodes"
            NODE1[Secret Node 1]
            NODE2[Secret Node 2]
            NODE3[Secret Node 3]
        end
    end
    
    subgraph "Monitoring & Analytics"
        subgraph "Metrics Collection"
            PROM[Prometheus]
            GRAFANA[Grafana Dashboard]
        end
        
        subgraph "Logging"
            LOGS[Centralized Logging<br/>ELK Stack]
            ALERTS[Alert Manager]
        end
        
        subgraph "Usage Analytics"
            ANALYTICS[Usage Analytics<br/>Database]
            REPORTS[Reporting Service]
        end
    end
    
    LB1 --> C1
    LB1 --> C2
    LB2 --> C2
    LB2 --> C3
    
    C1 --> MKS
    C2 --> MKS
    C3 --> MKS
    
    C1 --> PERMITS
    C2 --> PERMITS
    C3 --> PERMITS
    
    C1 --> API1
    C1 --> OLLAMA1
    C1 --> TORCH1
    
    C2 --> API2
    C2 --> OLLAMA2
    C2 --> TORCH2
    
    C3 --> ECHO
    C3 --> HEALTH
    
    C1 --> SN
    C2 --> SN
    C3 --> SN
    
    SN --> NODE1
    SN --> NODE2
    SN --> NODE3
    
    C1 --> PROM
    C2 --> PROM
    C3 --> PROM
    
    C1 --> LOGS
    C2 --> LOGS
    C3 --> LOGS
    
    PROM --> GRAFANA
    LOGS --> ALERTS
    
    C1 --> ANALYTICS
    C2 --> ANALYTICS
    C3 --> ANALYTICS
    
    ANALYTICS --> REPORTS
```

## Security Architecture

```mermaid
graph TB
    subgraph "Transport Security Layer"
        TLS[TLS 1.3<br/>End-to-End Encryption]
        CERT[Certificate<br/>Management]
        HSTS[HTTP Strict Transport<br/>Security]
    end
    
    subgraph "Authentication Layer"
        subgraph "Multi-Tier Auth"
            MASTER[Master Key<br/>Authentication]
            FILE[File-Based<br/>Keys]
            CONTRACT[Smart Contract<br/>Validation]
        end
        
        subgraph "Key Management"
            HASH[SHA256<br/>Key Hashing]
            CACHE_SEC[Secure<br/>Caching]
            ROTATION[Key<br/>Rotation]
        end
    end
    
    subgraph "Authorization Layer"
        RBAC[Role-Based<br/>Access Control]
        RATE[Rate<br/>Limiting]
        QUOTA[Usage<br/>Quotas]
    end
    
    subgraph "Data Protection Layer"
        subgraph "In Transit"
            ENCRYPT[Request/Response<br/>Encryption]
            BLOCKCHAIN_ENC[Blockchain<br/>Communication]
        end
        
        subgraph "At Rest"
            LOG_SEC[Secure<br/>Logging]
            KEY_STORAGE[Secure Key<br/>Storage]
        end
        
        subgraph "In Memory"
            SECURE_CACHE[Secure<br/>Cache Storage]
            CLEAN_MEM[Memory<br/>Cleanup]
        end
    end
    
    subgraph "Threat Mitigation"
        subgraph "Input Validation"
            HEADER_VAL[Header<br/>Validation]
            BODY_VAL[Body<br/>Validation]
            SIZE_LIMITS[Size<br/>Limits]
        end
        
        subgraph "Attack Prevention"
            REPLAY[Replay<br/>Protection]
            INJECTION[Injection<br/>Prevention]
            DOS[DoS<br/>Protection]
        end
        
        subgraph "Monitoring"
            AUDIT[Audit<br/>Trail]
            ANOMALY[Anomaly<br/>Detection]
            INCIDENT[Incident<br/>Response]
        end
    end
    
    TLS --> CERT
    CERT --> HSTS
    
    HSTS --> MASTER
    MASTER --> FILE
    FILE --> CONTRACT
    
    CONTRACT --> HASH
    HASH --> CACHE_SEC
    CACHE_SEC --> ROTATION
    
    ROTATION --> RBAC
    RBAC --> RATE
    RATE --> QUOTA
    
    QUOTA --> ENCRYPT
    ENCRYPT --> BLOCKCHAIN_ENC
    
    BLOCKCHAIN_ENC --> LOG_SEC
    LOG_SEC --> KEY_STORAGE
    
    KEY_STORAGE --> SECURE_CACHE
    SECURE_CACHE --> CLEAN_MEM
    
    CLEAN_MEM --> HEADER_VAL
    HEADER_VAL --> BODY_VAL
    BODY_VAL --> SIZE_LIMITS
    
    SIZE_LIMITS --> REPLAY
    REPLAY --> INJECTION
    INJECTION --> DOS
    
    DOS --> AUDIT
    AUDIT --> ANOMALY
    ANOMALY --> INCIDENT
```

## Performance Characteristics

### Latency Analysis
- **Master Key Authentication**: <0.1ms
- **File-Based Key Check**: 1-5ms (depends on file size)
- **Cache Hit**: <1ms
- **Smart Contract Query**: 100-500ms (network dependent)
- **Token Counting**: 1-10ms (depends on content size and mode)

### Throughput Capabilities
- **Maximum RPS**: 50,000+ requests per second (cache hits)
- **Sustainable RPS**: 10,000+ requests per second (mixed workload)
- **Contract Query Limit**: ~100 queries per second
- **Optimal Cache Hit Ratio**: 95%+ for production workloads

### Memory Usage
- **Base Memory**: ~50MB per instance
- **Cache Memory**: ~1KB per 1000 cached keys
- **Token Accumulator**: ~100 bytes per active API key
- **Response Buffering**: Configurable (default 5MB max)

### Scaling Characteristics
- **Horizontal Scaling**: Linear scaling with load balancing
- **Vertical Scaling**: CPU-bound for token counting, memory-bound for caching
- **Network Scaling**: Bottlenecked by Secret Network query capacity
- **Storage Scaling**: Minimal storage requirements

## Monitoring and Observability

### Key Metrics Categories

1. **Authentication Metrics**
   - Success/failure rates by authentication tier
   - Cache hit/miss ratios
   - Contract query frequency and latency
   - API key usage patterns

2. **Token Usage Metrics**
   - Input/output token counts per API key
   - Token counting accuracy and performance
   - Usage trends and patterns
   - Cost attribution

3. **System Performance**
   - Request processing latency
   - Memory and CPU utilization
   - Error rates and types
   - Throughput measurements

4. **Security Metrics**
   - Authentication failure patterns
   - Suspicious activity detection
   - Rate limiting effectiveness
   - Security incident tracking

### Logging Strategy
- **Structured Logging**: JSON format for machine processing
- **Log Levels**: DEBUG, INFO, WARN, ERROR with appropriate verbosity
- **Security Logging**: No sensitive data in logs (hashed keys only)
- **Performance Logging**: Request tracing with timing information

### Health Checks
- **Component Health**: Individual component status monitoring
- **Dependency Health**: External service connectivity checks
- **Resource Health**: Memory, CPU, and disk usage monitoring
- **Functional Health**: End-to-end request processing validation

This architecture provides a comprehensive, scalable, and secure foundation for AI/ML API gateway operations with detailed observability and monitoring capabilities.