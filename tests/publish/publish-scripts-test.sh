#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# publish-scripts-test.sh — run scripts/publish/rpm.sh and deb.sh against a
# hardened two-service stack (tests/publish/compose.yml) with nfpm-built
# fixtures and an ephemeral GPG key, and check what production relies on:
# repodata per target with one inode per package, a signed InRelease per
# distribution with one pool inode, nothing staged on the host, and no key
# material left in the aptly container after success or failure.
# Needs Docker and gpg. Run via `make test-publish-scripts`.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
COMPOSE="${REPO_ROOT}/tests/publish/compose.yml"
export COMPOSE_PROJECT_NAME="packyard-publish-test-$$"
# macOS tar adds ._* AppleDouble entries that aptly rejects; production tars
# come from Linux runners. Keep the test honest about what it sends.
export COPYFILE_DISABLE=1
WORK="$(mktemp -d)"
NFPM_IMAGE="goreleaser/nfpm:v2.44.0"
cleanup() { docker compose -f "${COMPOSE}" down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "${WORK}"; }
trap cleanup EXIT
fail() { echo "FAIL: $*" >&2; exit 1; }

echo "== build images"
docker build -q -t packyard-rpm-test:local "${REPO_ROOT}/rpm" > /dev/null
docker build -q -t packyard-aptly-test:local "${REPO_ROOT}/aptly" > /dev/null

echo "== fixtures (nfpm: one config, both packagers)"
mkdir -p "${WORK}/pkg"; cp "${REPO_ROOT}/tests/rpm/fixtures/nfpm.yaml" "${WORK}/pkg/"; echo hello > "${WORK}/pkg/hello.txt"
docker run --rm --user "$(id -u):$(id -g)" -v "${WORK}/pkg:/work" -w /work "${NFPM_IMAGE}" package --packager rpm --target /work/ > /dev/null
docker run --rm --user "$(id -u):$(id -g)" -v "${WORK}/pkg:/work" -w /work "${NFPM_IMAGE}" package --packager deb --target /work/ > /dev/null
ls "${WORK}"/pkg/*.rpm "${WORK}"/pkg/*.deb | xargs -n1 basename | sed 's/^/   /'

echo "== ephemeral GPG key (generated in a throwaway aptly container, exported)"
docker run --rm -v "${WORK}:/out" --entrypoint sh packyard-aptly-test:local -c '
  set -e; export GNUPGHOME=/tmp/g; mkdir -p $GNUPGHOME; chmod 700 $GNUPGHOME
  gpg --batch --quiet --passphrase secret --quick-gen-key "Packyard Test <test@example.org>" default default never
  KEYID=$(gpg --list-keys --with-colons | awk -F: "/^fpr/{print \$10; exit}")
  gpg --batch --passphrase secret --pinentry-mode loopback --armor --export-secret-keys "$KEYID" > /out/key.asc
  gpg --armor --export "$KEYID" > /out/pub.asc
  echo "$KEYID" > /out/keyid; chmod 644 /out/key.asc /out/pub.asc /out/keyid'
KEYID=$(cat "${WORK}/keyid"); echo "   key ${KEYID}"

echo "== stack up"
docker compose -f "${COMPOSE}" up -d >/dev/null 2>&1; sleep 3
# The component record provisions the tree in production (auth, as 0:101).
docker compose -f "${COMPOSE}" exec -T --user 0:101 rpm mkdir -p /usr/share/nginx/html/rpm/bluebird/38/el9-x86_64 /usr/share/nginx/html/rpm/bluebird/38/el10-x86_64

echo "== rpm.sh: one package into two targets"
tar -cf - -C "${WORK}/pkg" --exclude='*.deb' --exclude='*.txt' --exclude='*.yaml' . | bash "${REPO_ROOT}/scripts/publish/rpm.sh" "${COMPOSE}" bluebird 38 el9-x86_64 el10-x86_64 > /dev/null
docker compose -f "${COMPOSE}" exec -T rpm bash -c '
  set -e; R=/usr/share/nginx/html/rpm/bluebird/38
  for t in el9-x86_64 el10-x86_64; do [ -f "$R/$t/repodata/repomd.xml" ] || { echo "no repodata in $t"; exit 1; }; done
  [ "$(stat -c %i $R/el9-x86_64/*.rpm)" = "$(stat -c %i $R/el10-x86_64/*.rpm)" ] || { echo "two inodes"; exit 1; }
  [ ! -e /tmp/rpm-stage ] || { echo "stage left behind"; exit 1; }
  echo "   repodata in both targets, one inode, stage removed"' || fail "rpm.sh assertions"

echo "== deb.sh: one snapshot, two distributions, signed inside aptly"
mkdir -p "${WORK}/bundle/packages" "${WORK}/bundle/signing"
cp "${WORK}"/pkg/*.deb "${WORK}/bundle/packages/"; cp "${WORK}/key.asc" "${WORK}/bundle/signing/key.asc"; printf 'secret\n' > "${WORK}/bundle/signing/passphrase"
tar -cf - -C "${WORK}/bundle" . | bash "${REPO_ROOT}/scripts/publish/deb.sh" "${COMPOSE}" bluebird 38 "${KEYID}" bookworm noble > /dev/null
docker compose -f "${COMPOSE}" exec -T aptly sh -c '
  set -e; P=/opt/aptly/public/bluebird/38
  for d in bookworm noble; do [ -f "$P/dists/$d/InRelease" ] || { echo "no InRelease for $d"; exit 1; }; done
  n=$(find /opt/aptly -name "*.deb" -type f -exec stat -c %i {} \; | sort -u | wc -l); [ "$n" = 1 ] || { echo "pool inodes: $n"; exit 1; }
  [ ! -e /root/.gnupg ] && [ ! -e /tmp/gpg-pass ] && [ ! -e /tmp/deb-stage ] || { echo "key material or stage left behind"; exit 1; }
  cat "$P/dists/bookworm/InRelease"' > "${WORK}/InRelease" || fail "deb.sh assertions"
export GNUPGHOME="${WORK}/gnupg"; mkdir -p "${GNUPGHOME}"; chmod 700 "${GNUPGHOME}"
gpg --batch --quiet --import "${WORK}/pub.asc" 2>/dev/null
gpg --batch --verify "${WORK}/InRelease" 2>&1 | grep -q "Good signature" || fail "InRelease is not signed by the ephemeral key"
echo "   InRelease for both distributions, good signature, one pool inode, no key material left"

echo "== deb.sh: a failed publish still cleans up"
printf 'wrong\n' > "${WORK}/bundle/signing/passphrase"
if tar -cf - -C "${WORK}/bundle" . | bash "${REPO_ROOT}/scripts/publish/deb.sh" "${COMPOSE}" bluebird 38 "${KEYID}" trixie > /dev/null 2>&1; then fail "publish with a wrong passphrase should have failed"; fi
docker compose -f "${COMPOSE}" exec -T aptly sh -c '[ ! -e /root/.gnupg ] && [ ! -e /tmp/gpg-pass ] && [ ! -e /tmp/deb-stage ]' || fail "key material left behind after a failed publish"
echo "   failed as expected, container clean"
echo "PASS"
