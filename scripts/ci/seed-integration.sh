#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# seed-integration.sh — give CI an operator session and seed test data through
# the real admin API.
#
# The admin API sits behind OAuth: /api/v1/* requires a session cookie, a
# matching Origin header (CSRF guard) and an active operator. CI cannot log in
# through GitHub or Microsoft, so this script:
#   1. inserts one session row for the bootstrap operator directly into the
#      auth SQLite database, using the backup image as a one-off sqlite3
#      sidecar on the auth-db volume (the auth image is distroless);
#   2. verifies the session with GET /api/v1/auth/whoami;
#   3. provisions the CI components (core public, minion private);
#   4. restarts auth so the keys handler reloads its component set;
#   5. creates a CI subscriber account and a subscription key for core,
#      exported as VALID_KEY.
#
# Required environment:
#   COMPOSE_PROJECT_NAME  compose project (volume is <project>_auth-db)
#   ADMIN_ORIGIN          value for the Origin header, must equal PACKYARD_ADMIN_HOST
#   BOOTSTRAP_EMAIL       the PACKYARD_BOOTSTRAP_OPERATOR_EMAIL the stack started with
# Optional:
#   AUTH_URL   (default http://localhost:8080)
#   GITHUB_ENV if set, VALID_KEY is appended for later workflow steps
set -euo pipefail

: "${COMPOSE_PROJECT_NAME:?COMPOSE_PROJECT_NAME is required}"
: "${ADMIN_ORIGIN:?ADMIN_ORIGIN is required (e.g. https://admin.ci.local)}"
: "${BOOTSTRAP_EMAIL:?BOOTSTRAP_EMAIL is required}"
AUTH_URL="${AUTH_URL:-http://localhost:8080}"
VOLUME="${COMPOSE_PROJECT_NAME}_auth-db"

# wait_until SECONDS COMMAND... — poll COMMAND every 2s until it succeeds or the
# deadline passes. Portable (no GNU timeout, which macOS lacks).
wait_until() {
  local deadline=$(( $(date +%s) + $1 )); shift
  until "$@" > /dev/null 2>&1; do
    [ "$(date +%s)" -lt "${deadline}" ] || { echo "ERROR: timed out waiting for: $*" >&2; return 1; }
    sleep 2
  done
}

# The backup service image carries sqlite3; reuse it rather than pinning a
# second image here. compose resolves the pinned tag from compose.yml.
SIDECAR_IMAGE=$(docker compose config --format json | jq -r '.services.backup.image')
[ -n "${SIDECAR_IMAGE}" ] && [ "${SIDECAR_IMAGE}" != "null" ] \
  || { echo "ERROR: could not resolve the backup image from compose config" >&2; exit 1; }

sql() {
  docker run --rm -v "${VOLUME}:/data/db" --entrypoint sqlite3 "${SIDECAR_IMAGE}" /data/db/auth.db "$1"
}

