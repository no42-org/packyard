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
ACME_EMAIL=ops@example.org
PKG_DOMAIN=pkg.example.org

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

### Advanced (auth service)

These are set via `compose.yml` and only need overriding when deploying with a non-standard volume layout:

| Variable | Default | Description |
|----------|---------|-------------|
| `RPM_DATA_ROOT` | `/data/rpm` | Root directory for RPM tree initialisation inside the auth container. Must match the mount point of the `rpm-data` volume. The auth service creates trees under `{RPM_DATA_ROOT}/rpm/{component}/{series}/{os_family}-{arch}/` — note the extra `rpm/` subdirectory level. |
| `PACKYARD_PUBLIC_COMPONENT_CACHE_TTL` | `30s` | How long forward-auth caches a component's `public` visibility in memory, so public package traffic is served without a database read and survives store outages shorter than this. Go duration syntax (`30s`, `2m`); `0` disables the cache; values above `1h` are rejected at startup. Only `public` results are cached, so private components always fail closed. This is also the revocation bound: a public-to-private change takes effect immediately on the instance that handled it and within one TTL on any other. |

## Compose Override Files

Two scenarios are supported for non-production work — pick the override that matches what you're doing.

| File | Purpose |
|------|---------|
| `compose.yml` | Base configuration (production: TLS via Let's Encrypt, no host-port forwarding). |
| `compose.override.ci.yml` | **Plain-HTTP fast path** for CI runs and casual local development. No TLS; admin API exposed on `localhost:8080`, Prometheus metrics on `localhost:9090`. Used by `verify.sh`, the e2e tests, and the Getting Started walkthrough. |
| `compose.override.dev.yml` | **TLS-path debugging** override. Keeps the production TLS path with a self-signed fallback (`curl -k` required) and switches routing to PathPrefix-only rules so endpoints are reachable by IP or `localhost` without a real Host header / DNS entry. Use this when you're verifying TLS or Traefik routing behaviour — not for "just run the stack". |
| `compose.override.arm64.yml` | Apple Silicon — swaps `zot` to the `arm64` image variant. Stack on top of either `.ci.yml` or `.dev.yml`. |
