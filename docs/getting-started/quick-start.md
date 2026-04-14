# Quick Start

Get a local Packyard stack running and make your first authenticated request in a few minutes.

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

Wait for the auth service to be ready:

```bash
until curl -sf http://localhost:8080/health > /dev/null; do sleep 2; done
echo "Ready"
```

## 3. Create a subscription key

```bash
curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "dev-key"}' | jq .
```

## 4. Make an authenticated request

Use the key `id` from the response above as the HTTP Basic password:

```bash
KEY=<id from step 3>
curl -u subscriber:${KEY} http://localhost/rpm/core/2025/el9-x86_64/repodata/repomd.xml
```

## 5. Run the verification suite

```bash
bash local-testing/verify.sh
```

Expected output: all tests passed, 0 failed.

---

For the full walkthrough including tear-down, arm64 notes, and the automated verification script, see [Local Development](local-development.md).
