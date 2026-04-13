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
| `aptly` | `urpylka/aptly:1.6.2` | DEB repo management and signing |
| `rustfs` | `rustfs/rustfs:latest` | S3-compatible staging storage for promotion pipeline |
| `static` | `nginx:alpine` | Public GPG/cosign key hosting |
| `backup` | `keinos/sqlite3:latest` | Daily SQLite backup of the key store |

## Prerequisites

- Docker Compose v2 (`docker compose`)
- A domain pointing to your host with port 443 open (for Let's Encrypt TLS)
- `git` (to clone the repo)

For local development without a public domain, substitute a self-signed cert or use Traefik's local CA.

## Local Development

No public domain or TLS certificate required. The CI override swaps Traefik to plain HTTP and exposes the auth admin API directly on the host.

### 1. Configure

```bash
git clone https://github.com/opennms/packyard.git
cd packyard
```

Create a minimal `.env`:

```bash
cat > .env <<'EOF'
ACME_EMAIL=dev@localhost
RUSTFS_ACCESS_KEY=dev-access-key
RUSTFS_SECRET_KEY=dev-secret-key-value
EOF
```

### 2. Start the stack

**x86-64:**
```bash
docker compose -f compose.yml -f compose.override.ci.yml up -d
```

**Apple Silicon (arm64):** add the local override to swap zot to its arm64 image:
```bash
docker compose -f compose.yml \
               -f compose.override.ci.yml \
               -f compose.override.arm64.yml \
               up -d
```

Wait for services to be ready:

```bash
# Traefik (routing)
until curl -sf -o /dev/null http://localhost/gpg/meridian.asc; do sleep 1; done
echo "Traefik ready"

# Auth service
until curl -sf http://localhost:8080/health > /dev/null; do sleep 2; done
echo "Auth ready"
```

### 3. Create a subscription key

The admin API is exposed directly at `localhost:8080` (not through Traefik):

```bash
curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "dev-key"}' | jq .
```

```json
{
  "id": "abc123...",
  "component": "core",
  "label": "dev-key",
  "active": true,
  "created_at": "2025-01-01T00:00:00Z"
}
```

### 4. Make an authenticated request

Use the key `id` as the HTTP Basic password with username `subscriber`:

```bash
KEY=abc123...

# GPG key (unauthenticated)
curl http://localhost/gpg/meridian.asc

# RPM repo metadata (authenticated)
curl -u subscriber:${KEY} http://localhost/rpm/core/2025/el9-x86_64/repodata/repomd.xml

# Check auth metrics
curl -s http://localhost:9090/metrics | grep packyard_auth
```

### 5. Tear down

```bash
docker compose -f compose.yml -f compose.override.ci.yml down -v
```

> **Note:** The local stack runs HTTP only — no TLS, no ACME. Port 80 serves public and authenticated routes; the admin API is on `localhost:8080`. Promotion workflows (RPM/DEB/OCI signing) require a running production host with SSH access.
>
> **Apple Silicon (arm64):** `zot` uses an image with the architecture in its name (`zot-linux-amd64`). Use `compose.override.arm64.yml` to swap it to the `arm64` variant. `aptly` is published as a multi-arch image (`ghcr.io/no42-org/packyard-aptly`) and selects the correct binary automatically.

### Automated verification

A verification script is provided to confirm all core routes work as expected:

```bash
bash local-testing/verify.sh
```

This creates a subscription key, exercises authenticated and unauthenticated routes, checks forwardAuth scope enforcement, and verifies Prometheus metrics. Expected output: 12 passed, 0 failed.

---

## Setup

### 1. Clone and configure

```bash
git clone https://github.com/opennms/packyard.git
cd packyard
cp .env.example .env
```

Edit `.env`:

```bash
# Required: Let's Encrypt contact email and your public domain
ACME_EMAIL=ops@example.com
DOMAIN=pkg.example.com

# Required: RustFS staging storage credentials (choose your own values)
RUSTFS_ACCESS_KEY=your-access-key
RUSTFS_SECRET_KEY=your-secret-key
```

### 2. Add signing keys

**GPG key (RPM + DEB signing):**
```bash
# Export your Meridian signing key and place it in static/gpg/
gpg --armor --export your-signing-key@example.com > static/gpg/meridian.asc
```

**cosign key pair (OCI image signing):**
```bash
cosign generate-key-pair
# cosign.pub → static/gpg/cosign.pub  (committed, served publicly)
# cosign.key → GHA secret COSIGN_PRIVATE_KEY (never committed)
cp cosign.pub static/gpg/cosign.pub
```

> The private GPG key and `cosign.key` must also be stored as GitHub Actions secrets for the promotion pipeline.

### 3. Start the stack

```bash
docker compose up -d
```

Wait for all services to become healthy:
```bash
docker compose ps
```

Traefik will automatically obtain a Let's Encrypt TLS certificate on first start. This requires port 443 to be reachable from the internet.

### 4. Create your first subscription key

Use the admin API (available on the loopback admin entrypoint at port 8443, routed via Traefik):

```bash
curl -X POST https://pkg.example.com/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "first-subscriber"}'
```

The response includes the key `id` — this is the subscription key value subscribers use as their HTTP Basic password. Components are `core`, `minion`, or `sentinel`.

## Subscriber Usage

Subscribers authenticate with HTTP Basic auth: username `subscriber`, password = subscription key.

### RPM (dnf/yum)

```bash
# /etc/yum.repos.d/meridian.repo
[meridian-core]
name=Meridian Core
baseurl=https://subscriber:KEY@pkg.example.com/rpm/core/2025/el9-x86_64/
enabled=1
gpgcheck=1
gpgkey=https://pkg.example.com/gpg/meridian.asc
```

### DEB (apt)

```bash
# Download the GPG key
curl -fsSL https://pkg.example.com/gpg/meridian.asc \
  | gpg --dearmor > /usr/share/keyrings/meridian.gpg

# /etc/apt/sources.list.d/meridian.list
deb [signed-by=/usr/share/keyrings/meridian.gpg] \
  https://subscriber:KEY@pkg.example.com/deb/core/2025/ bookworm main
```

### OCI (Docker / Kubernetes)

```bash
# Authenticate
docker login pkg.example.com/oci \
  --username subscriber \
  --password KEY

# Pull
docker pull pkg.example.com/oci/meridian-core:2025

# Verify signature offline (after downloading cosign.pub once)
curl -fsSL https://pkg.example.com/gpg/cosign.pub -o /etc/meridian/cosign.pub
cosign verify \
  --key /etc/meridian/cosign.pub \
  --insecure-ignore-tlog \
  pkg.example.com/oci/meridian-core:2025
```

## Promotion Pipeline

Packages are promoted from staging to serving via GitHub Actions:

| Workflow | Trigger | What it does |
|----------|---------|--------------|
| `promote-rpm.yml` | `workflow_dispatch` | Downloads RPM from RustFS staging, GPG-signs it, rebuilds repodata, publishes to rpm service |
| `promote-deb.yml` | `workflow_dispatch` | Downloads DEB from RustFS staging, GPG-signs it, creates Aptly snapshot, publishes to deb service |
| `promote-oci.yml` | `workflow_dispatch` | Downloads OCI tarballs, pushes multi-arch image index to Zot, cosign-signs all manifests |

To stage an artifact for promotion:
```bash
RUSTFS_ACCESS_KEY=... RUSTFS_SECRET_KEY=... \
  bash scripts/stage-artifact.sh /path/to/artifact.rpm core rpm 2025 el9-x86_64
```

Then trigger the corresponding promotion workflow with `component`, `year`, and `os` inputs.

## Key Management

| Operation | Command |
|-----------|---------|
| Create key | `POST /api/v1/keys` with `{"component": "core", "label": "name"}` |
| List keys | `GET /api/v1/keys` or `GET /api/v1/keys?component=core` |
| Inspect key | `GET /api/v1/keys/{id}` |
| Revoke key | `DELETE /api/v1/keys/{id}` |

The admin API is available at `https://pkg.example.com/api/v1/` via Traefik's loopback admin entrypoint (`:8443`). It is not reachable from the internet.

## Observability

**Prometheus metrics** are exposed by the auth service at `http://auth:9090/metrics` (Docker-internal only):

- `packyard_auth_requests_total{status="allowed|denied|error"}` — forwardAuth request outcomes
- `packyard_auth_duration_seconds` — forwardAuth latency histogram

Traefik metrics are available at `http://localhost:8443/metrics` (loopback only).

## Backup and Recovery

The `backup` service runs `scripts/backup-keystore.sh` daily, writing timestamped SQLite backups to the `auth-backup` volume. Backups older than 7 days are pruned automatically.

To restore from a backup, see [docs/ops/restore-keystore.md](docs/ops/restore-keystore.md).

## Public Keys

Subscribers can retrieve signing keys without authentication:

| URL | Purpose |
|-----|---------|
| `https://pkg.example.com/gpg/meridian.asc` | GPG public key for RPM/DEB verification |
| `https://pkg.example.com/gpg/cosign.pub` | cosign public key for OCI image verification |

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
