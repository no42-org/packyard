#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# publish-fixtures.sh — put one RPM, one DEB and one multi-arch image into
# the CI stack through the same scripts production promotions use.
#
# Preconditions: the CI stack is up, `make ci-seed` has provisioned the
# `core` component (2025 / el9 / x86_64), and scripts/ci/signing-key.sh has
# written ci/. Tools on the runner: docker, rpmsign (package `rpm`), crane,
# cosign, gpg.
#
# Environment:
#   COMPOSE_FILE_MAIN   compose file handed to scripts/publish (default compose.yml;
#                       COMPOSE_PROJECT_NAME selects the running project)
#   COSIGN_SIGN         "1" to sign the image keylessly (push and schedule runs)
#   OWN_IDENTITY        cosign identity to verify the signature against when signing
#   ZOT_REGISTRY        default 127.0.0.1:5000 (compose.yml publishes Zot on loopback)
#   FIXTURE_IMAGE_REF   default docker.io/library/busybox@<pinned multi-arch digest>
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE_FILE_MAIN="${COMPOSE_FILE_MAIN:-${REPO_ROOT}/compose.yml}"
ZOT_REGISTRY="${ZOT_REGISTRY:-127.0.0.1:5000}"
# busybox 1.37 image index: amd64, arm64 and five more platforms. Pinned by
# digest so the fixture cannot change under the test.
FIXTURE_IMAGE_REF="${FIXTURE_IMAGE_REF:-docker.io/library/busybox@sha256:9db7b59979c38555a39def84a31fb98b5296952f9e3afd4f6f11f05b07adfab0}"
NFPM_IMAGE="goreleaser/nfpm:v2.44.0"
COMPONENT=core; SERIES=2025; RPM_TARGET=el9-x86_64; DEB_DISTRO=bookworm; IMAGE=fixture

for cmd in docker rpmsign crane cosign gpg tar; do command -v "$cmd" >/dev/null || { echo "ERROR: $cmd not found" >&2; exit 1; }; done
for f in keyid key.asc passphrase; do [ -s "${REPO_ROOT}/ci/${f}" ] || { echo "ERROR: ci/${f} missing; run scripts/ci/signing-key.sh" >&2; exit 1; }; done
KEYID=$(cat "${REPO_ROOT}/ci/keyid")
WORK=$(mktemp -d); trap 'rm -rf "${WORK}"' EXIT
export COPYFILE_DISABLE=1

echo "== fixtures: nfpm builds the RPM and the DEB from one config"
mkdir -p "${WORK}/pkg"; cp "${REPO_ROOT}/tests/rpm/fixtures/nfpm.yaml" "${WORK}/pkg/"; echo hello > "${WORK}/pkg/hello.txt"
docker run --rm -v "${WORK}/pkg:/work" -w /work "${NFPM_IMAGE}" package --packager rpm --target /work/ >/dev/null
docker run --rm -v "${WORK}/pkg:/work" -w /work "${NFPM_IMAGE}" package --packager deb --target /work/ >/dev/null
RPM=$(ls "${WORK}"/pkg/*.rpm); DEB=$(ls "${WORK}"/pkg/*.deb)
echo "   $(basename "${RPM}")  $(basename "${DEB}")"

echo "== sign the RPM with the ephemeral key (rpmsign, loopback passphrase)"
export GNUPGHOME="${WORK}/gnupg"; mkdir -p "${GNUPGHOME}"; chmod 700 "${GNUPGHOME}"
printf 'pinentry-mode loopback\n' > "${GNUPGHOME}/gpg.conf"; printf 'allow-loopback-pinentry\n' > "${GNUPGHOME}/gpg-agent.conf"
gpg --batch --quiet --import "${REPO_ROOT}/ci/key.asc"
gpg --batch --yes --passphrase-file "${REPO_ROOT}/ci/passphrase" -u "${KEYID}" --sign --output /dev/null - </dev/null
rpmsign --addsign --define "_gpg_name ${KEYID}" --define "_gpg_path ${GNUPGHOME}" "${RPM}" >/dev/null
rpm -K "${RPM}" | grep -qiE "signatures? OK|pgp" || { echo "ERROR: RPM is not signed" >&2; exit 1; }

echo "== rpm.sh -> ${COMPONENT}/${SERIES}/${RPM_TARGET}"
tar -cf - -C "${WORK}/pkg" "$(basename "${RPM}")" | bash "${REPO_ROOT}/scripts/publish/rpm.sh" "${COMPOSE_FILE_MAIN}" "${COMPONENT}" "${SERIES}" "${RPM_TARGET}"

echo "== deb.sh -> ${COMPONENT}/${SERIES} ${DEB_DISTRO}"
mkdir -p "${WORK}/bundle/packages" "${WORK}/bundle/signing"
cp "${DEB}" "${WORK}/bundle/packages/"; cp "${REPO_ROOT}/ci/key.asc" "${WORK}/bundle/signing/key.asc"; cp "${REPO_ROOT}/ci/passphrase" "${WORK}/bundle/signing/passphrase"
tar -cf - -C "${WORK}/bundle" . | bash "${REPO_ROOT}/scripts/publish/deb.sh" "${COMPOSE_FILE_MAIN}" "${COMPONENT}" "${SERIES}" "${KEYID}" "${DEB_DISTRO}"

echo "== oci.sh -> lts-${COMPONENT}/${IMAGE}:${SERIES} (COSIGN_SIGN=${COSIGN_SIGN:-0})"
SOURCE_REF="${FIXTURE_IMAGE_REF}" COSIGN_SIGN="${COSIGN_SIGN:-0}" OWN_IDENTITY="${OWN_IDENTITY:-}" \
  bash "${REPO_ROOT}/scripts/publish/oci.sh" "${ZOT_REGISTRY}" "${COMPONENT}" "${IMAGE}" "${SERIES}" "${SERIES}"
echo "fixtures published"
