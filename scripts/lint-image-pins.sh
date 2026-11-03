#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# lint-image-pins.sh — every third-party container image in a compose file
# must be pinned `tag@sha256:<digest>`, the way the Dockerfiles pin their
# bases. A tag alone is mutable: the deployment host runs watchtower, which
# re-pulls a moved tag and once broke the auth service that way.
#
# Two exemptions:
#   - `ghcr.io/no42-org/packyard-*` — first-party images. Their tag is written
#     by the release process (RELEASING.md § 1), not by a bot, and pinning a
#     digest there would mean a compose change for every rebuild.
#   - `*:local` — images built by a test stack, never pulled.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

FAILED=0
note() { echo "lint-image-pins: $*" >&2; FAILED=1; }

for f in compose.yml compose.override.*.yml tests/publish/compose.yml; do
  [ -f "$f" ] || continue
  while IFS= read -r entry; do
    lineno="${entry%%:*}"                  # `grep -n` prefix
    ref="${entry#*image:}"
    ref="${ref#"${ref%%[![:space:]]*}"}"   # strip leading whitespace
    case "${ref}" in
      ghcr.io/no42-org/packyard-*) continue ;;
      *:local) continue ;;
    esac
    case "${ref}" in
      *@sha256:*) ;;
      *) note "${f}:${lineno}: '${ref}' is not pinned by digest (expected <image>:<tag>@sha256:<digest>)" ;;
    esac
  done < <(grep -n '^[[:space:]]*image:' "$f")
done

if [ "${FAILED}" -ne 0 ]; then
  exit 1
fi
echo "image pins OK"
