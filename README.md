# Secret Reverse Proxy

A Caddy middleware that provides secure API key authentication for reverse proxy operations. The middleware validates API keys against multiple sources including configured master keys, local files, and Secret Network smart contracts with intelligent caching for performance.

## Project Purpose

This middleware implements a multi-tiered API key authentication system designed for high-security environments where API access needs to be validated against:

1. **Master Keys** - Primary API keys configured directly in the Caddyfile
2. **Master Keys File** - Additional API keys stored in local files for easier rotation
3. **Secret Network Smart Contract** - Decentralized API key validation with blockchain-based authorization

The middleware integrates seamlessly with Caddy's HTTP handler chain, blocking unauthorized requests while forwarding valid requests to downstream services.

## Technical Architecture

### Authentication Flow
```
Request → Extract API Key → Check Master Key → Check Master Keys File → Check Cache → Query Contract → Allow/Deny
```

### Key Features
- **Multi-source validation** with configurable precedence
- **Intelligent caching** with TTL-based cache invalidation
- **Environment variable support** for secure configuration
- **Thread-safe operations** with proper mutex handling
- **Comprehensive logging** for audit and debugging
- **Graceful error handling** with detailed error reporting

### Components

#### Middleware
- **Config**: Configuration structure with JSON serialization support
- **APIKeyValidator**: Core validation logic with caching
- **ServeHTTP**: Main request processing handler
- **UnmarshalCaddyfile**: Caddyfile configuration parser

#### Security Features
- API key hashing (SHA256) for secure cache storage
- Environment variable expansion for sensitive configuration
- Support for multiple authorization header formats (Basic, Bearer)
- File-based key rotation without service restart

## Configuration

### Caddyfile Directives

```caddyfile
secret_reverse_proxy {
    API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
    master_keys_file "/etc/caddy/master_keys.txt"
    permit_file "/etc/caddy/permit.json"
    contract_address "secret1ttm9axv8hqwjv3qxvxseecppsrw4cd68getrvr"
}
```

### Configuration Options

| Directive | Description | Default |
|-----------|-------------|---------|
| `API_MASTER_KEY` | Primary master key for immediate access | None |
| `master_keys_file` | Path to file containing additional master keys | `master_keys.txt` |
| `permit_file` | Path to JSON file with Secret Network permit | Uses default permit |
| `contract_address` | Secret Network contract address for API key validation | `secret1ttm9axv8hqwjv3qxvxseecppsrw4cd68getrvr` |

### Environment Variables

The middleware supports environment variable expansion using the `{env.VARIABLE_NAME}` syntax:

```caddyfile
secret_reverse_proxy {
    API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
}
```

## Building and Testing

### Build Docker Image

```bash
docker build -t secret-reverse-proxy:latest .
```

### Running Tests

#### Unit Tests
```bash
cd secret-reverse-proxy
go test -v
```

#### Integration Tests
```bash
go test -v -tags=integration
```

#### Test Coverage
```bash
go test -coverprofile=coverage.out
go tool cover -html=coverage.out
```

### Docker Compose Testing

The project includes a complete testing setup with docker-compose:

#### 1. Start the test environment
```bash
docker-compose up --build
```

#### 2. Test with valid API key
```bash
curl -H "Authorization: Bearer your-api-key-here" http://localhost/
```

#### 3. Test with invalid API key
```bash
curl -H "Authorization: Bearer invalid-key" http://localhost/
```

#### 4. Test without authorization header
```bash
curl http://localhost/
```

### Test Configuration

The `Caddyfile-test` provides a complete test configuration:

