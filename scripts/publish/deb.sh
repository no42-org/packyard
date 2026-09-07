#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# deb.sh — publish DEBs through the aptly container of a compose project.
#
# Usage: deb.sh <compose.yml> <component> <series> <gpg-key-id> <distro> [<distro>...] < bundle.tar
#
# The tar on standard input carries:
#   packages/*.deb          the packages
#   signing/key.asc         the ASCII-armored GPG private key aptly signs with
#   signing/passphrase      its passphrase, one line
#
# Secrets travel inside the stream on purpose: the workflows run this script
# over SSH, which passes neither environment nor safe arguments, and the tar
# is extracted straight into the aptly container's /tmp tmpfs, so nothing
# touches the host filesystem. The container's root is read-only, which is
# also why docker cp is not used.
#
# apt verifies the repository's InRelease, not individual packages, so aptly
# signs inside its container: the key is imported into the tmpfs home with
# loopback pinentry (gpg reads a passphrase file only in that mode), one
# snapshot is created, published to every distribution, InRelease is checked
# per distribution, old snapshots are pruned, and the key material and the
# agent are removed on exit whether or not publishing succeeded.
set -euo pipefail

COMPOSE="${1:?compose.yml path required}"
COMPONENT="${2:?component required}"
SERIES="${3:?series required}"
GPG_KEY_ID="${4:?gpg key id required}"
shift 4
[ "$#" -ge 1 ] || { echo "ERROR: at least one distribution required" >&2; exit 1; }

seg='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
for v in "${COMPONENT}" "${SERIES}" "$@"; do
  [[ "$v" =~ $seg ]] && [[ "$v" != *..* ]] || { echo "ERROR: invalid path segment: ${v}" >&2; exit 1; }
done
[[ "${GPG_KEY_ID}" =~ ^[0-9A-Fa-f]{16,40}$ ]] || { echo "ERROR: gpg key id must be 16 to 40 hex characters" >&2; exit 1; }
[ -f "${COMPOSE}" ] || { echo "ERROR: compose file not found: ${COMPOSE}" >&2; exit 1; }

in_service() { docker compose -f "${COMPOSE}" exec -T "$@"; }

cleanup() {
  in_service aptly sh -c 'gpgconf --kill gpg-agent 2>/dev/null; rm -rf /tmp/deb-stage /tmp/gpg-pass /root/.gnupg' >/dev/null 2>&1 || true
}
trap cleanup EXIT

# Extract the bundle into the container and set up signing from it.
in_service aptly sh -c '
  set -e
  rm -rf /tmp/deb-stage /tmp/gpg-pass /root/.gnupg
  mkdir -p /tmp/deb-stage
  tar -xf - --no-same-owner -C /tmp/deb-stage
  [ -f /tmp/deb-stage/signing/key.asc ] || { echo "ERROR: bundle has no signing/key.asc" >&2; exit 1; }
  [ -f /tmp/deb-stage/signing/passphrase ] || { echo "ERROR: bundle has no signing/passphrase" >&2; exit 1; }
  ls /tmp/deb-stage/packages/*.deb >/dev/null 2>&1 || { echo "ERROR: bundle has no packages/*.deb" >&2; exit 1; }
  mkdir -p /root/.gnupg && chmod 700 /root/.gnupg
  printf "pinentry-mode loopback\n" > /root/.gnupg/gpg.conf
  printf "allow-loopback-pinentry\n" > /root/.gnupg/gpg-agent.conf
  gpg --batch --quiet --import /tmp/deb-stage/signing/key.asc
  umask 077; cp /tmp/deb-stage/signing/passphrase /tmp/gpg-pass
  rm -rf /tmp/deb-stage/signing
'

# One snapshot, published to every distribution. The create script takes a
# distro argument it only echoes, so the first one serves.
snap=$(in_service -e DEB_STAGE_DIR=/tmp/deb-stage/packages aptly /scripts/create-snapshot.sh "${COMPONENT}" "${SERIES}" "$1")
[ -n "${snap}" ] || { echo "ERROR: snapshot name is empty" >&2; exit 1; }
echo "snapshot: ${snap}"
for d in "$@"; do
  in_service -e GPG_KEY_ID="${GPG_KEY_ID}" -e GPG_PASSPHRASE_FILE=/tmp/gpg-pass aptly \
    /scripts/publish-snapshot.sh "${snap}" "${COMPONENT}" "${SERIES}" "${d}"
  in_service aptly test -f "/opt/aptly/public/${COMPONENT}/${SERIES}/dists/${d}/InRelease" \
    || { echo "ERROR: InRelease missing for ${d}" >&2; exit 1; }
  echo "published ${COMPONENT}/${SERIES} ${d}"
done
in_service aptly /scripts/prune-snapshots.sh "${COMPONENT}" "${SERIES}"
