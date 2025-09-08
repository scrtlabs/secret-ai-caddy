# Secret Reverse Proxy Module

This repository provides a custom Caddy module `secret_reverse_proxy` that validates API keys from the `Authorization` HTTP header before forwarding requests to the backend server via a reverse proxy. The module also includes advanced token usage metering with per-model tracking capabilities.

## Features

- **API Key Authentication:**
  - Middleware checks for the presence of an `Authorization` header
  - Strips the `Basic ` or `Bearer ` prefix from the `Authorization` header to extract the API key
  - Validates API keys against a cache and updates the cache periodically from a smart contract
  - Supports multiple authentication sources: master keys, file-based keys, and smart contract validation
  
- **Advanced Token Usage Metering:**
  - **Per-Model Token Tracking:** Automatically detects AI model from JSON request bodies and tracks token usage per model
  - **Real-time Usage Collection:** Counts input and output tokens for each request
  - **Resilient Reporting:** Built-in retry mechanism and failure persistence for reliable usage reporting
  - **Enhanced Metrics:** Comprehensive metrics collection including processing times, token counts, and error rates
  
- **Error Handling:**
  - Responds with `401 Unauthorized` for invalid API keys
  - Responds with `500 Internal Server Error` if an issue occurs during the validation process
  - Graceful degradation when external services are unavailable
  
- **Security & Integration:**
  - **TLS Support:** TLS is configured for secure communication
  - **API Master Key:** A master API key can be set to provide direct access without additional validation
  - Integrates seamlessly with Caddy's reverse proxy functionality

## Prerequisites

- Install `xcaddy` to build a custom Caddy binary with the `secret_reverse_proxy` module.
  ```bash
  go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
  ```

## Building Caddy with the Module

To build Caddy with the `secret_reverse_proxy` module:

1. Clone this repository or ensure the module's code is accessible in `./`.
2. Run the following command to build Caddy:
   ```bash
   xcaddy build --with secret-reverse-proxy-module=./
   ```

After a successful build, you can verify that the module is included:

```bash
./caddy list-modules
```

You should see:

```
http.handlers.secret_reverse_proxy

  Non-standard modules: 1
```

## Configuration

### Required Files

Before running Caddy, ensure that the following files are present in the directory where Caddy is executed:

- `Caddyfile` — Configuration file for Caddy.
- `master_keys.txt` — Contains master API keys that grant access to the server without contract verification. The keys should be listed separated by spaces.
- `permit.json` — The permission file signed by the contract administrator, allowing access to the API key query request.

### Example `Caddyfile`

Below is an example `Caddyfile` configuration to use the `secret_reverse_proxy` module:

```caddyfile
{
    debug                         # Enables debug mode, providing detailed logs for troubleshooting.
    order secret_reverse_proxy first  # Ensures the `secret_reverse_proxy` middleware is executed before other handlers.
}

:8080 {
    tls localhost.pem localhost-key.pem  # Specifies the TLS certificates for secure HTTPS connections.
    
    handle {                      # Handles all incoming requests on port 8080.
        secret_reverse_proxy {    # Invokes the custom `secret_reverse_proxy` middleware to validate API keys.
            API_MASTER_KEY {env.DEFAULT_API_MASTER_KEY}  # Define the master API key for direct access.
            master_keys_file {env.MASTER_KEYS_FILE}      # Path to file containing additional master keys
            contract_address {env.CONTRACT_ADDRESS}      # Secret Network contract address for API key validation
            secret_node {env.SECRET_NODE}                # Secret Network node URL
            secret_chain_id {env.SECRET_CHAIN_ID}        # Secret Network chain ID
            
            # Token metering configuration
            metering on                                  # Enable token usage metering
            metering_url {env.METERING_URL}             # URL for reporting token usage
            metering_interval 60s                       # How often to report usage (default: 60s)
            
            # Advanced configuration
            token_counting_mode accurate                 # Token counting mode: heuristic, fast, or accurate
            max_body_size 10MB                          # Maximum request body size to process
            enable_metrics on                           # Enable metrics endpoint
            metrics_path /metrics                       # Metrics endpoint path
        }
        reverse_proxy {           # Configures a reverse proxy to forward requests to the backend server.
            to https://ai1.myclaive.com:21434   # Specifies the backend server running on localhost with HTTPS on port 21434.
            header_up Host ai1.myclaive.com  # Sets the Host header to `ai1.myclaive.com` for the backend request.
        }
    }
}
```

## Configuration Parameters Reference

### Complete Caddyfile Configuration Parameters

The `secret_reverse_proxy` directive supports the following configuration parameters:

#### Authentication Parameters

