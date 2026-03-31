#!/usr/bin/env bash
# add-package.sh — copy a signed RPM into the tree and rebuild metadata
# Usage: add-package.sh <signed-rpm> <component> <year> <os-arch>
#
# Relies on GHA concurrency group (rpm-publish-${component}-${os}) to prevent
# concurrent createrepo_c runs on the same directory. No shell-level file lock.
set -euo pipefail

RPM_FILE="${1:?signed RPM path required}"
COMPONENT="${2:?component required}"
YEAR="${3:?year required}"
OS_ARCH="${4:?os-arch required}"
ROOT="${RPM_ROOT:-/usr/share/nginx/html}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET="${ROOT}/rpm/${COMPONENT}/${YEAR}/${OS_ARCH}"

[ -f "${RPM_FILE}" ] || { echo "ERROR: file not found: ${RPM_FILE}"; exit 1; }
[ -d "${TARGET}" ] || { echo "ERROR: target dir not found: ${TARGET}"; exit 1; }

echo "Copying $(basename "${RPM_FILE}") to ${TARGET}/"
cp "${RPM_FILE}" "${TARGET}/"

"${SCRIPT_DIR}/rebuild-metadata.sh" "${ROOT}" "${COMPONENT}" "${YEAR}" "${OS_ARCH}"
