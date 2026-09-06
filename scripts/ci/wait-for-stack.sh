#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# wait-for-stack.sh — block until every long-running compose service is running
# (and healthy where it declares a healthcheck), Traefik routes the public GPG
# key (200, so the file provider has loaded its routers) and the auth service
# is healthy. One-shot services that exited 0 (rustfs-init) are accepted;
# `docker compose up --wait` would treat them as failures.
# Used by `make ci-stack-up`.
#
#   BASE_URL  public base URL through Traefik   (default http://localhost)
#   AUTH_URL  auth admin API published to host  (default http://localhost:8080)
set -euo pipefail
# shellcheck source=scripts/ci/lib.sh
source "$(dirname "${BASH_SOURCE[0]}")/lib.sh"
require_cmds curl docker jq

BASE_URL="${BASE_URL:-http://localhost}"
AUTH_URL="${AUTH_URL:-http://localhost:8080}"

stack_settled() {
  # Every container: exited 0 (one-shot) or running and not unhealthy/starting.
  docker compose ps -a --format json | jq -e -s '
    map(if type == "array" then .[] else . end)
    | all(
        (.State == "exited" and .ExitCode == 0)
        or (.State == "running" and ((.Health // "") == "" or .Health == "healthy"))
      )' > /dev/null
}

traefik_routes() {
  # 200 only: a 404 is also what Traefik returns before any router is loaded.
  [ "$(curl -s --max-time 5 -o /dev/null -w '%{http_code}' "${BASE_URL}/gpg/lts.asc")" = "200" ]
}

echo "Waiting for all services to be running or cleanly exited ..."
wait_until 180 stack_settled
echo "Services settled."

echo "Waiting for Traefik to route ${BASE_URL}/gpg/lts.asc ..."
wait_until 60 traefik_routes
echo "Traefik is routing."

echo "Waiting for auth at ${AUTH_URL}/health ..."
wait_until 90 curl -sf --max-time 5 "${AUTH_URL}/health"
echo "Auth is healthy."