| Parameter | Type | Required | Description | Example |
|-----------|------|----------|-------------|---------|
| `API_MASTER_KEY` | string | No | Primary master API key that bypasses contract validation | `API_MASTER_KEY {env.SECRET_API_MASTER_KEY}` |
| `master_keys_file` | string | No | Path to file containing additional master keys (one per line) | `master_keys_file /etc/caddy/master_keys.txt` |
| `permit_file` | string | No | Path to JSON permit file for Secret Network contract access | `permit_file /etc/caddy/permit.json` |

#### Secret Network Parameters  

| Parameter | Type | Required | Description | Example |
|-----------|------|----------|-------------|---------|
| `secret_node` | string | Yes | Secret Network LCD endpoint URL | `secret_node {env.SECRET_NODE}` |
| `secret_chain_id` | string | Yes | Secret Network chain ID | `secret_chain_id {env.SECRET_CHAIN_ID}` |
| `contract_address` | string | Yes | Secret Network contract address for API key validation | `contract_address {env.SECRET_CONTRACT}` |

#### Token Metering Parameters

| Parameter | Type | Required | Default | Description | Example |
|-----------|------|----------|---------|-------------|---------|
| `metering` | boolean | No | false | Enable/disable token usage metering | `metering {env.METERING}` or `metering on` |
| `metering_url` | string | No* | - | URL endpoint for reporting token usage (*required if metering enabled) | `metering_url {env.METERING_URL}` |
| `metering_interval` | duration | No | 60s | How frequently to report token usage | `metering_interval {env.METERING_INTERVAL}` |
| `token_counting_mode` | string | No | "heuristic" | Token counting accuracy mode: `heuristic`, `fast`, or `accurate` | `token_counting_mode accurate` |
| `max_body_size` | size | No | 1MB | Maximum request body size to process for token counting | `max_body_size 2097152` or `max_body_size 2MB` |

#### Resilience Parameters

| Parameter | Type | Required | Default | Description | Example |
|-----------|------|----------|---------|-------------|---------|
| `max_retries` | integer | No | 3 | Maximum retry attempts for failed reports | `max_retries 5` |
| `retry_backoff` | duration | No | 5m | Base backoff time between retries (uses exponential backoff) | `retry_backoff 300s` |

#### Metrics Parameters

| Parameter | Type | Required | Default | Description | Example |
|-----------|------|----------|---------|-------------|---------|
| `enable_metrics` | boolean | No | false | Enable Prometheus-style metrics endpoint | `enable_metrics true` |
| `metrics_path` | string | No | "/metrics" | Path for the metrics endpoint | `metrics_path /metrics` |

### Environment Variables

When using Docker or containerized deployments, these environment variables are commonly used:

| Environment Variable | Purpose | Example Value |
|----------------------|---------|---------------|
| `SECRET_NODE` | Secret Network LCD endpoint | `lcd.secret.tactus.starshell.net` |
| `SECRET_CHAIN_ID` | Secret Network chain identifier | `secret-4` |
| `SECRET_CONTRACT` | Contract address for API key validation | `secret18xpp2kmkk7g8xzx24wm5zstw9tjv6g3xle2vjm` |
| `SECRET_API_MASTER_KEY` | Base64-encoded master API key | `bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1` |
| `METERING` | Enable metering (1/0, true/false, on/off) | `1` |
| `METERING_INTERVAL` | Reporting interval | `1m` |
| `METERING_URL` | Metering service endpoint | `https://preview-aidev.scrtlabs.com` |
| `SKIP_SSL_VALIDATION` | Skip SSL validation (development only) | `true` |

### Explanation of the Caddyfile

1. **Global Configuration:**
   - `debug`: Enables detailed logging for debugging
   - `order secret_reverse_proxy first`: Ensures the middleware is executed before other handlers

2. **Site Block (`:8080`):**
   - Specifies that Caddy listens for HTTP requests on port `8080`.

3. **TLS Configuration (`tls localhost.pem localhost-key.pem`):**
   - This directive enables TLS for HTTPS connections and specifies the certificate and private key files (`localhost.pem` and `localhost-key.pem`) for secure communication. These certificates can be generated using tools like OpenSSL or obtained from a Certificate Authority (CA).

4. **`handle` Block:**
   - Processes all incoming requests and applies the `claive_reverse_proxy` middleware for API key validation.
   - Forwards validated requests to a backend server via the `reverse_proxy` directive.

5. **Reverse Proxy Configuration:**
   - `reverse_proxy https://ai1.myclaive.com:21434`: Forwards requests to the backend server.
   - `header_up Host ai1.myclaive.com`: Sets the `Host` header to `ai1.myclaive.com` for the request forwarded to the backend.

6. **API Master Key:**
   - `API_MASTER_KEY {env.SECRET_API_MASTER_KEY}`: The master API key allows direct access to the backend without the need for further validation if this key is provided in the request.

## Configuration Examples

### Example 1: Minimal Configuration (Contract-based Authentication Only)

