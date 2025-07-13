# HOWOT

## Build the image
```bash
docker build -t secret-ai-caddy:latest .
```

## Run with default config
```bash
docker run -p 80:80 -p 443:443 -p 21434:21434 secret-ai-caddy:latest
```

## Run with custom config
```bash
docker run -v ./custom-caddyfile:/etc/caddy/Caddyfile -p 21434:21434 secret-ai-caddy:latest
```

## Run with persistent data
```bash
docker run -v caddy_data:/data -v caddy_config:/config -p 21434:21434 secret-ai-caddy:latest
```

