#!/usr/bin/env bash
# publish-snapshot.sh — publish or atomically switch an Aptly snapshot
# Usage: publish-snapshot.sh <snapshot-name> <component> <year> <distro>
# Example: publish-snapshot.sh core-2025-20260329T120000Z core 2025 bookworm
#
# Publish path: :{component}/{year}
# Maps to subscriber URL: https://pkg.mdn.opennms.com/deb/{component}/{year}/
# Aptly's publish switch is atomic — subscribers always see a consistent state.
set -euo pipefail

SNAPSHOT_NAME="${1:?snapshot-name required}"
COMPONENT="${2:?component required}"
YEAR="${3:?year required}"
DISTRO="${4:?distro required (bookworm|trixie|jammy|noble)}"

PUBLISH_POINT=":${COMPONENT}/${YEAR}"

echo "Publishing snapshot: ${SNAPSHOT_NAME}"
echo "  → publish point: ${PUBLISH_POINT} (distribution: ${DISTRO})"

# Check if this publish point already has a published snapshot
if aptly publish show "${DISTRO}" "${PUBLISH_POINT}" > /dev/null 2>&1; then
  # Atomically switch to new snapshot — subscribers see either old or new, never partial
  echo "Switching published snapshot at ${PUBLISH_POINT} to ${SNAPSHOT_NAME}..."
  aptly publish switch \
    -component="${COMPONENT}" \
    "${DISTRO}" \
    "${PUBLISH_POINT}" \
    "${SNAPSHOT_NAME}"
else
  # First-time publish for this component/year
  echo "Publishing snapshot for the first time at ${PUBLISH_POINT}..."
  aptly publish snapshot \
    -distribution="${DISTRO}" \
    -component="${COMPONENT}" \
    "${SNAPSHOT_NAME}" \
    "${PUBLISH_POINT}"
fi

echo "Published: ${SNAPSHOT_NAME} → ${PUBLISH_POINT}"
