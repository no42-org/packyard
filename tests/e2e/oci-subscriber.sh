#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# oci-subscriber.sh — End-to-end OCI subscriber test (Story 5.3)
#
# INFRASTRUCTURE DEPENDENCY:
#   A running packyard stack with a multi-arch image published under a public
#   component (in CI: scripts/ci/publish-fixtures.sh), and a private component
#   for the 401 check. See tests/e2e/README.md.
#
# OCI is anonymous-only by design (#221): docker clients cannot authenticate
# to private components until forward-auth issues a registry challenge, so
# this test never runs `docker login`. Public images are pulled anonymously
# through the /v2/ router; the private component answers 401 to curl.
#
# REQUIRED ENV VARS:
#   BASE_URL   — packyard base URL (https://pkg.example.org, or http://localhost in CI)
#
# OPTIONAL ENV VARS:
#   COMPONENT           — public component (default: core)
#   SERIES              — series tag to pull (default: 2025)
#   OCI_REGISTRY        — registry reference for docker/crane (default: BASE_URL host)
#   OCI_IMAGE           — image name under <component>/ (default: unset, the
#                         legacy single-segment <component>:<series> reference)
#   PRIVATE_COMPONENT   — a private component for the 401 check (default: minion)
#   COSIGN_CERT_IDENTITY_REGEXP — signing identity to verify (default: promote-release on main)
#   COSIGN_SKIP         — "1" skips the signature check (pull requests in CI, where
#                         the fixture is not signed)
#   VALID_KEY           — accepted for symmetry with the other tests; unused
#
# USAGE:
#   BASE_URL=https://pkg.example.org bash tests/e2e/oci-subscriber.sh
set -euo pipefail

BASE_URL="${BASE_URL:?BASE_URL is required (e.g. https://pkg.example.org)}"
COMPONENT="${COMPONENT:-core}"
SERIES="${SERIES:-2025}"
PRIVATE_COMPONENT="${PRIVATE_COMPONENT:-minion}"

# Strip the scheme: docker and crane use bare registry references. OCI_REGISTRY
# overrides the derived host; CI needs "localhost:80", because crane reads a
# bare "localhost" as a Docker Hub namespace, and both clients treat localhost
# with any port as an insecure (plain HTTP) registry, which is what CI serves.
REGISTRY="${BASE_URL#https://}"; REGISTRY="${REGISTRY#http://}"
REGISTRY="${OCI_REGISTRY:-${REGISTRY}}"
if [ -n "${OCI_IMAGE:-}" ]; then
  REPO_PATH="${COMPONENT}/${OCI_IMAGE}"
else
  REPO_PATH="${COMPONENT}"
fi
IMAGE="${REGISTRY}/oci/${REPO_PATH}:${SERIES}"
# crane needs to be told a plain-HTTP registry is intended; docker infers it for localhost.
CRANE_OPTS=()
case "${REGISTRY}" in localhost*|127.0.0.1*) CRANE_OPTS=(--insecure) ;; esac

FAILED=0
pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; FAILED=1; }
cleanup() { docker rmi "${IMAGE}" > /dev/null 2>&1 || true; }
trap cleanup EXIT

for cmd in docker crane cosign curl jq; do
  command -v "$cmd" > /dev/null 2>&1 \
    || { echo "ERROR: '$cmd' not found — see tests/e2e/README.md for prerequisites"; exit 1; }
done

# ─── AC1: Anonymous pull of a public image succeeds ──────────────────────────

echo ""
echo "=== AC1: Anonymous pull succeeds (public component) ==="
PULL_RC=0
PULL_OUT=$(docker pull "${IMAGE}" 2>&1) || PULL_RC=$?
if [ "${PULL_RC}" -eq 0 ] && docker image inspect "${IMAGE}" > /dev/null 2>&1; then
  pass "AC1 — '${IMAGE}' pulled without credentials"
else
  fail "AC1 — docker pull failed (is the stack up and the image published?): ${PULL_OUT}"
fi

# ─── AC2: Multi-arch index resolution ────────────────────────────────────────

echo ""
echo "=== AC2: Multi-arch index resolution ==="
MANIFEST_RC=0
MANIFEST_JSON=$(crane manifest "${CRANE_OPTS[@]}" "${IMAGE}" 2>&1) || MANIFEST_RC=$?
if [ "${MANIFEST_RC}" -ne 0 ]; then
  fail "AC2 — crane manifest failed: ${MANIFEST_JSON}"
