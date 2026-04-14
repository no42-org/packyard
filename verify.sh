#!/usr/bin/env bash
# verify.sh — Smoke-tests a running Packyard stack.
#
# Usage:
#   Local (full suite, starts stack):
#     bash verify.sh
#
#   Local (stack already running):
#     bash verify.sh --skip-stack
#
#   Remote smoke (read-only, no admin API):
#     bash verify.sh \
#       --base-url https://pkg.example.org \
#       --test-key "$KEY" \
#       --test-component core
#
# Prerequisites: Docker Compose v2 (local only), curl, jq

set -uo pipefail

# ── Defaults ──────────────────────────────────────────────────────────────────
BASE_URL="http://localhost"
ADMIN_URL="http://localhost:8080"
METRICS_URL="http://localhost:9090"
SKIP_STACK=false
TEST_KEY=""
TEST_COMPONENT="core"

# ── Argument parsing ──────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --base-url)       BASE_URL="$2";       shift 2 ;;
    --admin-url)      ADMIN_URL="$2";      shift 2 ;;
    --metrics-url)    METRICS_URL="$2";    shift 2 ;;
    --skip-stack)     SKIP_STACK=true;     shift ;;
    --test-key)       TEST_KEY="$2";       shift 2 ;;
    --test-component) TEST_COMPONENT="$2"; shift 2 ;;
    *) echo "Unknown option: $1" >&2; exit 1 ;;
  esac
done

# ── Mode inference ────────────────────────────────────────────────────────────
if [[ "$BASE_URL" == "http://localhost"* ]]; then
  MODE="local"
else
  MODE="remote"
  SKIP_STACK=true
fi

if [[ "$MODE" == "remote" && -z "$TEST_KEY" ]]; then
  echo "error: --test-key is required for remote mode" >&2
  exit 1
fi

# ── Helpers ───────────────────────────────────────────────────────────────────
PASS=0
FAIL=0
CORE_KEY=""
MINION_KEY=""

pass()    { echo "[PASS] $1"; PASS=$((PASS+1)); }
fail()    { echo "[FAIL] $1"; FAIL=$((FAIL+1)); }
info()    { echo "       $1"; }
section() { echo ""; echo "--- $1 ---"; }

# Poll URL until status matches ok_pattern or max_tries is exceeded.
wait_for() {
  local url="$1" ok_pattern="$2" max_tries="$3" interval="$4"
  local tries=0 s
  while [[ $tries -lt $max_tries ]]; do
    s=$(curl -s -o /dev/null -w "%{http_code}" "$url" || true)
    [[ "$s" =~ $ok_pattern ]] && return 0
    tries=$((tries+1)); sleep "$interval"
  done
  return 1
}

# ── Cross-component for scope mismatch tests ──────────────────────────────────
case "$TEST_COMPONENT" in
  core)   CROSS_COMPONENT="minion" ;;
  minion) CROSS_COMPONENT="core" ;;
  *)      CROSS_COMPONENT="core" ;;
esac

COMPOSE="docker compose -f compose.yml -f compose.override.ci.yml"

echo "=== Packyard verification [mode: $MODE] ==="
echo "    base:    $BASE_URL"
if [[ "$MODE" == "local" ]]; then
  echo "    admin:   $ADMIN_URL"
  echo "    metrics: $METRICS_URL"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Prerequisites
# ─────────────────────────────────────────────────────────────────────────────
section "Prerequisites"
for cmd in curl jq; do
  if ! command -v "$cmd" &>/dev/null; then
    echo "error: $cmd is required" >&2; exit 1
  fi
done
if [[ "$MODE" == "local" ]] && ! docker compose version &>/dev/null 2>&1; then
  echo "error: Docker Compose v2 is required" >&2; exit 1
fi
pass "prerequisites satisfied"

# ─────────────────────────────────────────────────────────────────────────────
# .env  (local only)
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" ]]; then
  section ".env"
  if [[ -f .env ]]; then
    pass ".env exists"
  else
    cat > .env <<'EOF'
ACME_EMAIL=dev@localhost
RUSTFS_ACCESS_KEY=dev-access-key
RUSTFS_SECRET_KEY=dev-secret-key-value
EOF
    pass ".env created"
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# Stack startup  (local only, skippable)
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" && "$SKIP_STACK" == false ]]; then
  section "Stack startup"
  $COMPOSE up -d 2>/dev/null
  pass "docker compose up -d completed"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Traefik readiness  (local only)
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" ]]; then
  section "Traefik readiness"
  if wait_for "$BASE_URL/gpg/lts.asc" "^(200|404)$" 30 1; then
    pass "Traefik ready (/gpg/lts.asc probe)"
  else
    fail "Traefik did not become ready within 30s"
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# Auth health  (local only)
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" ]]; then
  section "Auth health"
  if wait_for "$ADMIN_URL/health" "^200$" 30 2; then
    pass "Auth service healthy"
  else
    fail "Auth service did not become healthy within 60s"
    echo "Cannot continue: auth service unreachable" >&2
    exit 1
  fi
