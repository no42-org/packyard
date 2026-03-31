#!/usr/bin/env bash
# create-snapshot.sh — create an immutable Aptly snapshot from staged DEBs
# Usage: create-snapshot.sh <component> <year> <distro>
# Example: create-snapshot.sh core 2025 bookworm
#
# Staged DEBs must be present in /tmp/deb-stage/ before calling this script.
# Outputs the snapshot name to stdout on success.
set -euo pipefail

COMPONENT="${1:?component required (core|minion|sentinel)}"
YEAR="${2:?year required (e.g. 2025)}"
DISTRO="${3:?distro required (bookworm|trixie|jammy|noble)}"

REPO_NAME="${COMPONENT}-${YEAR}"
SNAPSHOT_NAME="${COMPONENT}-${YEAR}-$(date -u +%Y%m%dT%H%M%SZ)"
STAGE_DIR="${DEB_STAGE_DIR:-/tmp/deb-stage}"

echo "Creating snapshot: ${SNAPSHOT_NAME}" >&2

# Create local repo if it doesn't already exist
if ! aptly repo show "${REPO_NAME}" > /dev/null 2>&1; then
  echo "Creating Aptly repo: ${REPO_NAME}" >&2
  aptly repo create -component="${COMPONENT}" "${REPO_NAME}" >&2
fi

# Add staged DEBs to the local repo
echo "Adding packages from ${STAGE_DIR} to repo ${REPO_NAME}..." >&2
aptly repo add "${REPO_NAME}" "${STAGE_DIR}/" >&2

# Create immutable snapshot
echo "Creating snapshot from repo ${REPO_NAME}..." >&2
aptly snapshot create "${SNAPSHOT_NAME}" from repo "${REPO_NAME}" >&2

# Stdout is reserved for the snapshot name — caller captures it directly
echo "${SNAPSHOT_NAME}"