```caddyfile
{
    order secret_reverse_proxy first
}

:8080 {
    secret_reverse_proxy {
        secret_node https://lcd.secret.tactus.starshell.net
        secret_chain_id secret-4
        contract_address secret18xpp2kmkk7g8xzx24wm5zstw9tjv6g3xle2vjm
        permit_file /etc/caddy/permit.json
    }
    
    reverse_proxy backend-server:3000
}
```

### Example 2: Production Configuration with Full Metering

```caddyfile
{
    debug
    order secret_reverse_proxy first
    log {
        output stdout
        format console
        level INFO
    }
}

api.example.com {
    tls /etc/ssl/certs/fullchain.pem /etc/ssl/private/privkey.pem
    
    secret_reverse_proxy {
        # Authentication sources (multiple layers)
        API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
        master_keys_file /etc/caddy/master_keys.txt
        permit_file /etc/caddy/permit.json
        
        # Secret Network configuration
        secret_node {env.SECRET_NODE}
        secret_chain_id {env.SECRET_CHAIN_ID}
        contract_address {env.SECRET_CONTRACT}
        
        # Token metering with per-model tracking
        metering on
        metering_url {env.METERING_URL}
        metering_interval 30s
        token_counting_mode accurate
        max_body_size 10MB
        
        # Resilience configuration
        max_retries 5
        retry_backoff 300s
        
        # Monitoring
        enable_metrics true
        metrics_path /metrics
    }
    
    reverse_proxy ai-backend:8000 {
        health_uri /health
        health_interval 30s
        health_timeout 10s
    }
}
```

### Example 3: Development Configuration with Docker

```caddyfile
{
    debug
    order secret_reverse_proxy first
}

:80 {
    # CORS configuration for development
    @cors_preflight method OPTIONS
    handle @cors_preflight {
        header {
            Access-Control-Allow-Origin "*"
            Access-Control-Allow-Methods "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD"
            Access-Control-Allow-Headers "*"
            Access-Control-Max-Age "86400"
        }
        respond "" 204
    }

    handle {
        header {
            Access-Control-Allow-Origin "*"
            Access-Control-Allow-Methods "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD"
            Access-Control-Allow-Headers "*"
            Access-Control-Expose-Headers "*"
        }
        
        secret_reverse_proxy {
            # Use environment variables from docker-compose
            API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
            master_keys_file /etc/caddy/master_keys.txt
            secret_node {env.SECRET_NODE}
            contract_address {env.SECRET_CONTRACT}
            secret_chain_id {env.SECRET_CHAIN_ID}
            permit_file /etc/caddy/permit.json
            
            # Development metering settings
            metering {env.METERING}
            metering_interval {env.METERING_INTERVAL}
            metering_url {env.METERING_URL}
            
            # Enhanced configuration for testing
            max_body_size 2097152
            token_counting_mode accurate
            max_retries 5
            retry_backoff 300s
            enable_metrics true
            metrics_path /metrics
        }
        
        reverse_proxy echo-server:80 {
            health_uri /health
            health_interval 30s
            health_timeout 10s
        }
    }
}
```

### Docker Compose Example

Based on the actual configuration files, here's a complete docker-compose.yaml example:

```yaml
version: '3.8'

services:
  echo-server:
    image: ealen/echo-server
    container_name: echo-server
    networks:
      - testnet

  caddy:
    image: secret-reverse-proxy:latest
    container_name: caddy-reverse-proxy
    ports:
      - "8085:80"
    environment:
      - SKIP_SSL_VALIDATION=true
      - SECRET_NODE=lcd.secret.tactus.starshell.net
      - SECRET_CHAIN_ID=secret-4
      - SECRET_CONTRACT=secret18xpp2kmkk7g8xzx24wm5zstw9tjv6g3xle2vjm
      - SECRET_API_MASTER_KEY=bWFzdGVyQHNjcnRsYWJzLmNvbTpTZWNyZXROZXR3b3JrTWFzdGVyS2V5X18yMDI1
      - METERING=1
      - METERING_INTERVAL=1m
      - METERING_URL=https://preview-aidev.scrtlabs.com
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile
      - ./permit.json:/etc/caddy/permit.json
      - ./master_keys.txt:/etc/caddy/master_keys.txt
      - ./config:/config/caddy
      - ./data:/data/caddy
    networks:
      - testnet
    depends_on:
      - echo-server

networks:
  testnet:
    driver: bridge
```

## API Key Validation (`checkApiKey` Logic)

The module validates API keys as follows:

1. **Cache Lookup:**
   - The API key is checked against an in-memory cache.
   - If the API key is found and the cache is fresh (not expired), the validation is successful.

2. **Cache Expiry and Update:**
   - If the cache is stale or the API key is not found, the middleware queries a smart contract to retrieve the latest list of API keys.
   - The cache is updated with the latest API keys from the contract.

