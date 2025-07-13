# Claive Reverse Proxy Module

This repository provides a custom Caddy module `claive_reverse_proxy` that validates API keys from the `Authorization` HTTP header before forwarding requests to the backend server via a reverse proxy.

## Features

- Middleware checks for the presence of an `Authorization` header.
- Strips the `Basic ` or `Bearer ` prefix from the `Authorization` header to extract the API key.
- Validates API keys against a cache and updates the cache periodically from a smart contract.
- Handles errors gracefully:
  - Responds with `401 Unauthorized` for invalid API keys.
  - Responds with `500 Internal Server Error` if an issue occurs during the validation process.
- Integrates seamlessly with Caddy's reverse proxy functionality.
- **TLS Support:** TLS is configured for secure communication, and certificates are provided for proper encryption and security.
- **API Master Key:** A master API key can be set to provide an additional level of security, allowing direct access to the backend without additional validation.

## Prerequisites

- Install `xcaddy` to build a custom Caddy binary with the `claive_reverse_proxy` module.
  ```bash
  go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
  ```

## Building Caddy with the Module

To build Caddy with the `claive_reverse_proxy` module:

1. Clone this repository or ensure the module's code is accessible in `./`.
2. Run the following command to build Caddy:
   ```bash
   xcaddy build --with claive-reverse-proxy-module=./
   ```

After a successful build, you can verify that the module is included:

```bash
./caddy list-modules
```

You should see:

```
http.handlers.claive_reverse_proxy

  Non-standard modules: 1
```

## Configuration

### Required Files

Before running Caddy, ensure that the following files are present in the directory where Caddy is executed:

- `Caddyfile` — Configuration file for Caddy.
- `master_keys.txt` — Contains master API keys that grant access to the server without contract verification. The keys should be listed separated by spaces.
- `permit.json` — The permission file signed by the contract administrator, allowing access to the API key query request.

### Example `Caddyfile`

Below is an example `Caddyfile` configuration to use the `claive_reverse_proxy` module:

```caddyfile
{
    debug                         # Enables debug mode, providing detailed logs for troubleshooting.
    order claive_reverse_proxy first  # Ensures the `claive_reverse_proxy` middleware is executed before other handlers.
}

:8080 {
    tls localhost.pem localhost-key.pem  # Specifies the TLS certificates for secure HTTPS connections.
    
    handle {                      # Handles all incoming requests on port 8080.
        claive_reverse_proxy {    # Invokes the custom `claive_reverse_proxy` middleware to validate API keys.
            API_MASTER_KEY DEFAULT_API_MASTER_KEY  # Define the master API key for direct access.
        }
        reverse_proxy {           # Configures a reverse proxy to forward requests to the backend server.
            to https://ai1.myclaive.com:21434   # Specifies the backend server running on localhost with HTTPS on port 21434.
            header_up Host ai1.myclaive.com  # Sets the Host header to `ai1.myclaive.com` for the backend request.
        }
    }
}
```

### Explanation of the Caddyfile

1. **Global Configuration:**
   - `debug`: Enables detailed logging for debugging.
   - `order claive_reverse_proxy first`: Ensures the middleware is executed before other handlers.

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
   - `API_MASTER_KEY DEFAULT_API_MASTER_KEY`: The master API key allows direct access to the backend without the need for further validation if this key is provided in the request.

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

## Notes

- **TLS:** For testing purposes, `tls internal` can be used for self-signed certificates, but it is recommended to use valid certificates in production environments.
- **TLS Certificates:** For secure communication, ensure the TLS certificates (`localhost.pem` and `localhost-key.pem`) are valid. If using self-signed certificates, clients may need to bypass certificate verification (e.g., with the `-k` option in `curl`).
- The cache is set to refresh every 30 minutes, but this can be adjusted in the code by modifying `cacheTTL`.
