# Packyard

Packyard is a self-hosted, authenticated package distribution platform for Meridian. It serves RPM, DEB, and OCI container packages behind subscription key authentication, with a promotion pipeline that signs and publishes artifacts from CI.

## Architecture

```
Subscriber
    │
    ▼
Traefik (TLS termination, forwardAuth, routing)
    │
    ├── /rpm/   → nginx (RPM repodata + packages)
    ├── /deb/   → nginx → Aptly (signed DEB snapshots)
    ├── /oci/   → Zot (OCI registry, cosign signatures)
    ├── /gpg/   → nginx (public keys — unauthenticated)
    └── /api/   → auth service (admin API, internal :8443)
         │
         └── auth service (forwardAuth + key management)
                  │
                  └── SQLite (subscription key store)

Promotion pipeline (GitHub Actions):
    RustFS (staging) → sign → publish → rpm/deb/zot
```

**Services:**

| Service | Image | Role |
|---------|-------|------|
| `traefik` | `traefik:3.6.12` | TLS, routing, forwardAuth middleware |
| `auth` | built from `./auth` | Subscription key validation, admin API, Prometheus metrics |
| `rpm` | built from `./rpm` | nginx serving signed RPM repos |
| `deb` | `nginx:alpine` | nginx serving Aptly-published DEB repos |
| `zot` | `ghcr.io/project-zot/zot-linux-amd64:v2.1.2` | OCI registry with cosign signatures |
| `aptly` | `ghcr.io/no42-org/packyard-aptly:1.6.2` | DEB repo management and signing (multi-arch) |
| `rustfs` | `rustfs/rustfs:latest` | S3-compatible staging storage for promotion pipeline |
| `static` | `nginx:alpine` | Public GPG/cosign key hosting |
| `backup` | `keinos/sqlite3:latest` | Daily SQLite backup of the key store |

## Documentation

Full documentation: https://no42-org.github.io/packyard/

## Local Development

Requires Docker Compose v2, `curl`, `jq`.

```bash
git clone https://github.com/no42-org/packyard.git
cd packyard
bash local-testing/verify.sh
```

See [Getting Started](https://no42-org.github.io/packyard/latest/getting-started/quick-start/) for the full walkthrough.

## Repository Layout

```
auth/               Go service — subscription key auth + admin API
aptly/              Aptly configuration and DEB repo scripts
deb/                nginx configuration for DEB serving
rpm/                nginx + createrepo_c for RPM serving
zot/                Zot OCI registry configuration
traefik/            Traefik static and dynamic configuration
rustfs/             RustFS staging storage configuration
static/             Public static files (GPG/cosign keys)
scripts/            Operator scripts (backup, stage-artifact, health-check)
docs/ops/           Operational runbooks
tests/e2e/          End-to-end subscriber tests (RPM, DEB, OCI, observability)
tests/load/         k6 load tests for NFR validation
.github/workflows/  Promotion pipeline (RPM, DEB, OCI)
```
