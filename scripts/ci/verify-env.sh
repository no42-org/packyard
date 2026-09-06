#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# verify-env.sh — prove three things about the running CI stack:
#   1. every PACKYARD_* variable compose.yml forwards reaches the auth
#      container, and the two that carry values in CI carry the right ones;
#   2. compose refuses to start without ADMIN_DOMAIN (the `${VAR:?}` guard);
#   3. every service with a build: entry is running an image built from the
#      commit under test (org.opencontainers.image.revision == CI_REVISION);
#   4. the backup and aptly image tags in compose.yml match the Dockerfile ARGs
#      the publish workflows derive them from.
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

# --- 3. images under test ------------------------------------------------
: "${CI_REVISION:?CI_REVISION is required (the commit the images were built from)}"
# Derive the list from compose config so a new build: entry is asserted
# automatically instead of silently escaping the check.
BUILT_SERVICES=$(docker compose config --format json | jq -r '.services | to_entries[] | select(.value.build) | .key' | sort)
[ -n "${BUILT_SERVICES}" ] || { echo "ERROR: no services with a build: entry in the compose configuration" >&2; exit 1; }
for svc in ${BUILT_SERVICES}; do
  cid=$(docker compose ps -q --status running "${svc}" 2>/dev/null) || cid=""
  [ "$(printf '%s\n' "${cid}" | grep -c .)" = "1" ] || { echo "ERROR: expected exactly one running ${svc} container, got: '${cid}'" >&2; exit 1; }
  rev=$(docker inspect --format '{{index .Config.Labels "org.opencontainers.image.revision"}}' "${cid}")
  case "${rev}" in
    ""|"<no value>") echo "ERROR: ${svc} runs an image with no org.opencontainers.image.revision label (pulled, not built here)" >&2; exit 1 ;;
  esac
  [ "${rev}" = "${CI_REVISION}" ] || { echo "ERROR: ${svc} runs an image with revision '${rev}', want '${CI_REVISION}'" >&2; exit 1; }
done
# shellcheck disable=SC2086  # word-splitting the service list is intended
echo "$(printf '%s, ' ${BUILT_SERVICES} | sed 's/, $//') run images built from ${CI_REVISION:0:12}${CI_REVISION#"${CI_REVISION%-dirty}"}"

# --- 4. published tags stay in sync with the Dockerfile ARGs -------------
# build-backup.yml and build-aptly.yml derive the published tag from these
# ARGs; compose.yml carries the tag by hand. Catch drift here.
compose_tag() { docker compose config --format json | jq -r --arg s "$1" '.services[$s].image | split(":")[1]'; }
sqlite_ver=$(sed -n 's/^ARG SQLITE_VERSION=//p' backup/Dockerfile); sqlite_ver="${sqlite_ver%-r*}"
aptly_ver=$(sed -n 's/^ARG APTLY_VERSION=//p' aptly/Dockerfile)
[ "$(compose_tag backup)" = "${sqlite_ver}" ] || { echo "ERROR: compose backup tag '$(compose_tag backup)' != Dockerfile SQLITE_VERSION '${sqlite_ver}'" >&2; exit 1; }
[ "$(compose_tag aptly)" = "${aptly_ver}" ] || { echo "ERROR: compose aptly tag '$(compose_tag aptly)' != Dockerfile APTLY_VERSION '${aptly_ver}'" >&2; exit 1; }
echo "backup and aptly compose tags match their Dockerfile ARGs (${sqlite_ver}, ${aptly_ver})"
