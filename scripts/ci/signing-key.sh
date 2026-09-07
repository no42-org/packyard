#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# signing-key.sh — an ephemeral GPG signing key for one CI run.
#
# Production serves a real key from the deployment (the repository's copy of
# static/content/gpg/lts.asc is a placeholder). CI mirrors that shape: this
# script generates a passphrase-protected key, and writes under ci/ (ignored
# by git):
#   ci/lts.asc      public key, bind-mounted over the static placeholder by
#                   compose.override.ci.yml so /gpg/lts.asc serves it
#   ci/key.asc      ASCII-armored private key, for rpmsign and deb.sh
#   ci/passphrase   one line
#   ci/keyid        the 40-hex fingerprint, the same value the workflows keep
#                   in the GPG_KEY_ID secret
# Nothing to store, nothing to rotate; a new run gets a new key. Idempotent:
# an existing ci/keyid is kept, so `make ci-stack-up` can depend on this.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUT="${REPO_ROOT}/ci"
if [ -s "${OUT}/keyid" ] && [ -s "${OUT}/lts.asc" ]; then
  echo "signing key present: $(cat "${OUT}/keyid")"
  exit 0
fi
command -v gpg >/dev/null || { echo "ERROR: gpg not found" >&2; exit 1; }
mkdir -p "${OUT}"
export GNUPGHOME="${OUT}/gnupg"
rm -rf "${GNUPGHOME}"; mkdir -p "${GNUPGHOME}"; chmod 700 "${GNUPGHOME}"
PASS=$(head -c 24 /dev/urandom | base64 | tr -d '/+=' | head -c 24)
gpg --batch --quiet --passphrase "${PASS}" --quick-gen-key "Packyard CI (ephemeral) <ci@packyard.invalid>" default default never
KEYID=$(gpg --list-keys --with-colons | awk -F: '/^fpr/{print $10; exit}')
umask 077
printf '%s\n' "${PASS}" > "${OUT}/passphrase"
gpg --batch --passphrase "${PASS}" --pinentry-mode loopback --armor --export-secret-keys "${KEYID}" > "${OUT}/key.asc"
umask 022
gpg --armor --export "${KEYID}" > "${OUT}/lts.asc"
printf '%s\n' "${KEYID}" > "${OUT}/keyid"
if [ -n "${GITHUB_ENV:-}" ]; then
  echo "GPG_KEY_ID=${KEYID}" >> "${GITHUB_ENV}"
fi
echo "generated ephemeral signing key ${KEYID} under ci/"