else
  MEDIA_TYPE=$(echo "${MANIFEST_JSON}" | jq -r '.mediaType // empty' 2>/dev/null || true)
  if echo "${MEDIA_TYPE}" | grep -q "image.index"; then
    PLATFORMS=$(echo "${MANIFEST_JSON}" | jq -r '.manifests[].platform.architecture' 2>/dev/null || true)
    if echo "${PLATFORMS}" | grep -q "amd64" && echo "${PLATFORMS}" | grep -q "arm64"; then
      pass "AC2 — OCI image index confirmed with amd64 and arm64 manifests"
    else
      fail "AC2 — expected amd64 and arm64 in index; found: $(echo "${PLATFORMS}" | tr '\n' ',')"
    fi
  else
    fail "AC2 — manifest is not an OCI image index (mediaType: ${MEDIA_TYPE})"
  fi
fi

# ─── AC3: Auth middleware order and private-component 401 ────────────────────

echo ""
echo "=== AC3: Auth middleware order and 401 check ==="

# AC3a used to grep the auth logs for "/oci/v2/", but the auth service logs
# its own request path (/auth), not the forwarded URI, so that could never
# match. The middleware order is proven by AC3b instead: a private
# component can only answer 401 on the /v2/oci/... shape if forward-auth
# saw the rewritten, unstripped /oci/v2/<component>/ path.
# AC3b — a private component answers 401 to an invalid key, on both path shapes
for path in "oci/v2/${PRIVATE_COMPONENT}/manifests/${SERIES}" "v2/oci/${PRIVATE_COMPONENT}/manifests/${SERIES}"; do
  HTTP_STATUS=$(curl -s -o /dev/null -w '%{http_code}' -u "subscriber:invalidkey9999" "${BASE_URL}/${path}" || true)
  if [ "${HTTP_STATUS}" = "401" ]; then
    pass "AC3b — invalid key returns 401 on /${path}"
  else
    fail "AC3b — expected 401 for an invalid key on /${path}; got ${HTTP_STATUS}"
  fi
done

# ─── AC4: Keyless cosign verification ────────────────────────────────────────

echo ""
echo "=== AC4: Keyless cosign verification ==="
if [ "${COSIGN_SKIP:-0}" = "1" ]; then
  echo "SKIP: AC4 — COSIGN_SKIP=1 (the image was not signed in this run)"
else
  COSIGN_CERT_IDENTITY_REGEXP="${COSIGN_CERT_IDENTITY_REGEXP:-https://github.com/no42-org/packyard/\\.github/workflows/promote-release\\.yml@refs/heads/main}"
  COSIGN_RC=0
  COSIGN_OUT=$(cosign verify \
    --certificate-identity-regexp "${COSIGN_CERT_IDENTITY_REGEXP}" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "${IMAGE}" 2>&1) || COSIGN_RC=$?
  if [ "${COSIGN_RC}" -eq 0 ]; then
    pass "AC4 — keyless cosign verification succeeded (signature co-located in Zot)"
  else
    fail "AC4 — cosign verify failed: ${COSIGN_OUT}"
  fi
fi

# ─── AC6: Anonymous writes are refused ───────────────────────────────────────
# Forward-auth allows any method on a public component and Zot enforces
# nothing itself; the edge must refuse write methods or anyone can overwrite
# the tags subscribers pull. Never 2xx.

echo ""
echo "=== AC6: Anonymous writes to a public component are refused ==="
for probe in "POST oci/v2/${REPO_PATH}/blobs/uploads/" "POST v2/oci/${REPO_PATH}/blobs/uploads/" "PUT oci/v2/${REPO_PATH}/manifests/probe"; do
  METHOD="${probe%% *}"; P="${probe#* }"
  HTTP_STATUS=$(curl -s -o /dev/null -w '%{http_code}' -X "${METHOD}" -H 'Content-Type: application/vnd.oci.image.manifest.v1+json' --data '{}' "${BASE_URL}/${P}" || true)
  case "${HTTP_STATUS}" in
    404|405) pass "AC6 — ${METHOD} /${P} refused with ${HTTP_STATUS}" ;;
    *) fail "AC6 — ${METHOD} /${P} answered ${HTTP_STATUS}; anonymous writes must be refused" ;;
  esac
done

# ─── Summary ─────────────────────────────────────────────────────────────────

echo ""
echo "=================================="
if [ "${FAILED}" -eq 0 ]; then
  echo "ALL TESTS PASSED"
  exit 0
else
  echo "SOME TESTS FAILED — review output above"
  exit 1
fi
