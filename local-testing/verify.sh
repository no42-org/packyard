#!/usr/bin/env bash
# verify.sh — Walks through the local development instructions from the README
# and reports pass/fail for each step.
#
# Usage:
#   cd <repo-root>
#   bash local-testing/verify.sh
#
# Prerequisites: Docker Compose v2, curl, jq

set -euo pipefail

PASS=0
FAIL=0

pass() { echo "[PASS] $1"; PASS=$((PASS+1)); }
fail() { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }
info() { echo "       $1"; }

COMPOSE="docker compose -f compose.yml -f compose.override.ci.yml"

echo "=== Packyard local dev verification ==="
echo ""

# ── Step 1: .env ──────────────────────────────────────────────────────────────
echo "--- Step 1: .env ---"
if [ -f .env ]; then
  pass ".env exists"
else
  cat > .env <<'EOF'
ACME_EMAIL=dev@localhost
RUSTFS_ACCESS_KEY=dev-access-key
RUSTFS_SECRET_KEY=dev-secret-key-value
EOF
  pass ".env created"
fi

# ── Step 2: Stack startup ─────────────────────────────────────────────────────
echo ""
echo "--- Step 2: Stack startup ---"
$COMPOSE up -d 2>/dev/null
pass "docker compose up -d completed"

# ── Step 3: Traefik readiness ─────────────────────────────────────────────────
echo ""
echo "--- Step 3: Traefik readiness ---"
TRIES=0
while [ $TRIES -lt 30 ]; do
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost/gpg/meridian.asc)
  if echo "$STATUS" | grep -qE "^(200|404)$"; then break; fi
  TRIES=$((TRIES+1)); sleep 1
done
if [ $TRIES -lt 30 ]; then
  pass "Traefik ready (/gpg/meridian.asc probe)"
else
  fail "Traefik did not become ready within 30s"
fi

# ── Step 4: Auth health ───────────────────────────────────────────────────────
echo ""
echo "--- Step 4: Auth health ---"
TRIES=0
while [ $TRIES -lt 30 ]; do
  if curl -sf http://localhost:8080/health > /dev/null 2>&1; then break; fi
  TRIES=$((TRIES+1)); sleep 2
done
if [ $TRIES -lt 30 ]; then
  pass "Auth service healthy"
else
  fail "Auth service did not become healthy within 60s"
  exit 1
fi

# ── Step 5: GPG unauthenticated ───────────────────────────────────────────────
echo ""
echo "--- Step 5: Unauthenticated routes ---"
STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost/gpg/meridian.asc)
if [ "$STATUS" = "200" ]; then
  pass "/gpg/meridian.asc → 200"
else
  fail "/gpg/meridian.asc → $STATUS (expected 200)"
fi

STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://localhost/gpg/cosign.pub)
if [ "$STATUS" = "200" ]; then
  pass "/gpg/cosign.pub → 200"
else
  fail "/gpg/cosign.pub → $STATUS (expected 200)"
fi

# ── Step 6: Create subscription key ──────────────────────────────────────────
echo ""
echo "--- Step 6: Create subscription key ---"
RESPONSE=$(curl -s -X POST http://localhost:8080/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "verify-test"}')
KEY=$(echo "$RESPONSE" | jq -r '.id // empty')
if [ -n "$KEY" ] && [ "$KEY" != "null" ]; then
  pass "Key created: ${KEY:0:16}..."
else
  fail "Key creation failed: $RESPONSE"
  exit 1
fi

# ── Step 7: Authenticated routes ──────────────────────────────────────────────
echo ""
echo "--- Step 7: Authenticated routes ---"

# RPM — valid key
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -u "subscriber:${KEY}" \
  http://localhost/rpm/core/2025/el9-x86_64/)
if [ "$STATUS" = "200" ]; then
  pass "/rpm/core/2025/el9-x86_64/ (valid key) → 200"
else
  fail "/rpm/core/2025/el9-x86_64/ (valid key) → $STATUS (expected 200)"
fi

# RPM — bad key (64 x's)
BAD_KEY=$(printf '%064s' | tr ' ' 'x')
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -u "subscriber:${BAD_KEY}" \
  http://localhost/rpm/core/2025/el9-x86_64/)
if [ "$STATUS" = "401" ]; then
  pass "/rpm/core/2025/el9-x86_64/ (bad key) → 401"
else
  fail "/rpm/core/2025/el9-x86_64/ (bad key) → $STATUS (expected 401)"
fi

# RPM — wrong component scope
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -u "subscriber:${KEY}" \
  http://localhost/rpm/minion/2025/el9-x86_64/)
if [ "$STATUS" = "401" ]; then
  pass "/rpm/minion/2025/el9-x86_64/ (core key, minion scope) → 401"
else
  fail "/rpm/minion/2025/el9-x86_64/ (scope mismatch) → $STATUS (expected 401)"
fi

# DEB — valid key (404 expected: aptly has no published content in local dev)
STATUS=$(curl -s -o /dev/null -w "%{http_code}" -u "subscriber:${KEY}" \
  http://localhost/deb/core/2025/)
if [ "$STATUS" = "200" ] || [ "$STATUS" = "404" ]; then
  pass "/deb/core/2025/ (valid key) → $STATUS (auth passed)"
elif [ "$STATUS" = "401" ]; then
  fail "/deb/core/2025/ (valid key) → 401 (auth rejected valid key)"
else
  info "/deb/core/2025/ → $STATUS"
fi

# ── Step 8: Metrics ───────────────────────────────────────────────────────────
echo ""
echo "--- Step 8: Auth metrics ---"
METRICS=$(curl -s http://localhost:9090/metrics)
if echo "$METRICS" | grep -q "packyard_auth_requests_total"; then
  ALLOWED=$(echo "$METRICS" | grep 'packyard_auth_requests_total{status="allowed"}' | awk '{print $2}')
  DENIED=$(echo "$METRICS" | grep 'packyard_auth_requests_total{status="denied"}' | awk '{print $2}')
  pass "packyard_auth_requests_total — allowed=${ALLOWED} denied=${DENIED}"
else
  fail "packyard_auth_requests_total not found in metrics"
fi

# ── Summary ───────────────────────────────────────────────────────────────────
echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed ==="

# Cleanup key
curl -s -X DELETE "http://localhost:8080/api/v1/keys/${KEY}" > /dev/null
info "Test key revoked"

if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
