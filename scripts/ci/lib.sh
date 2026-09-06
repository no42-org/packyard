#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# lib.sh — helpers shared by the CI stack scripts. Source it; do not execute.

# require_cmds NAME... — fail early with one clear message per missing tool.
require_cmds() {
  local missing=0 c
  for c in "$@"; do
    command -v "$c" > /dev/null 2>&1 || { echo "ERROR: required command not found: $c" >&2; missing=1; }
  done
  [ "$missing" -eq 0 ]
}

# wait_until SECONDS COMMAND... — poll COMMAND every 2s until it succeeds or the
# deadline passes. Portable (no GNU timeout, which macOS lacks). COMMAND must
# bound its own runtime (e.g. curl --max-time), otherwise the deadline is only
# checked between attempts.
wait_until() {
  local deadline=$(( $(date +%s) + $1 )); shift
  until "$@" > /dev/null 2>&1; do
    [ "$(date +%s)" -lt "${deadline}" ] || { echo "ERROR: timed out waiting for: $*" >&2; return 1; }
    sleep 2
  done
}

# compose_auth_env NAME — the resolved value of an auth service environment
# variable from `docker compose config`, so scripts read the same values the
# container gets instead of duplicating literals.
compose_auth_env() {
  docker compose config --format json | jq -r --arg k "$1" '.services.auth.environment[$k] // empty'
}
