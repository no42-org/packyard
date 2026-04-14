# Packyard Manual Test Plan

A hands-on walkthrough for learning the stack and verifying all key scenarios.
Run each section in order — later sections depend on state created earlier.

## Prerequisites

Stack running with CI override (HTTP only, no TLS):

```bash
# x86-64
docker compose -f compose.yml -f compose.override.ci.yml up -d

# Apple Silicon (arm64)
docker compose -f compose.yml \
               -f compose.override.ci.yml \
               -f compose.override.arm64.yml \
               up -d
```

Tools needed: `docker`, `curl`, `jq`

Port map in local mode:
- `:80`    — Traefik (all subscriber-facing routes)
- `:8080`  — auth admin API (host-published by ci override)
- `:9090`  — auth Prometheus metrics (host-published by ci override)

---

## 1. Stack Health

**What this teaches:** how to verify all services are running and how health checks work.

```bash
# See all service states
docker compose -f compose.yml -f compose.override.ci.yml ps
```

Expected: all services `running` or `healthy`. `rustfs-init` should show `exited (0)` — it is a one-shot init container.

```bash
# Traefik is ready when this returns 200 (static file served by the `static` service)
curl -v http://localhost/gpg/lts.asc
```

```bash
# Auth service health endpoint
curl -s http://localhost:8080/health
```

Expected: `{"status":"ok"}` or similar. HTTP 200.

---

## 2. Unauthenticated Routes

**What this teaches:** `/gpg/` is served by the `static` nginx container and is deliberately outside Traefik's forwardAuth middleware.

```bash
# Both files are public — no credentials required
curl -o /dev/null -w "%{http_code}\n" http://localhost/gpg/lts.asc
curl -o /dev/null -w "%{http_code}\n" http://localhost/gpg/cosign.pub
```

Expected: `200` for both.

```bash
# Confirm no Authorization header is needed (explicitly provide none)
curl -s http://localhost/gpg/lts.asc | head -3
```

Expected: ASCII-armored GPG key (`-----BEGIN PGP PUBLIC KEY BLOCK-----`).

**Negative:** authenticated routes without credentials return 401.

```bash
curl -o /dev/null -w "%{http_code}\n" http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `401` — Traefik called `/auth`, auth rejected the missing credentials.

---

## 3. Subscription Key Lifecycle

**What this teaches:** the admin API (`:8080`) is the control plane. Keys are 64-char hex IDs used as HTTP Basic passwords.

### 3.1 Create keys

```bash
# core key
CORE_KEY=$(curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "test-core"}' | jq -r '.id')
echo "core key: $CORE_KEY"

# minion key
MINION_KEY=$(curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "minion", "label": "test-minion"}' | jq -r '.id')
echo "minion key: $MINION_KEY"
```

Expected: 64-character hex strings. HTTP 201.

```bash
# Check the full response shape
curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "sentinel", "label": "test-sentinel"}' | jq .
```

Expected fields: `id`, `component`, `label`, `active` (true), `created_at`.

### 3.2 List and filter

```bash
# All keys
curl -s http://localhost:8080/api/v1/keys | jq 'length'

# Filter by component
curl -s "http://localhost:8080/api/v1/keys?component=core" | jq '.[].label'

# Invalid component — should return 400
curl -s -o /dev/null -w "%{http_code}\n" \
  "http://localhost:8080/api/v1/keys?component=invalid"
```

Expected: 400 with `{"code":"INVALID_COMPONENT","message":"..."}`.

### 3.3 Inspect a key

```bash
curl -s "http://localhost:8080/api/v1/keys/${CORE_KEY}" | jq .
```

Expected: full key object including `active: true`.

```bash
# Non-existent key
curl -s -o /dev/null -w "%{http_code}\n" \
  "http://localhost:8080/api/v1/keys/$(printf '%064s' | tr ' ' '0')"
