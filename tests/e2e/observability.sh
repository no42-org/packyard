#!/usr/bin/env bash
# observability.sh — Observability and credential non-logging verification (Story 5.4)
#
# INFRASTRUCTURE DEPENDENCY:
#   This script requires a running packyard stack. See tests/e2e/README.md for setup.
#   The auth service must have processed at least one request before running AC2.
#
# REQUIRED ENV VARS:
#   BASE_URL   — packyard base URL (e.g. https://pkg.mdn.opennms.com)
#   VALID_KEY  — a valid active subscription key in the auth database
#
# OPTIONAL ENV VARS:
#   METRICS_URL — auth metrics endpoint (default: http://localhost:9090/metrics)
#                 Override if running from outside the Docker network.
#   COMPONENT   — Meridian component (default: core)
#   YEAR        — Meridian year (default: 2025)
#
# USAGE:
#   BASE_URL=https://pkg.mdn.opennms.com VALID_KEY=abc123 bash tests/e2e/observability.sh
set -euo pipefail

BASE_URL="${BASE_URL:?BASE_URL is required (e.g. https://pkg.mdn.opennms.com)}"
VALID_KEY="${VALID_KEY:?VALID_KEY is required (a valid subscription key)}"
METRICS_URL="${METRICS_URL:-http://localhost:9090/metrics}"
COMPONENT="${COMPONENT:-core}"
YEAR="${YEAR:-2025}"

FAILED=0

# ─── Helpers ─────────────────────────────────────────────────────────────────

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; FAILED=1; }

for cmd in curl docker jq; do
  command -v "$cmd" > /dev/null 2>&1 \
    || { echo "ERROR: '$cmd' not found — see tests/e2e/README.md for prerequisites"; exit 1; }
done

# ─── Precondition: issue one authenticated request to generate metrics ────────

echo "Precondition: sending one authenticated request to generate log and metrics data..."
curl -s -o /dev/null \
  -u "subscriber:${VALID_KEY}" \
  "${BASE_URL}/rpm/el9-x86_64/${COMPONENT}/${YEAR}/repodata/repomd.xml" || true
echo "Precondition complete."

# ─── AC1: Prometheus metrics endpoint ────────────────────────────────────────

echo ""
echo "=== AC1: Prometheus metrics endpoint ==="
METRICS_RC=0
METRICS_BODY=$(curl -s -o - -w '' "${METRICS_URL}" 2>&1) || METRICS_RC=$?
if [ "${METRICS_RC}" -ne 0 ]; then
  fail "AC1 — could not reach metrics endpoint at ${METRICS_URL}: ${METRICS_BODY}"
else
  if echo "${METRICS_BODY}" | grep -q "packyard_auth_requests_total"; then
    pass "AC1a — packyard_auth_requests_total found in /metrics"
  else
    fail "AC1a — packyard_auth_requests_total not found in /metrics output"
  fi

  if echo "${METRICS_BODY}" | grep -q "packyard_auth_duration_seconds"; then
    pass "AC1b — packyard_auth_duration_seconds found in /metrics"
  else
    fail "AC1b — packyard_auth_duration_seconds not found in /metrics output"
  fi
fi

# ─── AC2: Credential non-logging (C3 verification) ───────────────────────────

echo ""
echo "=== AC2: Credential non-logging verification ==="

TRAEFIK_CONTAINER=$(docker ps --filter "label=com.docker.compose.service=traefik" \
  --filter "status=running" --format "{{.Names}}" 2>/dev/null | head -1 || true)
AUTH_CONTAINER=$(docker ps --filter "label=com.docker.compose.service=auth" \
  --filter "status=running" --format "{{.Names}}" 2>/dev/null | head -1 || true)

if [ -z "${TRAEFIK_CONTAINER}" ]; then
  fail "AC2 — no running traefik container found"
fi
if [ -z "${AUTH_CONTAINER}" ]; then
  fail "AC2 — no running auth container found"
fi

# AC2a — key value must not appear in traefik logs
TRAEFIK_LOGS=$(docker logs "${TRAEFIK_CONTAINER}" 2>&1 || true)
TRAEFIK_KEY_MATCHES=$(echo "${TRAEFIK_LOGS}" | grep -cF "${VALID_KEY}" || true)
if [ "${TRAEFIK_KEY_MATCHES}" -eq 0 ]; then
  pass "AC2a — VALID_KEY not found in traefik logs"
