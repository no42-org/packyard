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

Every image is pinned by tag and digest in `compose.yml`, which Dependabot keeps current.
Versions are deliberately left out of this table so it cannot drift from those pins.

| Service | Image | Role |
|---------|-------|------|
| `traefik` | upstream `traefik` | TLS, routing, forwardAuth middleware |
| `auth` | `ghcr.io/no42-org/packyard-auth`, built from `./auth` | Subscription key validation, admin API, Prometheus metrics |
| `rpm` | `ghcr.io/no42-org/packyard-rpm`, built from `./rpm` | nginx serving signed RPM repos |
| `deb` | upstream `nginx` | nginx serving Aptly-published DEB repos |
| `zot` | upstream `ghcr.io/project-zot/zot-linux-amd64` | OCI registry with keyless cosign signatures |
| `aptly` | `ghcr.io/no42-org/packyard-aptly`, built from `./aptly` | DEB repo management and signing (multi-arch) |
| `rustfs` | upstream `rustfs/rustfs` | S3-compatible staging storage for promotion pipeline |
| `rustfs-init` | upstream `amazon/aws-cli` | One-shot bucket provisioning for RustFS |
| `static` | `ghcr.io/no42-org/packyard-static`, built from `./static` | Public GPG key hosting |
| `backup` | `ghcr.io/no42-org/packyard-backup`, built from `./backup` | Daily SQLite backup of the key store |

The five `packyard-*` images are published by this project.
`auth`, `rpm` and `static` are released by tag alongside Packyard itself; `aptly` and `backup` carry the version of the upstream tool they wrap.
See [RELEASING.md](RELEASING.md).

## Documentation

Full documentation: https://no42-org.github.io/packyard/

## Quick Start

Requires Docker Compose v2, `curl`, `jq`.

`main` tracks the next `-rc` preview, so bring up a release tag rather than the branch tip.

```bash
git clone https://github.com/no42-org/packyard.git
cd packyard
git checkout "$(git describe --tags --abbrev=0)"   # latest release; omit to run the preview
cp .env.example .env        # set ACME_EMAIL, PKG_DOMAIN, ADMIN_DOMAIN and the OAuth provider
docker compose up -d
bash verify.sh
```

Then provision a component and issue a subscriber key through the admin UI at `https://admin.<ADMIN_DOMAIN>/admin/`.
See [Getting Started](https://no42-org.github.io/packyard/getting-started/quick-start) for the full walkthrough and [Operator onboarding](https://no42-org.github.io/packyard/ops/operator-onboarding) for the OAuth setup.

## Local Development

| Task | Command |
|------|---------|
| Auth unit tests | `make test` |
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
static/             Public static files (GPG key)
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
