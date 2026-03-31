#!/usr/bin/env bash
# oci-subscriber.sh — End-to-end OCI subscriber test (Story 5.3)
#
# INFRASTRUCTURE DEPENDENCY:
#   This script requires a running packyard stack with at least one cosign-signed
#   OCI image index (multi-arch) published to Zot. See tests/e2e/README.md for
#   setup instructions.
#
# REQUIRED ENV VARS:
#   BASE_URL   — packyard base URL (e.g. https://pkg.mdn.opennms.com)
#   VALID_KEY  — a valid active subscription key in the auth database
#
# OPTIONAL ENV VARS:
#   COMPONENT  — Meridian component (default: core)
#   YEAR       — Meridian year (default: 2025)
#
# USAGE:
#   BASE_URL=https://pkg.mdn.opennms.com VALID_KEY=abc123 bash tests/e2e/oci-subscriber.sh
set -euo pipefail

BASE_URL="${BASE_URL:?BASE_URL is required (e.g. https://pkg.mdn.opennms.com)}"
VALID_KEY="${VALID_KEY:?VALID_KEY is required (a valid subscription key)}"
COMPONENT="${COMPONENT:-core}"
YEAR="${YEAR:-2025}"

# Strip scheme — docker/crane use bare registry references
REGISTRY="${BASE_URL#https://}"
IMAGE="${REGISTRY}/oci/meridian-${COMPONENT}:${YEAR}"

COSIGN_PUB="$(mktemp --suffix=.pub)"
FAILED=0

# ─── Helpers ─────────────────────────────────────────────────────────────────

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; FAILED=1; }

cleanup() {
  docker logout "${REGISTRY}/oci" > /dev/null 2>&1 || true
  docker rmi "${IMAGE}" > /dev/null 2>&1 || true
  rm -f "${COSIGN_PUB}"
}
trap cleanup EXIT

for cmd in docker crane cosign curl jq; do
  command -v "$cmd" > /dev/null 2>&1 \
    || { echo "ERROR: '$cmd' not found — see tests/e2e/README.md for prerequisites"; exit 1; }
done

# ─── Precondition: docker login ──────────────────────────────────────────────

echo "${VALID_KEY}" | docker login "${REGISTRY}/oci" --username subscriber --password-stdin
echo "Docker login to ${REGISTRY}/oci successful."

# ─── AC1: Authenticated pull succeeds ────────────────────────────────────────

echo ""
echo "=== AC1: Authenticated pull succeeds ==="
PULL_RC=0
PULL_OUT=$(docker pull "${IMAGE}" 2>&1) || PULL_RC=$?
if [ "${PULL_RC}" -eq 0 ]; then
  if docker image inspect "${IMAGE}" > /dev/null 2>&1; then
    pass "AC1 — '${IMAGE}' pulled successfully and present in local images"
  else
    fail "AC1 — docker pull exited 0 but image not found via docker image inspect"
  fi
else
  fail "AC1 — docker pull failed (verify stack is running and image is published): ${PULL_OUT}"
fi

# ─── AC2: Multi-arch index resolution ────────────────────────────────────────

echo ""
echo "=== AC2: Multi-arch index resolution ==="
MANIFEST_RC=0
MANIFEST_JSON=$(crane manifest "${IMAGE}" 2>&1) || MANIFEST_RC=$?
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

# ─── AC3: Auth middleware order and invalid-key 401 ──────────────────────────

echo ""
echo "=== AC3: Auth middleware order and 401 check ==="

# AC3a — forwardAuth sees full /oci/v2/ path (not stripped /v2/) in auth logs
AUTH_CONTAINER=$(docker ps --filter "label=com.docker.compose.service=auth" --filter "status=running" \
  --format "{{.Names}}" 2>/dev/null | head -1 || true)
if [ -n "${AUTH_CONTAINER}" ]; then
  AUTH_LOGS=$(docker logs "${AUTH_CONTAINER}" --tail 20 2>&1 || true)
  if echo "${AUTH_LOGS}" | grep -qE '/oci/v2/'; then
    pass "AC3a — auth service received full /oci/v2/ path (forwardAuth fires before stripPrefix)"
  else
    fail "AC3a — /oci/v2/ not found in auth logs (last 20 lines); middleware order may be incorrect"
  fi
else
  fail "AC3a — no running container matching 'auth' found via docker ps"
fi

# AC3b — curl exact HTTP 401 for invalid key
HTTP_STATUS=$(curl -s -o /dev/null -w '%{http_code}' \
  -u "subscriber:invalidkey9999" \
  "${BASE_URL}/oci/v2/meridian-${COMPONENT}/manifests/${YEAR}" || true)
if [ "${HTTP_STATUS}" = "401" ]; then
  pass "AC3b — invalid key correctly returns HTTP 401 on OCI manifest endpoint"
else
  fail "AC3b — expected HTTP 401 for invalid key; got HTTP ${HTTP_STATUS}"
fi

# ─── AC4: Offline cosign verification ────────────────────────────────────────

echo ""
echo "=== AC4: Offline cosign verification ==="
curl -fsSL "${BASE_URL}/gpg/cosign.pub" -o "${COSIGN_PUB}"
COSIGN_RC=0
COSIGN_OUT=$(cosign verify \
  --key "${COSIGN_PUB}" \
  --insecure-ignore-tlog \
  "${IMAGE}" 2>&1) || COSIGN_RC=$?
if [ "${COSIGN_RC}" -eq 0 ]; then
  pass "AC4 — offline cosign verification succeeded (signature co-located in Zot)"
else
  fail "AC4 — cosign verify failed: ${COSIGN_OUT}"
fi

# ─── AC5: Invalid key returns 401 ────────────────────────────────────────────
# (AC5 is covered by AC3b above — same mechanism, reported together)

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