# 1. Seed a session for the bootstrap operator. Timestamps use the same
#    RFC 3339 layout the Go store writes. One hour is plenty for a CI run and
#    well under the service's 8h idle / 24h absolute limits.
SESSION_ID=$(openssl rand -hex 32)
ROWS=$(sql "INSERT INTO sessions (id, operator_id, created_at, last_seen_at, expires_at, ip, user_agent)
  SELECT '${SESSION_ID}', id,
         strftime('%Y-%m-%dT%H:%M:%SZ','now'),
         strftime('%Y-%m-%dT%H:%M:%SZ','now'),
         strftime('%Y-%m-%dT%H:%M:%SZ','now','+1 hour'),
         '127.0.0.1', 'packyard-ci'
  FROM operators WHERE email = lower('${BOOTSTRAP_EMAIL}') AND status = 'active';
  SELECT changes();")
if [ "${ROWS}" != "1" ]; then
  echo "ERROR: expected to seed 1 session row for ${BOOTSTRAP_EMAIL}, got '${ROWS}'." >&2
  echo "       Is PACKYARD_BOOTSTRAP_OPERATOR_EMAIL set and forwarded to the auth container?" >&2
  sql "SELECT id, email, role, status FROM operators;" >&2 || true
  exit 1
fi
echo "Seeded operator session for ${BOOTSTRAP_EMAIL}."

COOKIE="packyard_session=${SESSION_ID}"
api() { # method path [json-body]
  local method="$1" path="$2" body="${3:-}"
  if [ -n "${body}" ]; then
    curl -s -o /tmp/api-body -w '%{http_code}' -X "${method}" "${AUTH_URL}${path}" \
      -H "Cookie: ${COOKIE}" -H "Origin: ${ADMIN_ORIGIN}" -H 'Content-Type: application/json' -d "${body}"
  else
    curl -s -o /tmp/api-body -w '%{http_code}' -X "${method}" "${AUTH_URL}${path}" \
      -H "Cookie: ${COOKIE}" -H "Origin: ${ADMIN_ORIGIN}"
  fi
}

# 2. The session must be accepted by the real middleware chain.
CODE=$(api GET /api/v1/auth/whoami)
[ "${CODE}" = "200" ] || { echo "ERROR: whoami returned ${CODE}: $(cat /tmp/api-body)" >&2; exit 1; }
echo "whoami OK: $(jq -c '{email, role}' /tmp/api-body)"

# 3. Components: core public, minion private. 409 means a re-run; fine.
for payload in \
  '{"name":"core","visibility":"public","rpm_series":["2025"],"rpm_os_families":["el9"],"rpm_architectures":["x86_64"]}' \
  '{"name":"minion","visibility":"private","rpm_series":["2025"],"rpm_os_families":["el9"],"rpm_architectures":["x86_64"]}'; do
  CODE=$(api POST /api/v1/components "${payload}")
  case "${CODE}" in 201|409) ;; *) echo "ERROR: component seed returned ${CODE}: $(cat /tmp/api-body)" >&2; exit 1 ;; esac
done
echo "Components provisioned."

# 4. The keys handler validates component names against a set loaded at
#    startup, so restart auth before creating a key.
docker compose restart auth
wait_until 90 curl -sf "${AUTH_URL}/health"
echo "Auth reloaded with new components."

# 5. Keys belong to accounts. Create (or reuse, on 409) the CI account, then
#    issue a key for the public test component.
CODE=$(api POST /api/v1/accounts '{"email":"ci-subscriber@example.org","org_name":"packyard CI"}')
case "${CODE}" in
  201) ACCOUNT_ID=$(jq -r '.id' /tmp/api-body) ;;
  409) CODE=$(api GET '/api/v1/accounts?limit=100')
       [ "${CODE}" = "200" ] || { echo "ERROR: listing accounts returned ${CODE}: $(cat /tmp/api-body)" >&2; exit 1; }
       ACCOUNT_ID=$(jq -r 'if type == "array" then . else .accounts end | map(select(.email == "ci-subscriber@example.org")) | .[0].id' /tmp/api-body) ;;
  *)   echo "ERROR: account creation returned ${CODE}: $(cat /tmp/api-body)" >&2; exit 1 ;;
esac
[ -n "${ACCOUNT_ID}" ] && [ "${ACCOUNT_ID}" != "null" ] || { echo "ERROR: could not determine CI account id" >&2; exit 1; }
echo "CI subscriber account ${ACCOUNT_ID:0:8}…"

CODE=$(api POST /api/v1/keys "{\"account_id\":\"${ACCOUNT_ID}\",\"component\":\"core\",\"label\":\"ci-integration-test\"}")
[ "${CODE}" = "201" ] || { echo "ERROR: key creation returned ${CODE}: $(cat /tmp/api-body)" >&2; exit 1; }
VALID_KEY=$(jq -r '.id' /tmp/api-body)
[ -n "${VALID_KEY}" ] && [ "${VALID_KEY}" != "null" ] || { echo "ERROR: no key id in response" >&2; exit 1; }
if [ -n "${GITHUB_ENV:-}" ]; then
  echo "VALID_KEY=${VALID_KEY}" >> "${GITHUB_ENV}"
fi
echo "Seeded subscription key ${VALID_KEY:0:8}… for component core."