3. **Error Handling:**
   - If an error occurs while updating the cache, the module responds with a `500 Internal Server Error`.
   - If the API key is invalid, the module responds with a `401 Unauthorized`.

### Example Behavior

- **Valid API Key:**
  ```bash
  curl -H "Authorization: Basic valid_api_key" http://localhost:8080
  # Request is forwarded to the backend server
  ```

- **Invalid API Key:**
  ```bash
  curl -H "Authorization: Basic invalid_api_key" http://localhost:8080
  HTTP/1.1 401 Unauthorized
  {"error": "Invalid API key"}
  ```

- **Cache Update Failure:**
  ```bash
  curl -H "Authorization: Basic some_api_key" http://localhost:8080
  HTTP/1.1 500 Internal Server Error
  {"error": "Internal server error"}
  ```

## Running the Proxy Server

Once the custom Caddy binary is built and all required files (`Caddyfile`, `master_keys.txt`, `permit.json`) are in place, start the server using the following command in the directory containing these files:

```bash
./caddy run
```

## Token Usage Metering with Per-Model Tracking

The Secret Reverse Proxy module includes advanced token usage metering that tracks token consumption per API key and per AI model. This provides granular visibility into usage patterns for billing and analytics.

### How Model Detection Works

1. **Request Analysis**: The middleware automatically analyzes JSON request bodies for the `"model"` field
2. **Model Extraction**: Extracts model names like `"gpt-4"`, `"gpt-3.5-turbo"`, `"claude-3"`, etc.
3. **Fallback Handling**: Uses `"unknown"` for non-JSON content or requests without a model field

### Token Usage Report Format

The module now reports token usage in an enhanced format that includes both total usage and per-model breakdowns:

```json
{
  "usage_data": {
    "sha256_api_key_hash_1": {
      "input_tokens": 1500,
      "output_tokens": 800,
      "timestamp": 1757348525,
      "model_usage": {
        "gpt-4": {
          "input_tokens": 1000,
          "output_tokens": 500
        },
        "gpt-3.5-turbo": {
          "input_tokens": 500,
          "output_tokens": 300
        }
      }
    },
    "sha256_api_key_hash_2": {
      "input_tokens": 750,
      "output_tokens": 400,
      "timestamp": 1757348525,
      "model_usage": {
        "claude-3": {
          "input_tokens": 750,
          "output_tokens": 400
        }
      }
    }
  }
}
```

### Report Structure Explanation

- **Top Level**: `usage_data` contains all API key usage reports
- **API Key Level**: Each API key is hashed using SHA256 for privacy
  - `input_tokens`: Total input tokens across all models
  - `output_tokens`: Total output tokens across all models  
  - `timestamp`: Unix timestamp of the last update
  - `model_usage`: Per-model breakdown of token usage
- **Model Level**: Individual model usage within `model_usage`
  - `input_tokens`: Input tokens for this specific model
  - `output_tokens`: Output tokens for this specific model

### Benefits of Per-Model Tracking

1. **Granular Billing**: Charge different rates per model (GPT-4 vs GPT-3.5, etc.)
2. **Usage Analytics**: Understand which models are most popular
3. **Cost Optimization**: Identify opportunities to optimize model selection
4. **Compliance**: Detailed audit trails for usage reporting
5. **Capacity Planning**: Better resource allocation based on model-specific usage patterns

### Metering Configuration

Enable token metering by setting these directives in your Caddyfile:

```caddyfile
secret_reverse_proxy {
    metering on                                  # Enable token usage metering
    metering_url https://your-metering-service.com/api/user/report-usage
    metering_interval 60s                       # Report every 60 seconds
    token_counting_mode accurate                 # Use accurate token counting
}
```

### Resilient Reporting

The module includes built-in resilience features:

- **Retry Logic**: Automatically retries failed reports with exponential backoff
- **Failure Persistence**: Stores failed reports to disk for later retry
- **Graceful Degradation**: Continues operating even if metering service is unavailable
- **Memory Management**: Efficiently accumulates usage data without memory leaks

## Notes

- **TLS:** For testing purposes, `tls internal` can be used for self-signed certificates, but it is recommended to use valid certificates in production environments.
- **TLS Certificates:** For secure communication, ensure the TLS certificates (`localhost.pem` and `localhost-key.pem`) are valid. If using self-signed certificates, clients may need to bypass certificate verification (e.g., with the `-k` option in `curl`).
- The cache is set to refresh every 30 minutes, but this can be adjusted in the code by modifying `cacheTTL`.
- **Token Counting**: The module supports different counting modes:
  - `heuristic`: Fast approximate counting
  - `fast`: Balanced speed and accuracy
  - `accurate`: Most precise but slower counting
- **Privacy**: API keys are SHA256 hashed before being included in usage reports to protect sensitive information.
