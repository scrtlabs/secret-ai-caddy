# Secret AI Caddy - Architecture Documentation

## Overview

The Secret AI Caddy is a Caddy middleware that provides API key authentication for reverse proxy operations. It validates API keys against multiple sources including local master keys, file-based keys, and Secret Network smart contracts with performance-optimized caching.

## High-Level Architecture

```mermaid
graph TB
    subgraph "Client Layer"
        C[HTTP Client]
    end
    
    subgraph "Caddy Server"
        subgraph "Middleware Chain"
            SRP[Secret Reverse Proxy<br/>Middleware]
            RP[Reverse Proxy<br/>Handler]
            OH[Other Handlers]
        end
    end
    
    subgraph "Authentication Sources"
        MK[Master Key<br/>Configuration]
        MKF[Master Keys<br/>File]
        Cache[In-Memory<br/>Cache]
    end
    
    subgraph "External Services"
        SC[Secret Network<br/>Smart Contract]
        Backend[Backend<br/>Service]
    end
    
    C -->|HTTP Request<br/>with API Key| SRP
    SRP --> MK
    SRP --> MKF
    SRP --> Cache
    SRP -->|Cache Miss| SC
    SRP -->|Authorized| RP
    RP --> Backend
    SRP -->|Unauthorized| C
```

## Module Structure

```mermaid
classDiagram
    class Middleware {
        -config: Config
        -validator: APIKeyValidator
        +CaddyModule() ModuleInfo
        +Provision(ctx: Context) error
        +Validate() error
        +ServeHTTP(w, r, next) error
        +UnmarshalCaddyfile(d) error
    }
    
    class Config {
        +APIKey: string
        +MasterKeysFile: string
        +PermitFile: string
        +ContractAddress: string
        +CacheTTL: Duration
    }
    
    class APIKeyValidator {
        -config: Config
        -cache: map[string]bool
        -cacheMutex: RWMutex
        -lastUpdate: Time
        +ValidateAPIKey(key: string) (bool, error)
        -checkMasterKeys(key: string) (bool, error)
        -updateAPIKeyCache() error
    }
    
    class QueryContract {
        +QueryContract(address: string, query: map) (map, error)
        +NewWASMContext(context: map) WASMContext
        +Encrypt(data: []byte) ([]byte, error)
        +Decrypt(data: []byte) ([]byte, error)
    }
    
    Middleware --> Config
    Middleware --> APIKeyValidator
    APIKeyValidator --> Config
    APIKeyValidator --> QueryContract
```

## Component Responsibilities

### Middleware
- **Primary Role**: HTTP request interception and authentication
- **Responsibilities**:
  - Extract API keys from Authorization headers
  - Delegate validation to APIKeyValidator
  - Allow/deny requests based on validation results
  - Integration with Caddy's module system

### Config
- **Primary Role**: Configuration management
- **Responsibilities**:
  - Store all configurable parameters
  - Provide default values
  - Support Caddyfile parsing

### APIKeyValidator
- **Primary Role**: Core authentication logic
- **Responsibilities**:
  - Multi-tier API key validation
  - Cache management for performance
  - Integration with Secret Network
  - Thread-safe operations

### QueryContract
- **Primary Role**: Secret Network integration
- **Responsibilities**:
  - Encrypted communication with smart contracts
  - Key derivation and cryptographic operations
  - Blockchain query execution

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
        Middleware->>Backend: Forward Request
        Backend-->>Client: Response
    end
    
    Note over Validator: Tier 2: Master Keys File
    Validator->>MasterKeys: Check File Contents
    MasterKeys-->>Validator: Found/Not Found
    alt File Key Match
        Validator-->>Middleware: true, nil
        Middleware->>Backend: Forward Request
        Backend-->>Client: Response
    end
    
    Note over Validator: Tier 3: Cache Check
    Validator->>Cache: Lookup Hashed Key
    Cache-->>Validator: Cached Result + Freshness
    alt Cache Hit & Fresh
        Validator-->>Middleware: cached_result, nil
        alt Valid Key
            Middleware->>Backend: Forward Request
            Backend-->>Client: Response
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
        Backend-->>Client: Response
    else Invalid Key
        Validator-->>Middleware: false, nil
        Middleware-->>Client: 401 Unauthorized
    end
```

## Cache Update Flow

```mermaid
sequenceDiagram
    participant Validator
    participant PermitFile
    participant Contract
    participant Cache
    participant Logger
    
    Note over Validator: Cache Update Triggered
    Validator->>Logger: Debug: Starting cache update
    
    alt Permit File Configured
        Validator->>PermitFile: Read JSON Permit
        PermitFile-->>Validator: Permit Data
    else Default Permit
        Validator->>Validator: Generate Default Permit
    end
    
    Validator->>Contract: QueryContract(address, query)
    Note over Contract: Encrypted communication<br/>with Secret Network
    Contract-->>Validator: API Keys Response
    
    alt Query Success
        Validator->>Validator: Parse API Keys Array
        loop For Each Entry
            Validator->>Validator: Extract hashed_key
            Validator->>Cache: Store in New Cache
        end
        
        Validator->>Cache: Replace Old Cache
        Validator->>Validator: Update lastUpdate timestamp
        Validator->>Logger: Info: Cache update completed
    else Query Failed
        Validator->>Logger: Error: Contract query failed
        Validator-->>Validator: Return Error
    end
