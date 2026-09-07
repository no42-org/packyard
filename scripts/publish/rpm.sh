#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# rpm.sh — publish signed RPMs into the rpm container of a compose project.
#
# Usage: rpm.sh <compose.yml> <component> <series> <os-arch> [<os-arch>...] < packages.tar
#
# Reads a tar of *.rpm files on standard input and streams it into the rpm
# container's /tmp tmpfs (the container's root filesystem is read-only, which
# docker cp refuses). Every package is then published with
# /scripts/add-package.sh into each os-arch target and deleted, one after the
# other, so tmpfs never holds more than what the caller sent. Callers that
# care about memory send one package per call.
#
# Runs wherever docker compose can reach the project: on the deployment host
# for promotions (the workflows stream this script over SSH), on the runner
# for CI. The same file serves both, which is the point: a publish path that
# CI has not executed is one production will find broken.
#
# The tree is group 101, setgid and group-writable, and the container has no
# capabilities, so the publish runs as 0:101 and extracts without ownership.
set -euo pipefail

COMPOSE="${1:?compose.yml path required}"
COMPONENT="${2:?component required}"
SERIES="${3:?series required}"
shift 3
[ "$#" -ge 1 ] || { echo "ERROR: at least one os-arch target required" >&2; exit 1; }

seg='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
for v in "${COMPONENT}" "${SERIES}" "$@"; do
  [[ "$v" =~ $seg ]] && [[ "$v" != *..* ]] || { echo "ERROR: invalid path segment: ${v}" >&2; exit 1; }
done
[ -f "${COMPOSE}" ] || { echo "ERROR: compose file not found: ${COMPOSE}" >&2; exit 1; }

# One exec: extract, publish each package into every target, remove it. The
# target list is validated above, so word-splitting it is safe.
docker compose -f "${COMPOSE}" exec -T --user 0:101 rpm bash -c '
  set -euo pipefail
  component="$1"; series="$2"; shift 2
  stage=/tmp/rpm-stage
  rm -rf "$stage" && mkdir -p "$stage"
  tar -xf - --no-same-owner -C "$stage"
  shopt -s nullglob
  files=("$stage"/*.rpm)
  [ "${#files[@]}" -gt 0 ] || { echo "ERROR: the tar on stdin contained no .rpm files" >&2; exit 1; }
  for f in "${files[@]}"; do
    echo "publishing $(basename "$f") into: $*"
    /scripts/add-package.sh "$f" "$component" "$series" "$@"
    rm -f "$f"
  done
  rm -rf "$stage"
' publish "${COMPONENT}" "${SERIES}" "$@"
