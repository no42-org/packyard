# Getting Started

Get a local Packyard stack running and make your first authenticated request in a few minutes.

No public domain or TLS certificate required. The CI override switches Traefik to plain HTTP and exposes the admin API directly on the host.

**Prerequisites:** Docker Compose v2, `curl`, `jq`.

## 1. Clone and configure

```bash
git clone https://github.com/no42-org/packyard.git
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

## 2. Start the stack

**x86-64:**
```bash
docker compose -f compose.yml -f compose.override.ci.yml up -d
```

**Apple Silicon (arm64):**
```bash
docker compose -f compose.yml \
               -f compose.override.ci.yml \
               -f compose.override.arm64.yml \
               up -d
```

Wait for services to be ready:

```bash
until curl -sf -o /dev/null http://localhost/gpg/lts.asc; do sleep 1; done
echo "Traefik ready"

until curl -sf http://localhost:8080/health > /dev/null; do sleep 2; done
echo "Auth ready"
```

## 3. Provision a component and create a subscription key

The admin API is exposed directly at `localhost:8080`. Provision a component first, then create a key scoped to it:

```bash
# Provision the component (initialises RPM directory tree automatically)
curl -s -X POST http://localhost:8080/api/v1/components \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "core",
    "visibility": "public",
    "rpm_series": ["2025"],
    "rpm_os_families": ["el9"],
    "rpm_architectures": ["x86_64"]
  }' | jq .

# Create a subscription key scoped to the component
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
  "created_at": "2025-01-01T00:00:00Z",
  "component_visibility": "private"
}
```

!!! note
    `component_visibility` is derived from a startup-loaded snapshot and shows `"private"` (the safe default) for any component provisioned after the service started. This field is cosmetic — forward-auth always reads visibility live from the database, so `core` is publicly accessible immediately without a restart.

## 4. Make an authenticated request

Use the key `id` as the HTTP Basic password with username `subscriber`:

```bash
KEY=abc123...

# GPG key (unauthenticated)
curl http://localhost/gpg/lts.asc

# RPM repo metadata (authenticated)
curl -u subscriber:${KEY} http://localhost/rpm/core/2025/el9-x86_64/repodata/repomd.xml

# Check auth metrics
curl -s http://localhost:9090/metrics | grep packyard_auth
```

## 5. Run the verification suite

```bash
bash verify.sh
```

Expected output: all tests passed, 0 failed.

The script also supports running against a remote production deployment:

```bash
bash verify.sh --base-url https://pkg.example.org --test-key <key>
```

## 6. Tear down

```bash
docker compose -f compose.yml -f compose.override.ci.yml down -v
```

---

!!! note
    The local stack runs HTTP only — no TLS, no ACME. Port 80 serves public and authenticated routes; the admin API is on `localhost:8080`. Promotion workflows (RPM/DEB/OCI signing) require a running production host with SSH access.

!!! note "Apple Silicon (arm64)"
    `zot` uses an image with the architecture in its name (`zot-linux-amd64`). Use `compose.override.arm64.yml` to swap it to the `arm64` variant. `aptly` is published as a multi-arch image and selects the correct binary automatically.
