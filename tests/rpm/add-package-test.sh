#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# add-package-test.sh — publish a fixture RPM into two OS targets inside the
# rpm image and check that the bytes are stored once and each target has its
# own repodata. Needs Docker. Run via `make test-rpm-publish`.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
WORK="$(mktemp -d)"
IMAGE="packyard-rpm-test:local"
NFPM_IMAGE="goreleaser/nfpm:v2.44.0"
trap 'rm -rf "${WORK}"' EXIT

echo "== build rpm image"
docker build -q -t "${IMAGE}" "${REPO_ROOT}/rpm" > /dev/null

echo "== build fixture RPM with nfpm"
cp "${REPO_ROOT}/tests/rpm/fixtures/nfpm.yaml" "${WORK}/"
echo "hello" > "${WORK}/hello.txt"
docker run --rm -v "${WORK}:/work" -w /work "${NFPM_IMAGE}" package --packager rpm --target /work/ > /dev/null
FIXTURE="$(ls "${WORK}"/*.rpm)"
echo "   $(basename "${FIXTURE}")"

# The tree is normally created by the auth service from the component record.
# Simulate that: provision el9 and el10, leave el8 absent.
echo "== publish into el9 and el10"
docker run --rm \
  -v "${WORK}:/stage:ro" \
  --entrypoint /bin/bash \
  "${IMAGE}" -c '
    set -euo pipefail
    ROOT=/usr/share/nginx/html
    mkdir -p "${ROOT}/rpm/bluebird/38/el9-x86_64" "${ROOT}/rpm/bluebird/38/el10-x86_64"
    /scripts/add-package.sh /stage/*.rpm bluebird 38 el9-x86_64 el10-x86_64 > /dev/null

    f9="$(ls "${ROOT}"/rpm/bluebird/38/el9-x86_64/*.rpm)"
    f10="$(ls "${ROOT}"/rpm/bluebird/38/el10-x86_64/*.rpm)"
    i9="$(stat -c %i "${f9}")"; i10="$(stat -c %i "${f10}")"
    [ "${i9}" = "${i10}" ] || { echo "FAIL: expected one inode, got ${i9} and ${i10}"; exit 1; }
    echo "   one inode: ${i9}"

    for os in el9-x86_64 el10-x86_64; do
      [ -f "${ROOT}/rpm/bluebird/38/${os}/repodata/repomd.xml" ] || { echo "FAIL: no repodata in ${os}"; exit 1; }
      grep -q "packyard-fixture" "${ROOT}/rpm/bluebird/38/${os}/repodata/"*primary.xml* 2>/dev/null \
        || zcat "${ROOT}/rpm/bluebird/38/${os}/repodata/"*primary.xml.gz | grep -q packyard-fixture \
        || { echo "FAIL: ${os} primary.xml does not list the package"; exit 1; }
      echo "   ${os}: repodata lists packyard-fixture"
    done

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

    echo "== series 38 accepted, series 2025 still accepted"
    mkdir -p "${ROOT}/rpm/core/2025/el9-x86_64"
    /scripts/add-package.sh /stage/*.rpm core 2025 el9-x86_64 > /dev/null
    echo "   ok"
  '
echo "PASS"
