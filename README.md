# Packyard

[![CI](https://github.com/no42-org/packyard/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/no42-org/packyard/actions/workflows/ci.yml)
[![Latest release](https://img.shields.io/github/v/release/no42-org/packyard?sort=semver)](https://github.com/no42-org/packyard/releases/latest)
[![License: GPL-3.0-or-later](https://img.shields.io/badge/license-GPL--3.0--or--later-blue.svg)](LICENSE)

Packyard is a self-hosted, authenticated distribution platform for LTS releases. It serves RPM, DEB, and OCI packages behind subscription key authentication, with a promotion pipeline that signs and publishes artifacts from CI.

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
    └── /gpg/   → nginx (public keys, unauthenticated)
         │
         └── auth service (forwardAuth + key management)
                  │
                  └── SQLite (accounts, keys, components, operators, audit)

Operator (admin.<domain>, OAuth session)
    │
    ▼
Traefik
    ├── /admin/*  → auth service (embedded React admin UI)
    └── /api/v1/* → auth service (admin API)

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

## Quick Start

Requires Docker Compose v2, `curl`, `jq`.

```bash
git clone https://github.com/no42-org/packyard.git
cd packyard
cp .env.example .env        # set ACME_EMAIL, PKG_DOMAIN, ADMIN_DOMAIN and the OAuth provider
docker compose up -d
bash verify.sh
```

Then provision a component and issue a subscriber key through the admin UI at `https://admin.<ADMIN_DOMAIN>/admin/`.
See [Getting Started](https://no42-org.github.io/packyard/getting-started/quick-start) for the full walkthrough and [Operator onboarding](https://no42-org.github.io/packyard/ops/operator-onboarding) for the OAuth setup.

## Local Development

| Task | Command |
|------|---------|
| Auth unit tests | `cd auth && go test ./...` |
| Auth binary with embedded admin UI | `make build` |
| Docs site with live reload | `make docs-serve` |

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full workflow.

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

## Contributing

Contributions are welcome. Read [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow, commit conventions and DCO sign-off.
Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).
Maintainers cut releases as described in [RELEASING.md](RELEASING.md).

## License

Packyard is licensed under the GNU General Public License v3.0 or later. See [LICENSE](LICENSE).
