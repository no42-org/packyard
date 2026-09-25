#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# check-release-version.sh <tag> — refuse a release tag that does not match
# the version in the tree. The Release workflow publishes whatever it is
# tagged on, so a tag pushed without the RELEASING.md § 1 bump ships images
# that self-report the -rc version and moves `latest` onto them (#275).
#
# A stable tag vX.Y.Z needs X.Y.Z in all four places. A prerelease tag
# vX.Y.Z-<suffix> is cut from the development version and needs X.Y.Z-rc.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

tag="${1:?usage: check-release-version.sh <tag, e.g. v1.2.3>}"
ver="${tag#v}"
case "${ver}" in
  *-*) want="${ver%%-*}-rc" ;;
  *) want="${ver}" ;;
esac

FAILED=0
check() {
  if [ "$2" != "${want}" ]; then
    echo "check-release-version: $1 has '$2', tag ${tag} needs '${want}'" >&2
    FAILED=1
  fi
}

check auth/cmd/server/version.go "$(sed -n 's/^const version = "\(.*\)"/\1/p' auth/cmd/server/version.go)"
check rpm/VERSION "$(cat rpm/VERSION)"
check static/VERSION "$(cat static/VERSION)"
for svc in auth rpm static; do
  check "compose.yml packyard-${svc}" "$(sed -n "s|.*ghcr.io/no42-org/packyard-${svc}:\([^[:space:]]*\).*|\1|p" compose.yml)"
done

if [ "${FAILED}" -ne 0 ]; then
  echo "check-release-version: bump the tree first (RELEASING.md § 1), then tag the bump commit" >&2
  exit 1
fi
echo "release version ${want} matches tag ${tag}"
