#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# verify-env.sh — prove two things about the running CI stack:
#   1. every PACKYARD_* variable compose.yml forwards reaches the auth
#      container, and the two that carry values in CI carry the right ones;
#   2. compose refuses to start without ADMIN_DOMAIN (the `${VAR:?}` guard).
# Used by `make ci-verify-env`.
set -euo pipefail
# shellcheck source=scripts/ci/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
require_cmds docker jq grep

ENV_FILE="${COMPOSE_ENV_FILES:-.env}"
case "${ENV_FILE}" in *,*) echo "ERROR: COMPOSE_ENV_FILES must name a single file for this check (got '${ENV_FILE}')" >&2; exit 1 ;; esac
[ -r "${ENV_FILE}" ] || { echo "ERROR: env file not readable: ${ENV_FILE}" >&2; exit 1; }
env_value() { sed -n "s/^$1=//p" "${ENV_FILE}" | tail -1; }

WANT_ADMIN_DOMAIN=$(env_value ADMIN_DOMAIN)
WANT_BOOTSTRAP=$(env_value PACKYARD_BOOTSTRAP_OPERATOR_EMAIL)
[ -n "${WANT_ADMIN_DOMAIN}" ] || { echo "ERROR: ${ENV_FILE} does not set ADMIN_DOMAIN; the guard check would be meaningless" >&2; exit 1; }
[ -n "${WANT_BOOTSTRAP}" ] || { echo "ERROR: ${ENV_FILE} does not set PACKYARD_BOOTSTRAP_OPERATOR_EMAIL" >&2; exit 1; }

# --- 1. container environment --------------------------------------------
CIDS=$(docker compose ps -q --status running auth)
[ "$(printf '%s\n' "${CIDS}" | grep -c .)" = "1" ] || { echo "ERROR: expected exactly one running auth container, got: '${CIDS}'" >&2; exit 1; }
ENV_DUMP=$(docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "${CIDS}")

for v in PACKYARD_ADMIN_HOST PACKYARD_BOOTSTRAP_OPERATOR_EMAIL PACKYARD_PUBLIC_COMPONENT_CACHE_TTL \
         PACKYARD_GITHUB_CLIENT_ID PACKYARD_GITHUB_CLIENT_SECRET PACKYARD_GITHUB_REDIRECT_URI PACKYARD_GITHUB_ORG \
         PACKYARD_MICROSOFT_CLIENT_ID PACKYARD_MICROSOFT_CLIENT_SECRET PACKYARD_MICROSOFT_REDIRECT_URI PACKYARD_MICROSOFT_TENANT_ID; do
  printf '%s\n' "${ENV_DUMP}" | grep -q "^${v}=" || { echo "ERROR: ${v} is not forwarded into the auth container" >&2; exit 1; }
done
got_host=$(printf '%s\n' "${ENV_DUMP}" | sed -n 's/^PACKYARD_ADMIN_HOST=//p')
got_boot=$(printf '%s\n' "${ENV_DUMP}" | sed -n 's/^PACKYARD_BOOTSTRAP_OPERATOR_EMAIL=//p')
[ "${got_host}" = "https://${WANT_ADMIN_DOMAIN}" ] || { echo "ERROR: PACKYARD_ADMIN_HOST='${got_host}', want 'https://${WANT_ADMIN_DOMAIN}'" >&2; exit 1; }
[ "${got_boot}" = "${WANT_BOOTSTRAP}" ] || { echo "ERROR: PACKYARD_BOOTSTRAP_OPERATOR_EMAIL='${got_boot}', want '${WANT_BOOTSTRAP}'" >&2; exit 1; }
echo "auth container receives all forwarded PACKYARD_* variables; admin host and bootstrap email carry the expected values"

# --- 2. compose guard ----------------------------------------------------
TMP=$(mktemp); trap 'rm -f "${TMP}" "${TMP}.err"' EXIT
grep -v '^ADMIN_DOMAIN=' "${ENV_FILE}" > "${TMP}"
# Unset it in the process environment too: compose prefers the shell over --env-file.
if env -u ADMIN_DOMAIN docker compose --env-file "${TMP}" config > /dev/null 2> "${TMP}.err"; then
  echo "ERROR: expected compose to reject a configuration without ADMIN_DOMAIN" >&2; exit 1
fi
grep -q 'ADMIN_DOMAIN' "${TMP}.err" || { echo "ERROR: compose failed for another reason:" >&2; cat "${TMP}.err" >&2; exit 1; }
echo "compose refuses to start without ADMIN_DOMAIN, as intended"
