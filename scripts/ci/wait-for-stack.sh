#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# wait-for-stack.sh — block until Traefik answers on the public entrypoint and
# the auth service reports healthy. Used by `make ci-stack-up`.
#
#   BASE_URL  public base URL through Traefik   (default http://localhost)
#   AUTH_URL  auth admin API published to host  (default http://localhost:8080)
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost}"
AUTH_URL="${AUTH_URL:-http://localhost:8080}"

# wait_until SECONDS COMMAND... — poll COMMAND every 2s until it succeeds or the
# deadline passes. Portable (no GNU timeout, which macOS lacks).
wait_until() {
  local deadline=$(( $(date +%s) + $1 )); shift
  until "$@" > /dev/null 2>&1; do
    [ "$(date +%s)" -lt "${deadline}" ] || { echo "ERROR: timed out waiting for: $*" >&2; return 1; }
    sleep 2
  done
}

traefik_up() { curl -s -o /dev/null -w '%{http_code}' "${BASE_URL}/gpg/lts.asc" | grep -qE '^(200|404)$'; }

echo "Waiting for Traefik at ${BASE_URL} ..."
wait_until 60 traefik_up
echo "Traefik is ready."

echo "Waiting for auth at ${AUTH_URL}/health ..."
wait_until 90 curl -sf "${AUTH_URL}/health"
echo "Auth is healthy."