fi

# ─────────────────────────────────────────────────────────────────────────────
# Key setup
#   Local:  create core + minion keys via admin API
#   Remote: use the caller-supplied --test-key
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" ]]; then
  section "Key creation (admin API)"

  RESPONSE=$(curl -s -X POST "$ADMIN_URL/api/v1/keys" \
    -H 'Content-Type: application/json' \
    -d '{"component": "core", "label": "verify-core"}' || true)
  CORE_KEY=$(echo "$RESPONSE" | jq -r '.id // empty')
  if [[ -n "$CORE_KEY" ]]; then
    pass "core key created: ${CORE_KEY:0:16}..."
  else
    fail "core key creation failed: $RESPONSE"
    echo "Cannot continue: key creation failed" >&2
    exit 1
  fi

  RESPONSE=$(curl -s -X POST "$ADMIN_URL/api/v1/keys" \
    -H 'Content-Type: application/json' \
    -d '{"component": "minion", "label": "verify-minion"}' || true)
  MINION_KEY=$(echo "$RESPONSE" | jq -r '.id // empty')
  if [[ -n "$MINION_KEY" ]]; then
    pass "minion key created: ${MINION_KEY:0:16}..."
  else
    fail "minion key creation failed: $RESPONSE"
    MINION_KEY=""
  fi

  TEST_KEY="$CORE_KEY"
  TEST_COMPONENT="core"
  CROSS_COMPONENT="minion"
else
  CORE_KEY="$TEST_KEY"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Unauthenticated routes  (shared)
# ─────────────────────────────────────────────────────────────────────────────
section "Unauthenticated routes"

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/gpg/lts.asc" || true)
[[ "$STATUS" == "200" ]] \
  && pass "/gpg/lts.asc → 200" \
  || fail "/gpg/lts.asc → $STATUS (expected 200)"

STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/gpg/cosign.pub" || true)
[[ "$STATUS" == "200" ]] \
  && pass "/gpg/cosign.pub → 200" \
  || fail "/gpg/cosign.pub → $STATUS (expected 200)"

# Negative: authenticated route without credentials → 401
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  "$BASE_URL/rpm/$TEST_COMPONENT/2025/el9-x86_64/" || true)
[[ "$STATUS" == "401" ]] \
  && pass "/rpm/$TEST_COMPONENT/ (no creds) → 401" \
  || fail "/rpm/$TEST_COMPONENT/ (no creds) → $STATUS (expected 401)"

# ─────────────────────────────────────────────────────────────────────────────
# ForwardAuth allow / deny  (shared)
# ─────────────────────────────────────────────────────────────────────────────
section "ForwardAuth allow/deny"

# Short password — fast-reject, no DB lookup
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:tooshort" "$BASE_URL/rpm/$TEST_COMPONENT/2025/el9-x86_64/" || true)
[[ "$STATUS" == "401" ]] \
  && pass "Short password fast-reject → 401" \
  || fail "Short password fast-reject → $STATUS (expected 401)"

# Bad key (64 x's)
BAD_KEY=$(printf '%064s' "" | tr ' ' 'x')
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${BAD_KEY}" "$BASE_URL/rpm/$TEST_COMPONENT/2025/el9-x86_64/" || true)
[[ "$STATUS" == "401" ]] \
  && pass "Bad key (64 x's) → 401" \
  || fail "Bad key (64 x's) → $STATUS (expected 401)"

# Valid key — RPM
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${TEST_KEY}" "$BASE_URL/rpm/$TEST_COMPONENT/2025/el9-x86_64/" || true)
[[ "$STATUS" == "200" ]] \
  && pass "/rpm/$TEST_COMPONENT/ (valid key) → 200" \
  || fail "/rpm/$TEST_COMPONENT/ (valid key) → $STATUS (expected 200)"

# Valid key — DEB (404 expected if no packages published)
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${TEST_KEY}" "$BASE_URL/deb/$TEST_COMPONENT/2025/" || true)
if [[ "$STATUS" == "200" || "$STATUS" == "404" ]]; then
  pass "/deb/$TEST_COMPONENT/ (valid key) → $STATUS (auth passed)"
