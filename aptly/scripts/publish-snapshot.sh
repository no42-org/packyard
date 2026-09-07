#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
# publish-snapshot.sh — publish or atomically switch an Aptly snapshot
# Usage: publish-snapshot.sh <snapshot-name> <component> <series> <distro>
# Example: publish-snapshot.sh core-2025-20260329T120000Z core 2025 bookworm
#
# Publish path: :{component}/{series}
# Maps to subscriber URL: https://pkg.example.org/deb/{component}/{series}/
# Aptly's publish switch is atomic — subscribers always see a consistent state.
set -euo pipefail

SNAPSHOT_NAME="${1:?snapshot-name required}"
COMPONENT="${2:?component required}"
SERIES="${3:?series required}"
DISTRO="${4:?distro required (bookworm|trixie|jammy|noble)}"

PUBLISH_POINT=":${COMPONENT}/${SERIES}"

# Signing: aptly signs Release with the default secret key unless told
# otherwise. Promotion workflows import the key into this container and pass
# its id and a passphrase file through the environment.
SIGN_ARGS=()
[ -n "${GPG_KEY_ID:-}" ] && SIGN_ARGS+=("-gpg-key=${GPG_KEY_ID}")
[ -n "${GPG_PASSPHRASE_FILE:-}" ] && SIGN_ARGS+=("-passphrase-file=${GPG_PASSPHRASE_FILE}")
[ "${APTLY_SKIP_SIGNING:-}" = "1" ] && SIGN_ARGS+=("-skip-signing")

echo "Publishing snapshot: ${SNAPSHOT_NAME}"
echo "  → publish point: ${PUBLISH_POINT} (distribution: ${DISTRO})"

# Check if this publish point already has a published snapshot
if aptly publish show "${DISTRO}" "${PUBLISH_POINT}" > /dev/null 2>&1; then
  # Atomically switch to new snapshot — subscribers see either old or new, never partial
  echo "Switching published snapshot at ${PUBLISH_POINT} to ${SNAPSHOT_NAME}..."
  aptly publish switch \
    "${SIGN_ARGS[@]}" \
    -component="${COMPONENT}" \
    "${DISTRO}" \
    "${PUBLISH_POINT}" \
    "${SNAPSHOT_NAME}"
else
  # First-time publish for this component/series
  echo "Publishing snapshot for the first time at ${PUBLISH_POINT}..."
  aptly publish snapshot \
    "${SIGN_ARGS[@]}" \
    -distribution="${DISTRO}" \
    -component="${COMPONENT}" \
    "${SNAPSHOT_NAME}" \
    "${PUBLISH_POINT}"
fi

echo "Published: ${SNAPSHOT_NAME} → ${PUBLISH_POINT}"
