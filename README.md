# Secret AI Caddy - Advanced API Gateway

A sophisticated Caddy middleware that provides secure API key authentication, intelligent token usage metering, and comprehensive metrics collection for AI/ML API gateways. The middleware validates API keys against multiple sources while tracking detailed usage statistics and reporting to blockchain-based smart contracts.

## ⭐ What's New in v2.0

**Accurate Token Counting** - We've completely reimplemented token counting to fix the 2-2.5x inflation issue:

- ✅ **Model-Specific Tokenizers** - Uses HuggingFace and SentencePiece tokenizers for accurate counting
- ✅ **90-95% Accuracy** - Matches actual model token usage for billing-grade precision
- ✅ **Automatic Model Detection** - Detects model from request JSON and applies correct tokenizer
- ✅ **Multi-Model Support** - Llama 2/3/3.3, Mistral, Mixtral, Falcon, BERT, and more
- ✅ **Pure Go Implementation** - No CGO or external dependencies required
- ✅ **Smart Fallback** - Gracefully handles unknown models with conservative estimation

**Before:** `(chars/4 + words×1.33)/2` inflated counts by 2-2.5x
**After:** Real tokenization matching actual AI model usage

[See detailed changes in METERING.md](./METERING.md#recent-improvements-v20)

## 🎯 Project Purpose

This middleware implements a comprehensive API gateway solution designed for high-security AI/ML environments requiring:

1. **Multi-tiered Authentication** - Master keys, file-based keys, and Secret Network smart contracts
2. **Accurate Token Metering** - Model-specific token counting for precise billing and usage tracking
3. **Comprehensive Metrics** - Performance monitoring, usage analytics, and operational insights
4. **Blockchain Integration** - Decentralized usage reporting via Secret Network smart contracts
5. **Production-Ready Security** - Encrypted communication, secure caching, and audit logging

## 📚 Documentation

- **[📐 Architecture](./ARCHITECTURE.md)** - Complete system architecture and component design
- **[⚖️ Metering & Metrics](./METERING.md)** - Token counting, usage tracking, and metrics collection

## 🏗️ Architecture Overview

```mermaid
graph TB
    subgraph "Client Layer"
        C[AI/ML Clients<br/>with API Keys]
    end
    
    subgraph "Caddy Gateway"
        subgraph "Middleware Pipeline"
            AUTH[API Key Authentication]
            METER[Token Metering]
            METRICS[Metrics Collection]
            PROXY[Reverse Proxy]
        end
    end
    
    subgraph "Authentication Sources"
        MK[Master Keys]
        MKF[Master Keys File]
        CACHE[Cached Results]
        SC[Secret Network<br/>Smart Contract]
    end
    
    subgraph "AI/ML Services"
        AI1[OpenAI API]
        AI2[Ollama]
        AI3[TorchServe]
        AI4[Custom ML APIs]
    end
    
    subgraph "Reporting & Analytics"
        BLOCKCHAIN[Secret Network<br/>Usage Reporting]
        METRICS_API[Metrics Endpoint<br/>/metrics]
    end
    
    C -->|HTTP + API Key| AUTH
    AUTH --> MK
    AUTH --> MKF  
    AUTH --> CACHE
    AUTH -->|Cache Miss| SC
    AUTH -->|✓ Authorized| METER
    METER -->|Count Tokens| METRICS
    METER --> PROXY
    PROXY --> AI1
    PROXY --> AI2
    PROXY --> AI3
    PROXY --> AI4
    
    METRICS -->|Usage Data| BLOCKCHAIN
    METRICS --> METRICS_API
```

## 🤖 Supported AI Models (v2.0)

The gateway provides accurate token counting for these models using industry-standard tokenizers:

### Fully Supported Models (90-95% Accuracy)

| Model Family | Variants | Tokenizer |
|--------------|----------|-----------|
| **Llama** | Llama 2 (7B, 13B, 70B)<br>Llama 3 (8B, 70B)<br>Llama 3.3 (70B) | HuggingFace |
| **Mistral** | Mistral 7B v0.1/v0.2<br>Mixtral 8x7B | HuggingFace |
| **Falcon** | Falcon 7B, 40B, 180B | HuggingFace |
| **BERT** | BERT base, large | HuggingFace |

### Model Detection

The system automatically detects the model from the `model` field in your JSON request:

```json
{
  "model": "llama3.3:70b",     // Detected and uses Llama-3.3 tokenizer
  "prompt": "Your prompt here"
}
```

**Model name variations handled:**
- `llama3.3:70b` → `llama3.3`
- `mistral-7b-v0.1` → `mistral-7b`
- Case-insensitive matching
- Automatic normalization

### Unknown Models

For models not in the supported list, the system uses a conservative `chars/4` fallback estimation (60-70% accuracy) - no configuration needed.

### Adding Custom Models

To add support for custom HuggingFace models, use the configuration:

```caddyfile
preload_models llama-2,mistral,your-custom-model
```

Any model available on HuggingFace with a `tokenizer.json` file can be used.

## ✨ Key Features

### 🔐 Advanced Authentication
- **Multi-tier validation** with configurable precedence and fallback
- **Secure caching** with SHA256 hashing and configurable TTL
- **Secret Network integration** with encrypted blockchain communication
- **Dynamic key rotation** via file-based keys without service restart
- **Thread-safe operations** with optimized read-write mutex usage

### ⚖️ Accurate Token Metering (v2.0)
- **Model-specific tokenization** using HuggingFace and SentencePiece libraries
- **90-95% accuracy** matching actual AI model token usage for billing-grade precision
- **Automatic model detection** from JSON request body
- **Supported models**: Llama 2/3/3.3, Mistral, Mixtral, Falcon, BERT, and custom HuggingFace models
- **Lazy-loading with caching** - tokenizers load once and are reused for performance
- **Request/response tracking** with comprehensive body analysis
- **Usage accumulation** per API key and per model with thread-safe operations
- **Resilient reporting** with retry logic and failed report persistence
- **Smart fallback** to conservative estimation for unknown models

### 📊 Comprehensive Metrics
- **Real-time monitoring** of requests, tokens, performance, and errors
- **HTTP metrics endpoint** at `/metrics` with detailed JSON output
- **Cache performance tracking** including hit rates and operation times
- **Token usage analytics** with input/output token breakdowns
- **System health indicators** for operational monitoring

### 🚫 URL Filtering & Security
- **Pattern-based blocking** with configurable URL patterns via environment variables
- **Early request filtering** for performance optimization (before API key validation)
- **Comprehensive logging** of blocked requests with pattern matching details
- **HTTP 403 Forbidden responses** for blocked requests with clear error messages
- **Metrics integration** for tracking blocked request statistics

### 🚀 Production Features
- **Environment variable support** for secure configuration management
- **URL filtering** with configurable blocked URL patterns via BLOCK_URLS environment variable
- **Graceful error handling** with detailed logging and audit trails
- **Resource management** with configurable limits and cleanup procedures
- **Docker-ready deployment** with multi-stage builds and health checks

## 🛠️ Building and Testing

### Prerequisites
- Go 1.24+
- Docker & Docker Compose
- Git

### Build Custom Caddy

The project uses a multi-stage Dockerfile to build Caddy with the custom module:

```bash
# Build the custom Caddy image
docker build -t secret-reverse-proxy:latest .
```

The Dockerfile:
1. **Builder Stage**: Uses Go 1.24+ to install xcaddy and build Caddy with the secret-reverse-proxy module
2. **Runtime Stage**: Creates lightweight Alpine-based runtime with security hardening
3. **Security Features**: Non-root user, minimal dependencies, health checks

### Test Environment Setup

#### 1. Start Test Environment
```bash
# Start all services including echo server for testing
docker-compose up --build
```

The docker-compose setup includes:
- **caddy**: Custom Caddy with secret-reverse-proxy module
- **echo-server**: Simple HTTP echo service for testing backend responses
- **networking**: Isolated testnet for secure communication

#### 2. Test Different Scenarios

**Valid API Key Test:**
```bash
curl -H "Authorization: Bearer bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1" \
     -H "Content-Type: application/json" \
     -d '{"model": "llama3.3:70b", "prompt": "Hello, world!", "max_tokens": 100}' \
     http://localhost:8085/
# Expected: 200 OK with accurate token count in logs
```

**Invalid API Key Test:**
```bash
curl -H "Authorization: Bearer invalid-key-123" \
     http://localhost:8085/
# Expected: 401 Unauthorized
```

**Missing Authorization Test:**
```bash
curl http://localhost:8085/
# Expected: 401 Unauthorized
```

**Accurate Token Counting Test (Llama):**
```bash
curl -H "Authorization: Bearer bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1" \
     -H "Content-Type: application/json" \
     -d '{"model": "llama3.3:70b", "prompt": "Write a haiku about programming", "max_tokens": 100}' \
     http://localhost:8085/
# Uses Llama-3.3 tokenizer for accurate counting
```

**Accurate Token Counting Test (Mistral):**
```bash
curl -H "Authorization: Bearer bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1" \
     -H "Content-Type: application/json" \
     -d '{"model": "mistral-7b", "messages": [{"role": "user", "content": "Explain quantum computing"}]}' \
     http://localhost:8085/chat/completions
# Uses Mistral tokenizer for accurate counting
```

**Unknown Model Test (Fallback):**
```bash
curl -H "Authorization: Bearer bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1" \
     -H "Content-Type: application/json" \
     -d '{"model": "custom-gpt-x", "prompt": "Test prompt"}' \
     http://localhost:8085/
# Uses fallback chars/4 estimation for unknown models
```

**Metrics Check:**
```bash
curl http://localhost:8085/metrics
```

**URL Filtering Test (Blocked):**
```bash
# Set environment variable for URL blocking
export BLOCK_URLS="/admin,/config,/internal"

# This request will be blocked with HTTP 403
curl -H "Authorization: Bearer bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1" \
     http://localhost:8085/admin/users
```

### Configuration Details

The `Caddyfile-test` demonstrates comprehensive configuration:

```caddyfile
:80 {
    secret_reverse_proxy {
        # Authentication configuration
        API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
        master_keys_file /etc/caddy/master_keys.txt
        secret_node {env.SECRET_NODE}
        contract_address {env.SECRET_CONTRACT}
        secret_chain_id {env.SECRET_CHAIN_ID}
        permit_file /etc/caddy/permit.json

        # Metering configuration
        metering {env.METERING}
        metering_interval {env.METERING_INTERVAL}
        metering_url {env.METERING_URL}

        # Token counting settings (v2.0)
        max_body_size 2097152          # 2MB max body size
        token_counting_mode accurate   # Uses model-specific tokenizers
        tokenizer_cache_dir /tmp/tokenizers  # Cache directory for tokenizers
        preload_models llama-2,mistral # Pre-cache common models for fast startup

        # Reporting settings
        max_retries 5                  # retry attempts for failed reports
        retry_backoff 300s             # backoff between retries

        # Metrics configuration
        enable_metrics true            # enable /metrics endpoint
        metrics_path /metrics          # metrics endpoint path
    }

    reverse_proxy echo-server:80 {
        health_uri /health
        health_interval 30s
        health_timeout 10s
    }
}
```

### Development Testing

#### Unit Tests
```bash
cd secret-reverse-proxy
go test -v ./...
```

#### Integration Tests  
```bash
go test -v -tags=integration ./...
```

#### Test Coverage
```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

#### Specific Component Tests
```bash
# Test API key validation
go test -v ./validators/

# Test token counting
go test -v -run TestTokenCounter

# Test metering functionality  
go test -v -run TestMetering
```

## 📋 Configuration Reference

### Environment Variables

| Variable | Description | Example |
|----------|-------------|---------|
| `SECRET_API_MASTER_KEY` | Primary API key for authentication | `your-secure-master-key` |
| `SECRET_NODE` | Secret Network LCD endpoint | `lcd.secret.tactus.starshell.net` |
| `SECRET_CHAIN_ID` | Secret Network chain identifier | `secret-4` |
| `SECRET_CONTRACT` | Smart contract address for validation | `secret18xpp2kmkk7g8xzx24wm5zjstw9tjv6g3xle2vjm` |
| `METERING` | Enable/disable usage metering | `1` or `true` |
| `METERING_INTERVAL` | Reporting interval | `5m`, `1h` |
| `METERING_URL` | Endpoint for usage reports | `https://api.example.com` |
| `BLOCK_URLS` | Comma-separated list of URL patterns to block | `/admin,/config,/internal` |

### Caddyfile Directives

| Directive | Type | Description | Default |
|-----------|------|-------------|---------|
| `API_MASTER_KEY` | string | Primary master key | None |
| `master_keys_file` | path | Additional keys file | `""` |
| `permit_file` | path | Secret Network permit file | Uses default |
| `contract_address` | string | Smart contract address | Required |
| `secret_node` | string | Secret Network node | Required |
| `secret_chain_id` | string | Chain ID | Required |
| `metering` | boolean | Enable usage metering | `false` |
| `metering_interval` | duration | Reporting frequency | `10m` |
| `metering_url` | string | Usage reporting endpoint | `""` |
| `max_body_size` | bytes | Max request body size | `10MB` |
| `token_counting_mode` | string | Token counting mode (always uses accurate v2.0) | `accurate` |
| `tokenizer_cache_dir` | path | Directory for caching tokenizer files | `/tmp/tokenizers` |
| `preload_models` | string | Comma-separated models to pre-cache | `llama-2,mistral` |
| `max_retries` | int | Failed report retry attempts | `3` |
| `retry_backoff` | duration | Retry delay | `5m` |
| `enable_metrics` | boolean | Enable metrics collection | `false` |
| `metrics_path` | string | Metrics HTTP endpoint | `/metrics` |

## 🚀 Production Deployment

### Docker Deployment

#### Basic Deployment
```bash
docker run -d \
  --name secret-ai-caddy \
  -p 80:80 -p 443:443 \
  -e SECRET_API_MASTER_KEY="your-production-key" \
  -e SECRET_NODE="lcd.secret.tactus.starshell.net" \
  -e SECRET_CONTRACT="secret18xpp2kmkk7g8xzx24wm5zjstw9tjv6g3xle2vjm" \
  -e SECRET_CHAIN_ID="secret-4" \
  -e METERING=true \
  -e METERING_INTERVAL="5m" \
  -e METERING_URL="https://your-metrics-api.com" \
  -e BLOCK_URLS="/admin,/config,/internal" \
  secret-reverse-proxy:latest
```

#### Production with Volumes
```bash
docker run -d \
  --name secret-ai-caddy \
  --restart unless-stopped \
  -p 80:80 -p 443:443 \
  -v ./Caddyfile:/etc/caddy/Caddyfile \
  -v ./master_keys.txt:/etc/caddy/master_keys.txt \
  -v ./permit.json:/etc/caddy/permit.json \
  -v caddy_data:/data \
  -v caddy_config:/config \
  -e SECRET_API_MASTER_KEY="your-production-key" \
  secret-reverse-proxy:latest
```

### Docker Compose Production

```yaml
version: '3.8'
services:
  secret-ai-caddy:
    image: secret-reverse-proxy:latest
    ports:
      - "80:80"
      - "443:443"
    environment:
      - SECRET_API_MASTER_KEY=${SECRET_API_MASTER_KEY}
      - SECRET_NODE=${SECRET_NODE}
      - SECRET_CONTRACT=${SECRET_CONTRACT}
      - SECRET_CHAIN_ID=${SECRET_CHAIN_ID}
      - METERING=true
      - METERING_INTERVAL=5m
      - METERING_URL=${METERING_URL}
      - BLOCK_URLS=${BLOCK_URLS}
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - ./master_keys.txt:/etc/caddy/master_keys.txt
      - ./permit.json:/etc/caddy/permit.json
      - caddy_data:/data
      - caddy_config:/config
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost/health"]
      interval: 30s
      timeout: 10s
      retries: 3

volumes:
  caddy_data:
  caddy_config:
```

### Security Best Practices

1. **API Key Security**
   - Use environment variables for sensitive keys
   - Rotate master keys regularly  
   - Implement key versioning
   - Monitor key usage patterns

2. **File Security**
   - Secure master keys file with `600` permissions
   - Use separate permit files per environment
   - Regular backup of configuration files

3. **Network Security**
   - Always use HTTPS in production
   - Implement proper firewall rules
   - Use private networks for backend communication
   - Enable rate limiting per API key

4. **Monitoring & Alerting**
   - Monitor authentication failure rates
   - Set up alerts for contract query failures
   - Track unusual usage patterns
   - Monitor system resource usage

5. **Operational Security**
   - Regular security updates
   - Log analysis and monitoring
   - Incident response procedures
   - Backup and recovery plans

## 🔍 Monitoring & Troubleshooting

### Health Checks

**System Health:**
```bash
curl http://localhost:8085/health
```

**Metrics Overview:**
```bash
curl http://localhost:8085/metrics | jq
```

### Common Issues

1. **Authentication Failures**
   ```bash
   # Check logs for details
   docker logs caddy-reverse-proxy
   
   # Verify environment variables
   docker exec caddy-reverse-proxy env | grep SECRET
   ```

2. **Contract Query Issues**
   ```bash
   # Test network connectivity
   curl https://lcd.secret.tactus.starshell.net/status
   
   # Verify contract address
   curl "https://lcd.secret.tactus.starshell.net/compute/v1beta1/code_hash/by_contract_address/YOUR_CONTRACT"
   ```

3. **Token Counting Problems**
   ```bash
   # Check metering logs for tokenizer loading
   docker logs caddy-reverse-proxy 2>&1 | grep -i tokenizer

   # Check for accurate token counting
   docker logs caddy-reverse-proxy 2>&1 | grep -i "Used accurate tokenizer"

   # Test with model-specific request
   curl -H "Authorization: Bearer YOUR_KEY" \
        -H "Content-Type: application/json" \
        -d '{"model": "llama3.3:70b", "prompt": "test"}' \
        http://localhost:8085/

   # Verify tokenizer cache
   docker exec caddy-reverse-proxy ls -la /tmp/tokenizers
   ```

   **Common Token Counting Issues:**
   - If seeing "Failed to load tokenizer" warnings, check network connectivity for HuggingFace downloads
   - Tokenizers are cached after first use - subsequent requests should be fast
   - Unknown models automatically fall back to conservative chars/4 estimation
   - Check `preload_models` configuration to pre-cache commonly used models

4. **URL Filtering Issues**
   ```bash
   # Check if BLOCK_URLS is set
   docker exec caddy-reverse-proxy env | grep BLOCK_URLS
   
   # View filtering logs
   docker logs caddy-reverse-proxy 2>&1 | grep -i "blocked"
   
   # Test blocked URL
   curl -H "Authorization: Bearer YOUR_KEY" \
        http://localhost:8085/admin/test
   # Should return HTTP 403 Forbidden
   
   # Test allowed URL  
   curl -H "Authorization: Bearer YOUR_KEY" \
        http://localhost:8085/api/test
   # Should proceed to API key validation
   ```

### Debug Configuration

```caddyfile
{
    debug
    log {
        output stdout
        format console
        level DEBUG
    }
}
```

## 📊 Performance Characteristics

### v2.0 Performance Metrics

- **Authentication Latency**: <1ms for cache hits, <500ms for contract queries
- **Token Counting (Accurate Mode)**:
  - First request with model: 50-200ms (downloads and caches tokenizer from HuggingFace)
  - Cached tokenizer: 1-5ms per request (accurate tokenization)
  - Unknown model fallback: <1ms (simple chars/4 estimation)
  - Preloaded models: 1-5ms from first request
- **Memory Usage**:
  - ~1KB per 1000 cached API keys
  - ~5-15MB per cached tokenizer (depends on model)
  - Typical deployment: 20-50MB for 2-3 common models
- **Throughput**: Supports 10k+ RPS with proper caching
- **Cache Efficiency**:
  - API keys: 95%+ hit rate for stable key sets
  - Tokenizers: 100% hit rate after initial load (cached permanently)

### Token Counting Accuracy

| Model Type | Accuracy vs Actual | Method |
|------------|-------------------|--------|
| Llama 2/3/3.3 | 90-95% | HuggingFace tokenizer |
| Mistral/Mixtral | 90-95% | HuggingFace tokenizer |
| Falcon | 90-95% | HuggingFace tokenizer |
| BERT | 90-95% | HuggingFace tokenizer |
| Unknown models | 60-70% | Chars/4 fallback |

**Before v2.0:** Heuristic method inflated counts by 2-2.5x
**After v2.0:** Within 5-10% of actual usage for supported models

## 🔄 Migrating from v1.x to v2.0

### What Changed

**Token Counting System:**
- Old heuristic `(chars/4 + words×1.33)/2` replaced with model-specific tokenizers
- Token counts will be **40-60% lower** for most requests (more accurate)
- Per-model usage tracking now available

### Migration Steps

1. **Update Docker Image**
   ```bash
   docker pull secret-reverse-proxy:latest
   # or rebuild: docker build -t secret-reverse-proxy:latest .
   ```

2. **Update Configuration (Optional)**
   ```caddyfile
   secret_reverse_proxy {
       # ... existing config ...

       # New optional settings (v2.0)
       tokenizer_cache_dir /tmp/tokenizers    # Default location
       preload_models llama-2,mistral          # Common models
   }
   ```

3. **Monitor First Deployment**
   ```bash
   # Watch for tokenizer downloads (first time only)
   docker logs -f caddy-reverse-proxy | grep tokenizer

   # Verify accurate counting
   docker logs -f caddy-reverse-proxy | grep "Used accurate tokenizer"
   ```

4. **Expect Lower Token Counts**
   - Old counts were inflated 2-2.5x
   - New counts are 90-95% accurate for supported models
   - Update billing expectations accordingly

### Rollback Plan

If you need to rollback:
```bash
# Use previous image version
docker pull secret-reverse-proxy:v1.x
docker-compose up -d
```

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Implement changes with tests
4. Update documentation
5. Submit a pull request

## 📄 License

[Add appropriate license information]

---

## 🆘 Support

For issues and questions:
- **Documentation**: See [Architecture](./ARCHITECTURE.md) and [Metering](./METERING.md) guides
- **Issues**: GitHub Issues tracker
- **Security**: Contact alexh@scrtlabs.com for security-related issues