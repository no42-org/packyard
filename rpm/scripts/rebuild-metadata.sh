#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# rebuild-metadata.sh — runs createrepo_c for one component/series/os-arch tree
# Usage: rebuild-metadata.sh <root> <component> <series> <os-arch>
#
# Serialisation is enforced externally by the GHA concurrency group of the
# calling workflow (cancel-in-progress: false). Do NOT add file locks here:
# the lock must cover the entire publish operation (copy + rebuild), not just
# the rebuild step.
set -euo pipefail

ROOT="${1:?RPM tree root required (e.g. /usr/share/nginx/html)}"
COMPONENT="${2:?component required}"
SERIES="${3:?series required}"
OS_ARCH="${4:?os-arch required}"

SEGMENT_RE='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
for value in "${COMPONENT}" "${SERIES}" "${OS_ARCH}"; do
  if ! [[ "${value}" =~ ${SEGMENT_RE} ]] || [[ "${value}" == *..* ]]; then
    echo "ERROR: invalid path segment: ${value}"
    exit 1
  fi
done

TARGET="${ROOT}/rpm/${COMPONENT}/${SERIES}/${OS_ARCH}"
[ -d "${TARGET}" ] || { echo "ERROR: directory not found: ${TARGET}"; exit 1; }

echo "Rebuilding RPM metadata for ${TARGET}..."
# gzip metadata: createrepo_c 1.x defaults to zstd, which older dnf/yum
# clients (EL8) cannot read.
createrepo_c --update --workers 4 --general-compress-type gz "${TARGET}"
echo "Done."