elif [[ "$STATUS" == "401" ]]; then
  fail "/deb/$TEST_COMPONENT/ (valid key) → 401 (auth rejected valid key)"
else
  info "/deb/$TEST_COMPONENT/ → $STATUS"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Scope enforcement  (shared)
# ─────────────────────────────────────────────────────────────────────────────
section "Scope enforcement"

# TEST_KEY on cross-component path → 401
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${TEST_KEY}" "$BASE_URL/rpm/$CROSS_COMPONENT/2025/el9-x86_64/" || true)
[[ "$STATUS" == "401" ]] \
  && pass "/rpm/$CROSS_COMPONENT/ ($TEST_COMPONENT key, wrong scope) → 401" \
  || fail "/rpm/$CROSS_COMPONENT/ ($TEST_COMPONENT key, wrong scope) → $STATUS (expected 401)"

# Full matrix — local only, requires both CORE_KEY and MINION_KEY
if [[ "$MODE" == "local" && -n "$MINION_KEY" ]]; then
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -u "subscriber:${CORE_KEY}" "$BASE_URL/rpm/sentinel/2025/el9-x86_64/" || true)
  [[ "$STATUS" == "401" ]] \
    && pass "/rpm/sentinel/ (core key) → 401" \
    || fail "/rpm/sentinel/ (core key) → $STATUS (expected 401)"

  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -u "subscriber:${MINION_KEY}" "$BASE_URL/rpm/core/2025/el9-x86_64/" || true)
  [[ "$STATUS" == "401" ]] \
    && pass "/rpm/core/ (minion key, wrong scope) → 401" \
    || fail "/rpm/core/ (minion key, wrong scope) → $STATUS (expected 401)"

  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -u "subscriber:${MINION_KEY}" "$BASE_URL/rpm/minion/2025/el9-x86_64/" || true)
  [[ "$STATUS" == "200" ]] \
    && pass "/rpm/minion/ (minion key, own scope) → 200" \
    || fail "/rpm/minion/ (minion key, own scope) → $STATUS (expected 200)"
fi

# ─────────────────────────────────────────────────────────────────────────────
# OCI scope  (shared)
# ─────────────────────────────────────────────────────────────────────────────
section "OCI scope"

# Valid: own lts-COMPONENT repo
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${TEST_KEY}" "$BASE_URL/oci/v2/lts-$TEST_COMPONENT/tags/list" || true)
if [[ "$STATUS" == "200" || "$STATUS" == "404" ]]; then
  pass "/oci/v2/lts-$TEST_COMPONENT/ (valid scope) → $STATUS (auth passed)"
elif [[ "$STATUS" == "401" ]]; then
  fail "/oci/v2/lts-$TEST_COMPONENT/ (valid scope) → 401 (auth rejected valid key)"
else
  info "/oci/v2/lts-$TEST_COMPONENT/ → $STATUS"
fi

# Wrong scope: cross-component OCI repo → 401
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${TEST_KEY}" "$BASE_URL/oci/v2/lts-$CROSS_COMPONENT/tags/list" || true)
[[ "$STATUS" == "401" ]] \
  && pass "/oci/v2/lts-$CROSS_COMPONENT/ ($TEST_COMPONENT key, wrong scope) → 401" \
  || fail "/oci/v2/lts-$CROSS_COMPONENT/ ($TEST_COMPONENT key, wrong scope) → $STATUS (expected 401)"

# Unrecognised OCI path format → 401
STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
  -u "subscriber:${TEST_KEY}" "$BASE_URL/oci/v2/someother-repo/tags/list" || true)
[[ "$STATUS" == "401" ]] \
  && pass "/oci/v2/someother-repo/ (no lts- prefix) → 401" \
  || fail "/oci/v2/someother-repo/ (no lts- prefix) → $STATUS (expected 401)"