```

Expected: 404 with `{"code":"KEY_NOT_FOUND","message":"..."}`.

### 3.4 Create with expiry

```bash
# Key expires in the past — valid to create, will it be accepted at auth time?
curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "expired-key", "expires_at": "2020-01-01T00:00:00Z"}' | jq .
```

Note the `expires_at` field in the response. Test whether this key passes forwardAuth (expected: 401 — store rejects expired keys).

### 3.5 Invalid component

```bash
curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "unknown", "label": "bad"}' | jq .
```

Expected: HTTP 400, `{"code":"INVALID_COMPONENT","message":"..."}`.

---

## 4. ForwardAuth — Allow / Deny

**What this teaches:** every request to `/rpm/`, `/deb/`, `/oci/` goes through Traefik → `GET /auth` → auth service. The auth service reads the `X-Forwarded-Uri` header to extract the component and compares it against the key's scope.

### 4.1 Valid key — allowed

```bash
# RPM: /rpm/{component}/{year}/{os-arch}/
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `200` — auth allowed, nginx served the directory (empty in local dev).

```bash
# DEB: /deb/{component}/{year}/
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/deb/core/2025/
```

Expected: `200` or `404` — either means auth passed (404 = no published content yet).

### 4.2 Wrong password — denied

```bash
BAD_KEY=$(printf '%064s' | tr ' ' 'x')

curl -u "subscriber:${BAD_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `401`.

### 4.3 Short password — denied immediately

The auth service rejects passwords that are not exactly 64 characters before hitting the database.

```bash
curl -u "subscriber:tooshort" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `401` (no DB lookup happens — length check fails first).

### 4.4 Missing credentials entirely — denied

```bash
curl -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `401`.

---

## 5. Component Scope Enforcement

**What this teaches:** a key scoped to `core` cannot access `minion` or `sentinel` repos, even with a valid key value.

```bash
# core key vs minion path — denied
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/minion/2025/el9-x86_64/

# core key vs sentinel path — denied
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/sentinel/2025/el9-x86_64/

# minion key vs core path — denied
curl -u "subscriber:${MINION_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/

# minion key vs its own path — allowed
curl -u "subscriber:${MINION_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/minion/2025/el9-x86_64/
```

Expected: first three `401`, last one `200`.

### 5.1 OCI component extraction

OCI paths use the format `/oci/v2/lts-{component}/...`. The auth service strips the `lts-` prefix.

```bash
# core key on OCI core repo
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  "http://localhost/oci/v2/lts-core/tags/list"

# core key on OCI minion repo — denied
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  "http://localhost/oci/v2/lts-minion/tags/list"
```

Expected: first `200` or `404` (Zot has no images pushed yet), second `401`.

```bash
# Path without lts- prefix — should be denied (unrecognised format)
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  "http://localhost/oci/v2/someother-repo/tags/list"
```

Expected: `401`.

---

## 6. Key Revocation

**What this teaches:** revocation is immediate — the next forwardAuth call will reject the key. Revocation is idempotent.

```bash
# Confirm key works before revocation
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `200`.

```bash
# Revoke
curl -s -X DELETE "http://localhost:8080/api/v1/keys/${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n"
```

Expected: `204`.

```bash
# Same request immediately after — denied
curl -u "subscriber:${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n" \
  http://localhost/rpm/core/2025/el9-x86_64/
```

Expected: `401`.

```bash
# Revoke again — idempotent 204 (key exists but is already inactive)
curl -s -X DELETE "http://localhost:8080/api/v1/keys/${CORE_KEY}" \
  -o /dev/null -w "%{http_code}\n"
```

Expected: `204` (not 404).

```bash
# Revoke completely non-existent key — 404
curl -s -X DELETE \
  "http://localhost:8080/api/v1/keys/$(printf '%064s' | tr ' ' '0')" \
  -o /dev/null -w "%{http_code}\n"
```

Expected: `404`.

```bash
# Inspect revoked key — still visible in admin API, active=false
curl -s "http://localhost:8080/api/v1/keys/${CORE_KEY}" | jq '{id, active}'
```

Expected: `{"id": "...", "active": false}`.

---

## 7. Prometheus Metrics

**What this teaches:** the auth service exposes counters and histograms that track every forwardAuth decision.

```bash
curl -s http://localhost:9090/metrics | grep packyard_auth
```

Expected lines:

```
packyard_auth_requests_total{status="allowed"} N
packyard_auth_requests_total{status="denied"}  N
packyard_auth_duration_seconds_bucket{...}
packyard_auth_duration_seconds_sum N
packyard_auth_duration_seconds_count N
```

Counters increase with each request:

```bash
# Snapshot current allowed count
BEFORE=$(curl -s http://localhost:9090/metrics \
  | grep 'packyard_auth_requests_total{status="allowed"}' | awk '{print $2}')

# Make one allowed request (use MINION_KEY which is still active)
curl -u "subscriber:${MINION_KEY}" \
  -o /dev/null http://localhost/rpm/minion/2025/el9-x86_64/

# Check counter incremented
AFTER=$(curl -s http://localhost:9090/metrics \
  | grep 'packyard_auth_requests_total{status="allowed"}' | awk '{print $2}')

echo "allowed: before=$BEFORE after=$AFTER"
```

Expected: `after` = `before + 1`.

---

## 8. Log Redaction

**What this teaches:** the auth service must never log credential values (NFR5). The `Authorization` header is redacted; the `ClientUsername` field is dropped.

Make a request that hits the auth path, then inspect the container logs:

```bash
curl -u "subscriber:${MINION_KEY}" \
  http://localhost/rpm/minion/2025/el9-x86_64/ > /dev/null

docker compose -f compose.yml -f compose.override.ci.yml \
  logs --no-log-prefix auth | tail -20
```

Expected: logs contain `key_id` (the ID, not the value), request method and path — but **no** raw `Authorization` header value and **no** subscriber username. The key value itself (`MINION_KEY`) must not appear in any log line.

---

## 9. Backup Verification

**What this teaches:** the `backup` service writes a daily `sqlite3 .backup` to the `auth-backup` volume. The backup is transaction-safe and integrity-checked.

```bash
# Trigger an on-demand backup (exec into the backup container)
docker compose -f compose.yml -f compose.override.ci.yml \
  exec backup /scripts/backup-keystore.sh
```

```bash
# List backup files in the volume
docker compose -f compose.yml -f compose.override.ci.yml \
  exec backup ls -lh /backup/
```

Expected: at least one file named `keystore-YYYYMMDD-HHMMSS.db`.

```bash
# Verify integrity of the latest backup
LATEST=$(docker compose -f compose.yml -f compose.override.ci.yml \
  exec backup ls -t /backup/ | head -1 | tr -d '\r')

docker compose -f compose.yml -f compose.override.ci.yml \
  exec backup sqlite3 /backup/${LATEST} "PRAGMA integrity_check; SELECT count(*) FROM subscription_key;"
```

Expected: `ok` from integrity check, then a row count matching the number of keys you created.

---

## 10. Cleanup

```bash
# Revoke remaining test keys
curl -s -X DELETE "http://localhost:8080/api/v1/keys/${MINION_KEY}" \
  -o /dev/null -w "minion key revoked: %{http_code}\n"

# Confirm no active keys remain
curl -s http://localhost:8080/api/v1/keys | jq '[.[] | select(.active)] | length'
```

Expected: `0`.

```bash
# Tear down everything (add -v to also remove volumes)
docker compose -f compose.yml -f compose.override.ci.yml down
```

---

## What verify.sh Covers vs This Plan

`local-testing/verify.sh` is a fast automated smoke test (12 checks, ~60s). This plan goes deeper:

| Scenario | verify.sh | This plan |
|----------|-----------|-----------|
| Stack startup | yes | yes |
| Traefik readiness | yes | yes |
| Auth health | yes | yes |
| GPG unauthenticated | yes | yes |
| RPM valid key | yes | yes |
| RPM bad key | yes | yes |
| RPM wrong scope | yes | yes |
| DEB valid key | yes | yes |
| Metrics presence | yes | yes |
| Key revocation | revoke only | full lifecycle (idempotent + inspect) |
| OCI scope | no | yes (§5.1) |
| Short password fast-reject | no | yes (§4.3) |
| Invalid component on create | no | yes (§3.5) |
| Expired key | no | yes (§3.4) |
| Scope cross-check (minion↔core) | no | yes (§5) |
| Metric counter increment | no | yes (§7) |
| Log redaction | no | yes (§8) |
| Backup integrity | no | yes (§9) |
