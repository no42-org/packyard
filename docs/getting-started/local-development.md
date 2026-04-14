# Local Development

No public domain or TLS certificate required. The CI override swaps Traefik to plain HTTP and exposes the auth admin API directly on the host.

## 1. Configure

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
until curl -sf -o /dev/null http://localhost/gpg/lts.asc; do sleep 1; done
echo "Traefik ready"

# Auth service
until curl -sf http://localhost:8080/health > /dev/null; do sleep 2; done
echo "Auth ready"
```

## 3. Create a subscription key

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

## 5. Tear down

```bash
docker compose -f compose.yml -f compose.override.ci.yml down -v
```

!!! note
    The local stack runs HTTP only — no TLS, no ACME. Port 80 serves public and authenticated routes; the admin API is on `localhost:8080`. Promotion workflows (RPM/DEB/OCI signing) require a running production host with SSH access.

!!! note "Apple Silicon (arm64)"
    `zot` uses an image with the architecture in its name (`zot-linux-amd64`). Use `compose.override.arm64.yml` to swap it to the `arm64` variant. `aptly` is published as a multi-arch image and selects the correct binary automatically.

## Automated verification

A verification script confirms all core routes work as expected:

```bash
bash local-testing/verify.sh
```

This creates a subscription key, exercises authenticated and unauthenticated routes, checks forwardAuth scope enforcement, and verifies Prometheus metrics. Expected output: all tests passed, 0 failed.

The script also supports running against a remote production deployment:

```bash
bash local-testing/verify.sh --base-url https://pkg.example.com --test-key <key>
```