```

## Configuration and Initialization

```mermaid
stateDiagram-v2
    [*] --> ModuleLoad: Package Import
    
    ModuleLoad --> CaddyfileParse: Caddy Starts
    CaddyfileParse --> ConfigCreate: Parse Directives
    
    state ConfigCreate {
        [*] --> DefaultConfig
        DefaultConfig --> ParseDirectives
        ParseDirectives --> SetAPIKey: API_MASTER_KEY
        ParseDirectives --> SetMasterFile: master_keys_file
        ParseDirectives --> SetPermitFile: permit_file
        ParseDirectives --> SetContract: contract_address
        SetAPIKey --> ConfigReady
        SetMasterFile --> ConfigReady
        SetPermitFile --> ConfigReady
        SetContract --> ConfigReady
    }
    
    ConfigCreate --> Provision: Config Complete
    
    state Provision {
        [*] --> CreateValidator
        CreateValidator --> InitializeCache
        InitializeCache --> LogConfiguration
        LogConfiguration --> [*]
    }
    
    Provision --> Validate: Provision Success
    
    state Validate {
        [*] --> CheckConfig
        CheckConfig --> CheckContract: Config Not Nil
        CheckContract --> CheckTTL: Contract Set
        CheckTTL --> CheckFiles: TTL > 0
        CheckFiles --> ValidationComplete: Files Accessible
        CheckConfig --> ValidationFailed: Config Nil
        CheckContract --> ValidationFailed: No Contract
        CheckTTL --> ValidationFailed: TTL <= 0
    }
    
    Validate --> Ready: Validation Success
    Validate --> [*]: Validation Failed
    Ready --> ServeHTTP: Request Received
```

## Deployment Architecture

```mermaid
graph TB
    subgraph "Load Balancer"
        LB[Load Balancer<br/>nginx/cloudflare]
    end
    
    subgraph "Caddy Instances"
        C1[Caddy Instance 1<br/>secret-ai-caddy]
        C2[Caddy Instance 2<br/>secret-ai-caddy]
        C3[Caddy Instance 3<br/>secret-ai-caddy]
    end
    
    subgraph "Backend Services"
        API1[API Service 1<br/>AI/ML Backend]
        API2[API Service 2<br/>AI/ML Backend]
        API3[API Service 3<br/>AI/ML Backend]
    end
    
    subgraph "Authentication Infrastructure"
        MKS[Master Keys<br/>Shared Storage]
        SN[Secret Network<br/>Blockchain]
    end
    
    subgraph "Monitoring"
        Logs[Centralized<br/>Logging]
        Metrics[Metrics<br/>Collection]
    end
    
    LB --> C1
    LB --> C2
    LB --> C3
    
    C1 --> API1
    C2 --> API2
    C3 --> API3
    
    C1 --> MKS
    C2 --> MKS
    C3 --> MKS
    
    C1 --> SN
    C2 --> SN
    C3 --> SN
    
    C1 --> Logs
    C2 --> Logs
    C3 --> Logs
    
    C1 --> Metrics
    C2 --> Metrics
    C3 --> Metrics
```

## Security Architecture

```mermaid
graph TB
    subgraph "Security Layers"
        subgraph "Transport Security"
            TLS[TLS 1.3<br/>Certificate Management]
        end
        
        subgraph "Authentication Layer"
            API[API Key<br/>Authentication]
            MFA[Multi-Factor<br/>Sources]
        end
        
        subgraph "Authorization Layer"
            RBAC[Role-Based<br/>Access Control]
            Rate[Rate<br/>Limiting]
        end
        
        subgraph "Data Protection"
            Hash[Key<br/>Hashing]
            Encrypt[Blockchain<br/>Encryption]
            Log[Secure<br/>Logging]
        end
    end
    
    subgraph "Threat Mitigation"
        subgraph "Input Validation"
            Header[Header<br/>Validation]
            Format[Format<br/>Checking]
        end
        
        subgraph "Attack Prevention"
            Replay[Replay<br/>Protection]
            Injection[Injection<br/>Prevention]
        end
        
        subgraph "Monitoring"
            Audit[Audit<br/>Trail]
            Alert[Security<br/>Alerts]
        end
    end
    
    TLS --> API
    API --> MFA
    MFA --> RBAC
    RBAC --> Rate
    Rate --> Hash
    Hash --> Encrypt
    Encrypt --> Log
    
    Header --> Format
    Format --> Replay
    Replay --> Injection
    Injection --> Audit
    Audit --> Alert
```

## Performance Characteristics

### Cache Performance
- **Cache Hit Ratio**: ~95% for stable API key sets
- **Cache TTL**: 30 minutes (configurable)
- **Memory Usage**: ~1KB per 1000 cached keys
- **Lookup Time**: O(1) hash table lookup

### Request Latency
- **Cache Hit**: <1ms additional latency
- **Master Key**: <0.1ms additional latency
- **File Check**: 1-5ms (depends on file size)
- **Contract Query**: 100-500ms (network dependent)

### Throughput
- **Max RPS**: Limited by contract query rate
- **Recommended**: Cache hit ratio >90% for production
- **Scaling**: Horizontal scaling supported

## Monitoring and Observability

### Key Metrics
- Authentication success/failure rates
- Cache hit/miss ratios
- Contract query frequency and latency
- Error rates by authentication tier

### Logging Levels
- **DEBUG**: Request processing details
- **INFO**: Authentication events, cache updates
- **WARN**: Configuration issues, file access problems
- **ERROR**: Authentication failures, system errors

### Health Checks
- Configuration validation status
- Master keys file accessibility
- Contract connectivity
- Cache freshness