# ─────────────────────────────────────────────────────────────────────────────
# Local-only tests
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" ]]; then

  ZERO_KEY=$(printf '%064s' "" | tr ' ' '0')

  # ── Key lifecycle ────────────────────────────────────────────────────────
  section "Key lifecycle (admin API)"

  # List all keys — expect at least 2
  COUNT=$(curl -s "$ADMIN_URL/api/v1/keys" | jq 'length' || echo 0)
  (( COUNT >= 2 )) \
    && pass "Key list → $COUNT keys (≥2 expected)" \
    || fail "Key list → $COUNT keys (expected ≥2)"

  # Filter by component — all results must match
  MISMATCHES=$(curl -s "$ADMIN_URL/api/v1/keys?component=core" \
    | jq '[.[] | select(.component != "core")] | length' || echo 1)
  [[ "$MISMATCHES" == "0" ]] \
    && pass "Key filter ?component=core → all results are core" \
    || fail "Key filter ?component=core → $MISMATCHES non-core result(s) returned"

  # Invalid component filter → 400
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    "$ADMIN_URL/api/v1/keys?component=invalid" || true)
  [[ "$STATUS" == "400" ]] \
    && pass "Key filter ?component=invalid → 400" \
    || fail "Key filter ?component=invalid → $STATUS (expected 400)"

  # Non-existent key GET → 404
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    "$ADMIN_URL/api/v1/keys/${ZERO_KEY}" || true)
  [[ "$STATUS" == "404" ]] \
    && pass "Non-existent key GET → 404" \
    || fail "Non-existent key GET → $STATUS (expected 404)"

  # Invalid component on create → 400
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -X POST "$ADMIN_URL/api/v1/keys" \
    -H 'Content-Type: application/json' \
    -d '{"component": "unknown", "label": "bad"}' || true)
  [[ "$STATUS" == "400" ]] \
    && pass "Create key with invalid component → 400" \
    || fail "Create key with invalid component → $STATUS (expected 400)"

  # Expired key → 401 at auth time
  RESPONSE=$(curl -s -X POST "$ADMIN_URL/api/v1/keys" \
    -H 'Content-Type: application/json' \
    -d '{"component": "core", "label": "verify-expired", "expires_at": "2020-01-01T00:00:00Z"}' || true)
  EXPIRED_KEY=$(echo "$RESPONSE" | jq -r '.id // empty')
  if [[ -n "$EXPIRED_KEY" && "$EXPIRED_KEY" != "null" ]]; then
    STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
      -u "subscriber:${EXPIRED_KEY}" "$BASE_URL/rpm/core/2025/el9-x86_64/" || true)
    [[ "$STATUS" == "401" ]] \
      && pass "Expired key → 401 (auth rejected)" \
      || fail "Expired key → $STATUS (expected 401)"
    curl -s -X DELETE "$ADMIN_URL/api/v1/keys/${EXPIRED_KEY}" > /dev/null || true
  else
    fail "Could not create expired key for test: $RESPONSE"
  fi

  # ── Key revocation ───────────────────────────────────────────────────────
  section "Key revocation"

  # Confirm works before revocation
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -u "subscriber:${CORE_KEY}" "$BASE_URL/rpm/core/2025/el9-x86_64/" || true)
  [[ "$STATUS" == "200" ]] \
    && pass "Pre-revocation: core key → 200" \
    || fail "Pre-revocation: core key → $STATUS (expected 200)"

  # Revoke
  STATUS=$(curl -s -X DELETE "$ADMIN_URL/api/v1/keys/${CORE_KEY}" \
    -o /dev/null -w "%{http_code}" || true)
  [[ "$STATUS" == "204" ]] \
    && pass "Revoke core key → 204" \
    || fail "Revoke core key → $STATUS (expected 204)"

  # Immediately denied
  STATUS=$(curl -s -o /dev/null -w "%{http_code}" \
    -u "subscriber:${CORE_KEY}" "$BASE_URL/rpm/core/2025/el9-x86_64/" || true)
  [[ "$STATUS" == "401" ]] \
    && pass "Post-revocation: core key → 401" \
    || fail "Post-revocation: core key → $STATUS (expected 401)"

  # Idempotent — revoke again → still 204
  STATUS=$(curl -s -X DELETE "$ADMIN_URL/api/v1/keys/${CORE_KEY}" \
    -o /dev/null -w "%{http_code}" || true)
  [[ "$STATUS" == "204" ]] \
    && pass "Revoke already-revoked key → 204 (idempotent)" \
    || fail "Revoke already-revoked key → $STATUS (expected 204)"

  # Non-existent key → 404
  STATUS=$(curl -s -X DELETE "$ADMIN_URL/api/v1/keys/${ZERO_KEY}" \
    -o /dev/null -w "%{http_code}" || true)
  [[ "$STATUS" == "404" ]] \
    && pass "Revoke non-existent key → 404" \
    || fail "Revoke non-existent key → $STATUS (expected 404)"

  # Inspect revoked key — active=false
  ACTIVE=$(curl -s "$ADMIN_URL/api/v1/keys/${CORE_KEY}" \
    | jq -r '.active // "error"' || echo "error")
  [[ "$ACTIVE" == "false" ]] \
    && pass "Revoked key inspect → active=false" \
    || fail "Revoked key inspect → active=$ACTIVE (expected false)"

  # ── Metrics ──────────────────────────────────────────────────────────────
  section "Metrics"

  METRICS=$(curl -s "$METRICS_URL/metrics" || true)
  if echo "$METRICS" | grep -q "packyard_auth_requests_total"; then
    ALLOWED=$(echo "$METRICS" | grep 'packyard_auth_requests_total{status="allowed"}' | awk '{print $2}' || true)
    DENIED=$(echo  "$METRICS" | grep 'packyard_auth_requests_total{status="denied"}'  | awk '{print $2}' || true)
    pass "packyard_auth_requests_total present — allowed=${ALLOWED:-?} denied=${DENIED:-?}"
  else
    fail "packyard_auth_requests_total not found in metrics"
    ALLOWED=""
  fi

  # Counter increment: snapshot before/after one allowed request
  if [[ -n "${ALLOWED:-}" && -n "$MINION_KEY" ]]; then
    BEFORE="$ALLOWED"
    curl -s -u "subscriber:${MINION_KEY}" \
      -o /dev/null "$BASE_URL/rpm/minion/2025/el9-x86_64/" || true
    AFTER=$(curl -s "$METRICS_URL/metrics" \
      | grep 'packyard_auth_requests_total{status="allowed"}' | awk '{print $2}' || true)
    if [[ -n "${AFTER:-}" ]] && awk "BEGIN { exit !($AFTER > $BEFORE) }"; then
      pass "Metric counter incremented: allowed $BEFORE → $AFTER"
    else
      fail "Metric counter did not increment: allowed $BEFORE → ${AFTER:-?}"
    fi
  else
    info "Metric increment check skipped (no minion key or counter unavailable)"
  fi

  # ── Log redaction ────────────────────────────────────────────────────────
  section "Log redaction"

  if [[ -n "$MINION_KEY" ]]; then
    curl -s -u "subscriber:${MINION_KEY}" \
      "$BASE_URL/rpm/minion/2025/el9-x86_64/" > /dev/null || true
  fi

  LOGS=$($COMPOSE logs --no-log-prefix auth 2>/dev/null | tail -40 || true)

  if [[ -n "$MINION_KEY" ]] && echo "$LOGS" | grep -qF "$MINION_KEY"; then
    fail "Log redaction: key value found in auth container logs"
  else
    pass "Log redaction: key value absent from auth container logs"
  fi

  # ── Backup integrity ─────────────────────────────────────────────────────
  section "Backup integrity"

  if $COMPOSE exec -T backup /scripts/backup-keystore.sh > /dev/null 2>&1; then
    pass "Backup script executed"
  else
    fail "Backup script failed or backup container not running"
  fi

  LATEST=$($COMPOSE exec -T backup ls -t /backup/ 2>/dev/null \
    | head -1 | tr -d '\r' || true)
  if [[ -n "$LATEST" ]]; then
    pass "Backup file found: $LATEST"

    INTEGRITY=$($COMPOSE exec -T backup \
      sqlite3 "/backup/${LATEST}" "PRAGMA integrity_check;" 2>/dev/null || true)
    [[ "$INTEGRITY" == "ok" ]] \
      && pass "Backup integrity check: ok" \
      || fail "Backup integrity check: ${INTEGRITY:-failed}"

    ROW_COUNT=$($COMPOSE exec -T backup \
      sqlite3 "/backup/${LATEST}" \
      "SELECT count(*) FROM subscription_key;" 2>/dev/null || echo "0")
    (( ROW_COUNT >= 1 )) \
      && pass "Backup contains $ROW_COUNT subscription key(s)" \
      || fail "Backup row count unexpected: $ROW_COUNT"
  else
    fail "No backup file found in /backup/"
  fi

fi  # end local-only

# ─────────────────────────────────────────────────────────────────────────────
# Cleanup  (local only)
# ─────────────────────────────────────────────────────────────────────────────
if [[ "$MODE" == "local" ]]; then
  echo ""
  echo "--- Cleanup ---"
  [[ -n "$MINION_KEY" ]] && \
    curl -s -X DELETE "$ADMIN_URL/api/v1/keys/${MINION_KEY}" > /dev/null || true
  info "Test keys revoked"
fi

# ─────────────────────────────────────────────────────────────────────────────
# Summary
# ─────────────────────────────────────────────────────────────────────────────
echo ""
echo "=== Results: ${PASS} passed, ${FAIL} failed ==="

if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
