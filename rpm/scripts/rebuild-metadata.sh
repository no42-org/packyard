#!/usr/bin/env bash
# rebuild-metadata.sh — runs createrepo_c for one component/year/os-arch tree
# Usage: rebuild-metadata.sh <root> <component> <year> <os-arch>
#
# Serialisation is enforced externally by the GHA concurrency group:
#   rpm-publish-${component}-${os}  (cancel-in-progress: false)
# Do NOT add file locks here — the lock must cover the entire publish operation
# (copy + rebuild), not just the rebuild step.
set -euo pipefail

ROOT="${1:?RPM tree root required (e.g. /usr/share/nginx/html)}"
COMPONENT="${2:?component required}"
YEAR="${3:?year required}"
OS_ARCH="${4:?os-arch required}"

TARGET="${ROOT}/rpm/${COMPONENT}/${YEAR}/${OS_ARCH}"
[ -d "${TARGET}" ] || { echo "ERROR: directory not found: ${TARGET}"; exit 1; }

echo "Rebuilding RPM metadata for ${TARGET}..."
createrepo_c --update --workers 4 "${TARGET}"
echo "Done."
