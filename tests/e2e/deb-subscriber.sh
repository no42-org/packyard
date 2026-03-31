#!/usr/bin/env bash
# deb-subscriber.sh — End-to-end DEB subscriber test (Story 5.2)
#
# INFRASTRUCTURE DEPENDENCY:
#   This script requires a running packyard stack with at least one signed DEB
#   published in Aptly. See tests/e2e/README.md for setup instructions.
#
# REQUIRED ENV VARS:
#   BASE_URL   — packyard base URL (e.g. https://pkg.mdn.opennms.com)
#   VALID_KEY  — a valid active subscription key in the auth database
#
# OPTIONAL ENV VARS:
#   COMPONENT  — Meridian component (default: core)
#   YEAR       — Meridian year (default: 2025)
#   DISTRO     — DEB distribution codename (default: bookworm)
#   PACKAGE    — DEB package name to install (default: meridian-core)
#
# USAGE:
#   BASE_URL=https://pkg.mdn.opennms.com VALID_KEY=abc123 bash tests/e2e/deb-subscriber.sh
set -euo pipefail

BASE_URL="${BASE_URL:?BASE_URL is required (e.g. https://pkg.mdn.opennms.com)}"
VALID_KEY="${VALID_KEY:?VALID_KEY is required (a valid subscription key)}"
COMPONENT="${COMPONENT:-core}"
YEAR="${YEAR:-2025}"
DISTRO="${DISTRO:-bookworm}"
PACKAGE="${PACKAGE:-meridian-core}"

APT_CACHE_DIR="$(mktemp -d)"
SOURCES_FILE="$(mktemp --suffix=.list)"
GPG_KEY_FILE="$(mktemp --suffix=.gpg)"
DOWNLOAD_DIR="$(mktemp -d)"
FAILED=0

AUTH_URL="$(echo "${BASE_URL}" | sed 's|://|://subscriber:'"${VALID_KEY}"'@|')"

# ─── Helpers ─────────────────────────────────────────────────────────────────

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; FAILED=1; }

cleanup() {
  rm -f "${SOURCES_FILE}" "${GPG_KEY_FILE}"
  rm -rf "${APT_CACHE_DIR}" "${DOWNLOAD_DIR}"
}
trap cleanup EXIT

for cmd in apt-get dpkg python3 curl gpg; do
  command -v "$cmd" > /dev/null 2>&1 \
    || { echo "ERROR: '$cmd' not found — see tests/e2e/README.md for prerequisites"; exit 1; }
done

# ─── Precondition: dearmor Meridian GPG key ──────────────────────────────────

mkdir -p "${APT_CACHE_DIR}/lists/partial" "${APT_CACHE_DIR}/archives/partial"
curl -fsSL "${BASE_URL}/gpg/meridian.asc" | gpg --dearmor > "${GPG_KEY_FILE}"
echo "Meridian GPG key dearmored to ${GPG_KEY_FILE}."

# ─── Sources file ────────────────────────────────────────────────────────────

cat > "${SOURCES_FILE}" <<LIST
deb [signed-by=${GPG_KEY_FILE}] ${AUTH_URL}/deb/${COMPONENT}/${YEAR}/ ${DISTRO} main
LIST

# Shared apt options — isolate cache and sources from system state
APT_OPTS=(
  "-o" "Dir::Etc::sourcelist=${SOURCES_FILE}"
  "-o" "Dir::Etc::sourcelistd=/dev/null"
  "-o" "Dir::Cache::Dir=${APT_CACHE_DIR}"
  "-o" "Dir::State::Lists=${APT_CACHE_DIR}/lists"
)

# ─── AC1: Happy-path install with GPG verification ───────────────────────────

echo ""
echo "=== AC1: Happy-path install ==="
if DEBIAN_FRONTEND=noninteractive apt-get "${APT_OPTS[@]}" update 2>&1 \
   && DEBIAN_FRONTEND=noninteractive apt-get "${APT_OPTS[@]}" install --assume-yes "${PACKAGE}" 2>&1; then
  pass "AC1 — '${PACKAGE}' installed successfully with GPG verification"
else
  fail "AC1 — apt-get install failed (verify stack is running and package is published)"
fi

# ─── AC2: Tampered package rejection ─────────────────────────────────────────

echo ""
echo "=== AC2: Tampered package rejection ==="
if ( cd "${DOWNLOAD_DIR}" && apt-get "${APT_OPTS[@]}" download "${PACKAGE}" 2>&1 ); then
  DEB_FILE=$(ls "${DOWNLOAD_DIR}"/*.deb 2>/dev/null | head -1 || true)
  if [ -z "${DEB_FILE}" ]; then
    fail "AC2 — no DEB downloaded for tampering test"
  else
    TAMPERED="${DOWNLOAD_DIR}/tampered.deb"
    python3 - "${DEB_FILE}" "${TAMPERED}" <<'EOF'
import sys
data = bytearray(open(sys.argv[1], 'rb').read())
data[4096] ^= 0xFF
open(sys.argv[2], 'wb').write(data)
EOF
    INSTALL_RC=0
    INSTALL_OUT=$(DEBIAN_FRONTEND=noninteractive apt-get "${APT_OPTS[@]}" install --assume-yes "${TAMPERED}" 2>&1) || INSTALL_RC=$?
    if [ "${INSTALL_RC}" -ne 0 ]; then
      pass "AC2 — tampered DEB rejected by apt-get install (exit non-zero): ${INSTALL_OUT}"
    else
      fail "AC2 — apt-get install did NOT reject tampered package (output: ${INSTALL_OUT})"
    fi
  fi
else
  fail "AC2 — apt-get download failed; cannot run tamper test"
fi

# ─── AC3: Invalid key returns 401 ────────────────────────────────────────────

echo ""
echo "=== AC3: Invalid key returns 401 ==="
BAD_AUTH_URL="$(echo "${BASE_URL}" | sed 's|://|://subscriber:invalidkey9999@|')/deb/${COMPONENT}/${YEAR}/dists/${DISTRO}/InRelease"
HTTP_STATUS=$(curl -s -o /dev/null -w '%{http_code}' "${BAD_AUTH_URL}" || true)
if [ "${HTTP_STATUS}" = "401" ]; then
  pass "AC3 — invalid key correctly returns HTTP 401"
else
  fail "AC3 — expected HTTP 401 for invalid key; got HTTP ${HTTP_STATUS}"
fi

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
