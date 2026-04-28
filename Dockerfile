# =====================================
# Custom Caddy Dockerfile with Claive API Reverse Proxy Module
# Builds Caddy with custom reverse proxy functionality
# =====================================

# Use Go 1.24+ image as builder to handle all Go version requirements
FROM golang:1.26-alpine AS caddy-builder

# Install build dependencies
RUN apk add --no-cache \
    ca-certificates \
    curl \
    tzdata \
    git \
    && rm -rf /var/cache/apk/*

# Create working directory and copy the local module
WORKDIR /tmp/build
COPY secret-reverse-proxy/ ./secret-reverse-proxy/

# Install xcaddy
RUN go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest

# Build Caddy with secret reverse proxy module using local path
WORKDIR /tmp/build
RUN xcaddy build v2.11.2 \
    --with github.com/scrtlabs/secret-reverse-proxy=./secret-reverse-proxy/

# =====================================
# Final stage - lightweight Alpine-based Caddy runtime
# =====================================
FROM alpine:latest

# Metadata
LABEL maintainer="ScrtLabs"
LABEL description="Custom Caddy server with Secret Reverse Proxy module"
LABEL version="1.0"

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    curl \
    tzdata \
    && rm -rf /var/cache/apk/*

# Create caddy user and group for security
RUN addgroup -g 1000 caddy && \
    adduser -D -s /bin/sh -u 1000 -G caddy caddy

# Create directories for Caddy
RUN mkdir -p /etc/caddy \
             /var/lib/caddy \
             /var/log/caddy \
             /srv && \
    chown -R caddy:caddy /etc/caddy \
                        /var/lib/caddy \
                        /var/log/caddy \
                        /srv

# Copy the custom Caddy binary from the builder stage
COPY --from=caddy-builder /tmp/build/caddy /usr/bin/caddy

# Make sure Caddy is executable
RUN chmod +x /usr/bin/caddy

# Switch to caddy user for security
USER caddy

# Set working directory
WORKDIR /srv

# Environment variables
ENV XDG_CONFIG_HOME=/config
ENV XDG_DATA_HOME=/data

# Expose standard HTTP and HTTPS ports
EXPOSE 80 443

# Expose your custom ports
EXPOSE 21434 22434 23434

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD curl -f http://localhost:21434/health || exit 1

# Volume for persistent data
VOLUME ["/data", "/config"]

# Default command - run Caddy with the config file
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