```caddyfile
{
    debug
    order secret_reverse_proxy first
    log {
        output stdout
        format console
        level DEBUG
    }
}

:80 {
    # CORS configuration for web applications
    @cors_preflight method OPTIONS
    handle @cors_preflight {
        @has_origin header Origin *
        header @has_origin {
            Access-Control-Allow-Origin {header.origin}
            Access-Control-Allow-Credentials "true"
            Access-Control-Allow-Methods "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD"
            Access-Control-Allow-Headers "*"
            Access-Control-Max-Age "86400"
            Vary "Origin"
        }
        respond "" 204
    }

    # Main request handler with authentication
    handle {
        @has_origin header Origin *
        header @has_origin {
            Access-Control-Allow-Origin {header.origin}
            Access-Control-Allow-Credentials "true"
            Access-Control-Allow-Methods "GET, POST, PUT, PATCH, DELETE, OPTIONS, HEAD"
            Access-Control-Allow-Headers "*"
            Access-Control-Expose-Headers "*"
            Vary "Origin"
        }

        # Secret reverse proxy with environment variable configuration
        secret_reverse_proxy {
            API_MASTER_KEY {env.SECRET_API_MASTER_KEY}
        }
        
        # Reverse proxy to echo server for testing
        reverse_proxy echo-server:80 {
            health_uri /health
            health_interval 30s
            health_timeout 10s
            header_up Host {host}
            header_up X-Real-IP {remote_host}
            header_up X-Forwarded-Port {server_port}
        }
    }
}
```

## Development

### Project Structure
```
secret-reverse-proxy/
├── secret_reverse_proxy.go      # Main middleware implementation
├── *_test.go                    # Comprehensive test suite
├── query-contract/              # Secret Network contract query module
├── Dockerfile                   # Docker build configuration
├── docker-compose.yaml          # Test environment setup
├── Caddyfile-test               # Test configuration
└── README.md                    # This documentation
```

### Key Files

- **`secret_reverse_proxy.go`**: Main middleware implementation with all authentication logic
- **`Dockerfile`**: Multi-stage build for optimized production image
- **`docker-compose.yaml`**: Complete test environment with echo server
- **`Caddyfile-test`**: Production-ready configuration example

### Adding New Features

1. Implement changes in `secret_reverse_proxy.go`
2. Add corresponding tests in `*_test.go` files
3. Update configuration parsing in `UnmarshalCaddyfile` if needed
4. Test with the docker-compose environment
5. Update documentation

### Performance Considerations

- **Caching**: 30-minute default TTL balances security and performance
- **Thread Safety**: All cache operations use read-write mutexes
- **Memory Usage**: Bounded cache size based on contract response
- **Network Calls**: Minimized through intelligent caching strategy

## Production Deployment

### Docker Run Examples

#### Basic deployment
```bash
docker run -p 80:80 -p 443:443 \
  -e SECRET_API_MASTER_KEY="your-secure-key" \
  secret-reverse-proxy:latest
```

#### With custom configuration
```bash
docker run -v ./Caddyfile:/etc/caddy/Caddyfile \
  -v ./master_keys.txt:/etc/caddy/master_keys.txt \
  -p 80:80 -p 443:443 \
  secret-reverse-proxy:latest
```

#### With persistent data
```bash
docker run -v caddy_data:/data \
  -v caddy_config:/config \
  -v ./Caddyfile:/etc/caddy/Caddyfile \
  -p 80:80 -p 443:443 \
  secret-reverse-proxy:latest
```

### Security Best Practices

1. **API Key Management**: Use environment variables for sensitive keys
2. **File Permissions**: Secure master keys file with appropriate permissions
3. **HTTPS**: Always use TLS in production environments
4. **Monitoring**: Implement comprehensive logging and monitoring
5. **Rotation**: Regularly rotate master keys and update contract data

## Troubleshooting

### Common Issues

1. **Environment variable not expanded**: Ensure proper `{env.VAR_NAME}` syntax
2. **Master keys file not found**: Check file path and permissions
3. **Contract query failures**: Verify network connectivity and contract address
4. **Cache not updating**: Check TTL configuration and contract responses

### Debug Logging

Enable debug logging in Caddyfile:
```caddyfile
{
    debug
    log {
        level DEBUG
    }
}
```

### Health Checks

The middleware includes comprehensive health checking:
- Master key validation
- File accessibility checks
- Contract connectivity validation
- Cache status monitoring

## License

[Add appropriate license information]

## Contributing

[Add contribution guidelines]