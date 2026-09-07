#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# add-package-test.sh — publish a fixture RPM into two OS targets inside the
# rpm image under the same conditions as production: the container runs as
# uid 101 with every capability dropped and a read-only root filesystem, the
# component directories are created by a capability-less root:101 process
# (what the auth service is, with group_add), and the publish exec runs as
# root:101. Checks that the bytes are stored once and each target has its
# own repodata. Needs Docker. Run via `make test-rpm-publish`.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
IMAGE="packyard-rpm-test:local"
NFPM_IMAGE="goreleaser/nfpm:v2.44.0"
NAME="packyard-rpm-test-$$"
VOL="${NAME}-data"
cleanup() { docker rm -f "${NAME}" >/dev/null 2>&1 || true; docker volume rm "${VOL}" >/dev/null 2>&1 || true; rm -rf "${WORK}"; }
trap cleanup EXIT

echo "== build rpm image"
docker build -q -t "${IMAGE}" "${REPO_ROOT}/rpm" > /dev/null

echo "== build fixture RPM with nfpm"
cp "${REPO_ROOT}/tests/rpm/fixtures/nfpm.yaml" "${WORK}/"
echo "hello" > "${WORK}/hello.txt"
docker run --rm -v "${WORK}:/work" -w /work "${NFPM_IMAGE}" package --packager rpm --target /work/ > /dev/null
echo "   $(ls "${WORK}"/*.rpm | xargs -n1 basename)"

echo "== start the rpm container hardened like compose.yml"
docker volume create "${VOL}" > /dev/null
docker run -d --name "${NAME}" \
  --user 101:101 --cap-drop ALL --security-opt no-new-privileges:true --read-only \
  --tmpfs /run:uid=101,gid=101 --tmpfs /var/cache/nginx:uid=101,gid=101 --tmpfs /tmp \
  -v "${VOL}:/usr/share/nginx/html" -v "${WORK}:/stage:ro" \
  "${IMAGE}" > /dev/null
sleep 2
docker exec "${NAME}" sh -c 'stat -c "%u:%g %a" /usr/share/nginx/html/rpm' | grep -q '^101:101 2775$' \
  || { echo "FAIL: tree root is not 101:101 2775"; docker exec "${NAME}" stat /usr/share/nginx/html/rpm; exit 1; }
echo "   tree root is 101:101 2775"

echo "== provision component directories as the auth service would (root:101, no capabilities)"
docker exec --user 0:101 "${NAME}" sh -c '
  set -e
  mkdir -p /usr/share/nginx/html/rpm/bluebird/38/el9-x86_64 /usr/share/nginx/html/rpm/bluebird/38/el10-x86_64
  stat -c "%u:%g %a" /usr/share/nginx/html/rpm/bluebird/38/el9-x86_64
' | grep -q '^0:101 2755$' || { echo "FAIL: auth-style mkdir did not yield root:101 setgid dirs"; exit 1; }
echo "   created as 0:101 2755 (setgid inherited)"

echo "== publish into el9 and el10 as root:101"
docker exec --user 0:101 "${NAME}" bash -c '
  set -euo pipefail
  ROOT=/usr/share/nginx/html
  /scripts/add-package.sh /stage/*.rpm bluebird 38 el9-x86_64 el10-x86_64 > /dev/null

  f9="$(ls "${ROOT}"/rpm/bluebird/38/el9-x86_64/*.rpm)"
  f10="$(ls "${ROOT}"/rpm/bluebird/38/el10-x86_64/*.rpm)"
  i9="$(stat -c %i "${f9}")"; i10="$(stat -c %i "${f10}")"
  [ "${i9}" = "${i10}" ] || { echo "FAIL: expected one inode, got ${i9} and ${i10}"; exit 1; }
  echo "   one inode: ${i9}"

  for os in el9-x86_64 el10-x86_64; do
    [ -f "${ROOT}/rpm/bluebird/38/${os}/repodata/repomd.xml" ] || { echo "FAIL: no repodata in ${os}"; exit 1; }
    zcat "${ROOT}/rpm/bluebird/38/${os}/repodata/"*primary.xml.gz | grep -q packyard-fixture \
      || { echo "FAIL: ${os} primary.xml does not list the package"; exit 1; }
    echo "   ${os}: repodata lists packyard-fixture"
  done

  echo "== nginx (uid 101) can read what root:101 published"
  stat -c "%a" "${ROOT}/rpm/bluebird/38/el9-x86_64/repodata/repomd.xml" | grep -qE "^6[0-9][4-7]$" \
    || { echo "FAIL: repomd.xml is not world-readable"; exit 1; }
  echo "   repodata world-readable"

  echo "== missing target is refused before publishing"
  if /scripts/add-package.sh /stage/*.rpm bluebird 38 el9-x86_64 el8-x86_64 > /tmp/out 2>&1; then
    echo "FAIL: el8 target should have been refused"; exit 1
  fi
  grep -q "rpm_os_families" /tmp/out || { echo "FAIL: error should point at rpm_os_families"; cat /tmp/out; exit 1; }
  echo "   refused with the rpm_os_families hint"

  echo "== traversal is refused"
  if /scripts/add-package.sh /stage/*.rpm bluebird ../38 el9-x86_64 > /dev/null 2>&1; then
    echo "FAIL: ../38 should have been refused"; exit 1
  fi
  echo "   ../38 refused"

  echo "== a year series still works"
  mkdir -p "${ROOT}/rpm/core/2025/el9-x86_64"
  /scripts/add-package.sh /stage/*.rpm core 2025 el9-x86_64 > /dev/null
  echo "   ok"
'
echo "== nginx serves the published repodata"
docker exec "${NAME}" sh -c 'curl -sf -o /dev/null -w "%{http_code}\n" http://127.0.0.1:8080/rpm/bluebird/38/el10-x86_64/repodata/repomd.xml' | grep -qx 200 \
  || { echo "FAIL: nginx does not serve repomd.xml"; exit 1; }
echo "   200 from nginx"
echo "PASS"
