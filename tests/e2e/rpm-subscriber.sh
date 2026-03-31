#!/usr/bin/env bash
# rpm-subscriber.sh — End-to-end RPM subscriber test (Story 5.1)
#
# INFRASTRUCTURE DEPENDENCY:
#   This script requires a running packyard stack with at least one signed RPM published.
#   See tests/e2e/README.md for setup instructions before running.
#
# REQUIRED ENV VARS:
#   BASE_URL   — packyard base URL (e.g. https://pkg.mdn.opennms.com)
#   VALID_KEY  — a valid active subscription key in the auth database
#
# OPTIONAL ENV VARS:
#   COMPONENT  — Meridian component (default: core)
#   YEAR       — Meridian year (default: 2025)
#   OS_ARCH    — RPM OS/arch path segment (default: el9-x86_64)
#   PACKAGE    — RPM package name to install (default: meridian-core)
#
# USAGE:
#   BASE_URL=https://pkg.mdn.opennms.com VALID_KEY=abc123 bash tests/e2e/rpm-subscriber.sh
set -euo pipefail

BASE_URL="${BASE_URL:?BASE_URL is required (e.g. https://pkg.mdn.opennms.com)}"
VALID_KEY="${VALID_KEY:?VALID_KEY is required (a valid subscription key)}"
COMPONENT="${COMPONENT:-core}"
YEAR="${YEAR:-2025}"
OS_ARCH="${OS_ARCH:-el9-x86_64}"
PACKAGE="${PACKAGE:-meridian-core}"

TEST_ROOT="$(mktemp -d)"
REPO_FILE="$(mktemp --suffix=.repo)"
BAD_REPO_FILE="$(mktemp --suffix=.repo)"
DOWNLOAD_DIR="$(mktemp -d)"
FAILED=0

# Inject credentials into base URL for the repo baseurl
AUTH_URL="$(echo "${BASE_URL}" | sed 's|://|://subscriber:'"${VALID_KEY}"'@|')"

# ─── Helpers ─────────────────────────────────────────────────────────────────

pass() { echo "PASS: $*"; }
fail() { echo "FAIL: $*" >&2; FAILED=1; }

cleanup() {
  rm -f "${REPO_FILE}" "${BAD_REPO_FILE}"
  rm -rf "${TEST_ROOT}" "${DOWNLOAD_DIR}"
}
trap cleanup EXIT

for cmd in dnf rpm python3; do
  command -v "$cmd" > /dev/null 2>&1 \
    || { echo "ERROR: '$cmd' not found — see tests/e2e/README.md for prerequisites"; exit 1; }
done

# ─── Repo files ──────────────────────────────────────────────────────────────

cat > "${REPO_FILE}" <<REPO
[meridian-test]
name=Meridian Test Repo
baseurl=${AUTH_URL}/rpm/${COMPONENT}/${YEAR}/${OS_ARCH}/
enabled=1
gpgcheck=1
gpgkey=${BASE_URL}/gpg/meridian.asc
REPO

cat > "${BAD_REPO_FILE}" <<REPO
[meridian-bad]
name=Meridian Bad Key Test
baseurl=$(echo "${BASE_URL}" | sed 's|://|://subscriber:invalidkey9999@|')/rpm/${COMPONENT}/${YEAR}/${OS_ARCH}/
enabled=1
gpgcheck=1
gpgkey=${BASE_URL}/gpg/meridian.asc
REPO

# ─── Precondition: import Meridian GPG key into test installroot ─────────────

rpm --root "${TEST_ROOT}" --initdb
rpm --root "${TEST_ROOT}" --import "${BASE_URL}/gpg/meridian.asc"
echo "Meridian GPG key imported into test installroot."

# ─── AC1: Happy-path install with GPG verification ───────────────────────────

echo ""
echo "=== AC1: Happy-path install ==="
if dnf install \
    --installroot "${TEST_ROOT}" \
    --config "${REPO_FILE}" \
    --disablerepo='*' \
    --enablerepo=meridian-test \
    --assumeyes \
    "${PACKAGE}" 2>&1; then
  pass "AC1 — '${PACKAGE}' installed successfully with GPG verification"
else
  fail "AC1 — dnf install failed (verify stack is running and package is published)"
fi

# ─── AC2: Tampered package rejection ─────────────────────────────────────────

echo ""
echo "=== AC2: Tampered package rejection ==="
mkdir -p "${DOWNLOAD_DIR}"
if dnf download \
    --config "${REPO_FILE}" \
    --disablerepo='*' \
    --enablerepo=meridian-test \
    --destdir "${DOWNLOAD_DIR}" \
    "${PACKAGE}" 2>&1; then

  RPM_FILE=$(ls "${DOWNLOAD_DIR}/"*.rpm 2>/dev/null | head -1 || true)
  if [ -z "${RPM_FILE}" ]; then
    fail "AC2 — no RPM downloaded for tampering test"
  else
    TAMPERED="${DOWNLOAD_DIR}/tampered.rpm"
    python3 - "${RPM_FILE}" "${TAMPERED}" <<'EOF'
import sys
data = bytearray(open(sys.argv[1], 'rb').read())
data[1024] ^= 0xFF
open(sys.argv[2], 'wb').write(data)
EOF
    INSTALL_OUT=$(rpm -i --root "${TEST_ROOT}" --test "${TAMPERED}" 2>&1 || true)
    if ! rpm -i --root "${TEST_ROOT}" --test "${TAMPERED}" > /dev/null 2>&1; then
      pass "AC2 — tampered RPM rejected by rpm install test (exit non-zero): ${INSTALL_OUT}"
    else
      fail "AC2 — rpm install test did NOT reject tampered package (output: ${INSTALL_OUT})"
    fi
  fi
else
  fail "AC2 — dnf download failed; cannot run tamper test"
fi

# ─── AC3: Invalid key returns 401 ────────────────────────────────────────────

echo ""
echo "=== AC3: Invalid key returns 401 ==="
BAD_AUTH_URL="$(echo "${BASE_URL}" | sed 's|://|://subscriber:invalidkey9999@|')/rpm/${COMPONENT}/${YEAR}/${OS_ARCH}/repodata/repomd.xml"
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
