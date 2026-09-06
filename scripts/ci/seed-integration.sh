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
#   5. creates a per-run CI subscriber account and a subscription key for core,
#      exported as VALID_KEY (masked in Actions logs) and written to a key file
#      for `make e2e-observability`.
#
# Reads the admin host and bootstrap email from `docker compose config`, so the
# values always match what the auth container was started with. Requires
# COMPOSE_PROJECT_NAME (the volume is <project>_auth-db). Optional:
#   AUTH_URL        default http://localhost:8080
#   VALID_KEY_FILE  default .ci-valid-key (git-ignored)
#   GITHUB_ENV      if set, VALID_KEY is exported for later workflow steps
set -euo pipefail
# shellcheck source=scripts/ci/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
require_cmds docker jq curl openssl

: "${COMPOSE_PROJECT_NAME:?COMPOSE_PROJECT_NAME is required}"
AUTH_URL="${AUTH_URL:-http://localhost:8080}"
VALID_KEY_FILE="${VALID_KEY_FILE:-.ci-valid-key}"
VOLUME="${COMPOSE_PROJECT_NAME}_auth-db"

ADMIN_ORIGIN=$(compose_auth_env PACKYARD_ADMIN_HOST)
BOOTSTRAP_EMAIL=$(compose_auth_env PACKYARD_BOOTSTRAP_OPERATOR_EMAIL)
[ -n "${ADMIN_ORIGIN}" ] || { echo "ERROR: PACKYARD_ADMIN_HOST is not set for the auth service" >&2; exit 1; }
[ -n "${BOOTSTRAP_EMAIL}" ] || { echo "ERROR: PACKYARD_BOOTSTRAP_OPERATOR_EMAIL is empty; the stack must start with a bootstrap operator" >&2; exit 1; }
# The email is interpolated into SQL below; only allow the characters an
# address legitimately contains, which excludes quotes and semicolons.
[[ "${BOOTSTRAP_EMAIL}" =~ ^[A-Za-z0-9._%+@-]+$ ]] || { echo "ERROR: bootstrap email contains characters outside [A-Za-z0-9._%+@-]" >&2; exit 1; }

docker volume inspect "${VOLUME}" > /dev/null 2>&1 \
  || { echo "ERROR: volume ${VOLUME} does not exist; is the stack up under COMPOSE_PROJECT_NAME=${COMPOSE_PROJECT_NAME}?" >&2; exit 1; }

# The backup service image carries sqlite3; reuse it rather than pinning a
# second image here.
SIDECAR_IMAGE=$(docker compose config --format json | jq -r '.services.backup.image // empty')
[ -n "${SIDECAR_IMAGE}" ] || { echo "ERROR: could not resolve the backup image from compose config" >&2; exit 1; }

sql() {
  # .timeout: auth holds the database open (WAL); wait up to 5s for its writes.
  # Set via -cmd so it produces no output that would pollute query results.
  docker run --rm -v "${VOLUME}:/data/db" --entrypoint sqlite3 "${SIDECAR_IMAGE}" \
    -cmd ".timeout 5000" /data/db/auth.db "$1"
}

# 1. Seed a session for the bootstrap operator. Timestamps use the RFC 3339
#    layout the Go store writes. One hour covers a CI run and is well under the
#    service's 8h idle / 24h absolute limits.
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
  echo "ERROR: expected to seed 1 session row for the bootstrap operator, got '${ROWS}'." >&2
  sql "SELECT id, role, status FROM operators;" >&2 || true
  exit 1
fi
echo "Seeded an operator session."

BODY=$(mktemp); trap 'rm -f "${BODY}"' EXIT
COOKIE="packyard_session=${SESSION_ID}"
api() { # method path [json-body] -> prints HTTP code, 000 on transport failure
  local method="$1" path="$2" body="${3:-}" code
  if [ -n "${body}" ]; then
    code=$(curl -s --max-time 20 -o "${BODY}" -w '%{http_code}' -X "${method}" "${AUTH_URL}${path}" \
      -H "Cookie: ${COOKIE}" -H "Origin: ${ADMIN_ORIGIN}" -H 'Content-Type: application/json' -d "${body}") || code=000
  else
    code=$(curl -s --max-time 20 -o "${BODY}" -w '%{http_code}' -X "${method}" "${AUTH_URL}${path}" \
      -H "Cookie: ${COOKIE}" -H "Origin: ${ADMIN_ORIGIN}") || code=000
  fi
  printf '%s' "${code}"
}
fail_api() { echo "ERROR: $1 returned HTTP $2: $(cat "${BODY}" 2>/dev/null)" >&2; exit 1; }

# 2. The session must be accepted by the real middleware chain.
CODE=$(api GET /api/v1/auth/whoami); [ "${CODE}" = "200" ] || fail_api whoami "${CODE}"
ROLE=$(jq -er '.role' "${BODY}") || { echo "ERROR: whoami body is not the expected JSON" >&2; exit 1; }
echo "whoami OK (role ${ROLE})."

# 3. Components: core public, minion private. 409 means a re-run; fine.
for payload in \
  '{"name":"core","visibility":"public","rpm_series":["2025"],"rpm_os_families":["el9"],"rpm_architectures":["x86_64"]}' \
  '{"name":"minion","visibility":"private","rpm_series":["2025"],"rpm_os_families":["el9"],"rpm_architectures":["x86_64"]}'; do
  CODE=$(api POST /api/v1/components "${payload}")
  case "${CODE}" in 201|409) ;; *) fail_api "component seed" "${CODE}" ;; esac
done
echo "Components provisioned."

# 4. The keys handler validates component names against a set loaded at
#    startup, so restart auth before creating a key. `docker compose restart`
#    is synchronous; the poll then covers listener start-up.
docker compose restart auth
wait_until 90 curl -sf --max-time 5 "${AUTH_URL}/health"
echo "Auth reloaded with new components."

# 5. A fresh account per run avoids any collision with soft-deleted or
#    suspended accounts from earlier runs against a persisted database.
ACCOUNT_EMAIL="ci-subscriber-${SESSION_ID:0:8}@example.org"
CODE=$(api POST /api/v1/accounts "{\"email\":\"${ACCOUNT_EMAIL}\",\"org_name\":\"packyard CI\"}")
[ "${CODE}" = "201" ] || fail_api "account creation" "${CODE}"
ACCOUNT_ID=$(jq -er '.id' "${BODY}")

CODE=$(api POST /api/v1/keys "{\"account_id\":\"${ACCOUNT_ID}\",\"component\":\"core\",\"label\":\"ci-integration-test\"}")
[ "${CODE}" = "201" ] || fail_api "key creation" "${CODE}"
VALID_KEY=$(jq -er '.id' "${BODY}")

if [ -n "${GITHUB_ACTIONS:-}" ]; then echo "::add-mask::${VALID_KEY}"; fi
if [ -n "${GITHUB_ENV:-}" ]; then printf 'VALID_KEY<<EOF\n%s\nEOF\n' "${VALID_KEY}" >> "${GITHUB_ENV}"; fi
umask 077; printf '%s\n' "${VALID_KEY}" > "${VALID_KEY_FILE}"
echo "Seeded subscription key for component core (written to ${VALID_KEY_FILE})."
