#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oci.sh — copy an image into Zot as lts-<component>/<image> and sign it.
#
# Usage: oci.sh <zot-registry> <component> <image> <version> <series>
#
# Environment:
#   SOURCE_REF        required; the image to copy, e.g. quay.io/bluebird/core:38.1.0
#   SOURCE_IDENTITY   optional; when set, `cosign verify` the source against this
#                     certificate identity before copying (upstream must have signed)
#   COSIGN_SIGN       "1" to sign the copied index keylessly with this job's OIDC
#                     identity and verify it against OWN_IDENTITY; anything else skips
#   OWN_IDENTITY      required when COSIGN_SIGN=1
#   OIDC_ISSUER       default https://token.actions.githubusercontent.com
#
# Runs where crane and cosign are installed and the registry is reachable:
# the runner, through the SSH tunnel in production, directly in CI. The copy
# is registry-to-registry by digest, so a multi-arch index stays intact and
# each layer moves once; the digest is compared afterwards. `--insecure`
# because Zot is plain HTTP on loopback in both settings.
set -euo pipefail

ZOT="${1:?zot registry required, e.g. localhost:5000}"
COMPONENT="${2:?component required}"
IMAGE="${3:?image name required}"
VERSION="${4:?version required}"
SERIES="${5:?series required}"
: "${SOURCE_REF:?SOURCE_REF must be set}"
OIDC_ISSUER="${OIDC_ISSUER:-https://token.actions.githubusercontent.com}"

seg='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
for v in "${COMPONENT}" "${IMAGE}" "${VERSION}" "${SERIES}"; do
  [[ "$v" =~ $seg ]] && [[ "$v" != *..* ]] || { echo "ERROR: invalid segment: ${v}" >&2; exit 1; }
done
for cmd in crane cosign; do command -v "$cmd" >/dev/null || { echo "ERROR: $cmd not found" >&2; exit 1; }; done

dst="${ZOT}/lts-${COMPONENT}/${IMAGE}"

if [ -n "${SOURCE_IDENTITY:-}" ]; then
  # Never copy an image the source did not sign.
  cosign verify --certificate-identity "${SOURCE_IDENTITY}" --certificate-oidc-issuer "${OIDC_ISSUER}" "${SOURCE_REF}" > /dev/null
  echo "source signature verified: ${SOURCE_REF}"
fi

crane copy --insecure "${SOURCE_REF}" "${dst}:${VERSION}"
crane tag --insecure "${dst}:${VERSION}" "${SERIES}"
src_digest=$(crane digest "${SOURCE_REF}")
dst_digest=$(crane digest --insecure "${dst}:${VERSION}")
[ "${src_digest}" = "${dst_digest}" ] || { echo "ERROR: digest changed while copying ${IMAGE}: ${src_digest} -> ${dst_digest}" >&2; exit 1; }
echo "copied ${SOURCE_REF} -> ${dst}:${VERSION} (${SERIES}) ${dst_digest}"

if [ "${COSIGN_SIGN:-0}" = "1" ]; then
  : "${OWN_IDENTITY:?OWN_IDENTITY must be set when COSIGN_SIGN=1}"
  cosign sign --yes --allow-insecure-registry "${dst}@${dst_digest}"
  cosign verify --allow-insecure-registry --certificate-identity "${OWN_IDENTITY}" --certificate-oidc-issuer "${OIDC_ISSUER}" "${dst}:${SERIES}" > /dev/null
  echo "signed ${dst}@${dst_digest} as ${OWN_IDENTITY}"
fi
