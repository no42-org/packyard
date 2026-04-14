# Configuration

## Prerequisites

- Docker Compose v2 (`docker compose`)
- A domain pointing to your host with port 443 open (for Let's Encrypt TLS)
- `git` (to clone the repo)

For local development without a public domain, substitute a self-signed cert or use Traefik's local CA.

## Environment Variables

Packyard is configured via a `.env` file at the repository root. This file is never committed.

### Production

```dotenv
# TLS
ACME_EMAIL=ops@example.com
DOMAIN=pkg.example.com

# RustFS staging storage (generate with: openssl rand -hex 20)
RUSTFS_ACCESS_KEY=<generate>
RUSTFS_SECRET_KEY=<generate>
```

### Local Development

```dotenv
ACME_EMAIL=dev@localhost
RUSTFS_ACCESS_KEY=dev-access-key
RUSTFS_SECRET_KEY=dev-secret-key-value
```

## Compose Override Files

| File | Purpose |
|------|---------|
| `compose.yml` | Base configuration |
| `compose.override.ci.yml` | Local development — plain HTTP, admin API exposed on `localhost:8080` |
| `compose.override.arm64.yml` | Apple Silicon — swaps `zot` to the `arm64` image variant |