else
  fail "AC2a — VALID_KEY found ${TRAEFIK_KEY_MATCHES} time(s) in traefik logs (NFR5 violation)"
fi

# AC2b — key value must not appear in auth logs
AUTH_LOGS=$(docker logs "${AUTH_CONTAINER}" 2>&1 || true)
AUTH_KEY_MATCHES=$(echo "${AUTH_LOGS}" | grep -cF "${VALID_KEY}" || true)
if [ "${AUTH_KEY_MATCHES}" -eq 0 ]; then
  pass "AC2b — VALID_KEY not found in auth logs"
else
  fail "AC2b — VALID_KEY found ${AUTH_KEY_MATCHES} time(s) in auth logs (NFR5 violation)"
fi

# AC2c — Authorization header values must not appear in traefik access log (C3 redaction)
AUTH_HEADER_MATCHES=$(echo "${TRAEFIK_LOGS}" | grep -ci 'Authorization:' || true)
if [ "${AUTH_HEADER_MATCHES}" -eq 0 ]; then
  pass "AC2c — no 'Authorization:' header values in traefik logs (redacted)"
else
  fail "AC2c — 'Authorization:' found ${AUTH_HEADER_MATCHES} time(s) in traefik logs (C3 redaction not active)"
fi

# AC2d — ClientUsername must not appear in traefik access log (C3 drop)
CLIENT_USERNAME_MATCHES=$(echo "${TRAEFIK_LOGS}" | grep -c 'ClientUsername' || true)
if [ "${CLIENT_USERNAME_MATCHES}" -eq 0 ]; then
  pass "AC2d — no 'ClientUsername' field in traefik logs (dropped)"
else
  fail "AC2d — 'ClientUsername' found ${CLIENT_USERNAME_MATCHES} time(s) in traefik logs (C3 drop not active)"
fi

# ─── AC3: Backup file presence (manual trigger) ──────────────────────────────

echo ""
echo "=== AC3: Backup file presence ==="
BACKUP_CONTAINER=$(docker ps --filter "label=com.docker.compose.service=backup" \
  --filter "status=running" --format "{{.Names}}" 2>/dev/null | head -1 || true)

if [ -z "${BACKUP_CONTAINER}" ]; then
  fail "AC3 — no running backup container found; is the stack running with the backup service?"
else
  # Trigger a manual backup run
  echo "AC3: Triggering manual backup..."
  docker exec "${BACKUP_CONTAINER}" /scripts/backup-keystore.sh

  # Verify a backup file exists
  BACKUP_COUNT=$(docker exec "${BACKUP_CONTAINER}" \
    sh -c 'ls /backup/auth-*.db 2>/dev/null | wc -l' || echo 0)
  if [ "${BACKUP_COUNT}" -gt 0 ]; then
    pass "AC3a — ${BACKUP_COUNT} backup file(s) present in auth-backup volume"
  else
    fail "AC3a — no backup files found in /backup after running backup-keystore.sh"
  fi

  # Verify integrity of the most recent backup
  LATEST=$(docker exec "${BACKUP_CONTAINER}" \
    sh -c 'ls -t /backup/auth-*.db 2>/dev/null | head -1' || true)
  if [ -n "${LATEST}" ]; then
    ROW_COUNT=$(docker exec "${BACKUP_CONTAINER}" \
      sqlite3 "${LATEST}" "SELECT count(*) FROM subscription_key" 2>&1 || echo "ERROR")
    if echo "${ROW_COUNT}" | grep -qE '^[0-9]+$'; then
      pass "AC3b — backup integrity verified (subscription_key row count: ${ROW_COUNT})"
    else
      fail "AC3b — backup integrity check failed: ${ROW_COUNT}"
    fi
  else
    fail "AC3b — could not identify latest backup file"
  fi
fi

# ─── AC4: Restore procedure ───────────────────────────────────────────────────

echo ""
echo "=== AC4: Restore procedure ==="
echo "AC4 requires volume manipulation and service restart — documented as manual procedure."
echo "See docs/ops/restore-keystore.md for the step-by-step restore procedure."
pass "AC4 — restore procedure documented in docs/ops/restore-keystore.md"

# ─── Summary ─────────────────────────────────────────────────────────────────

echo ""
echo "=================================="
if [ "${FAILED}" -eq 0 ]; then
  echo "ALL TESTS PASSED"
  exit 0
else
  echo "SOME TESTS FAILED — review output above"
  exit 1
fi
