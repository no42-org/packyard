#!/usr/bin/env bash
# Copyright 2026 Ronny Trommer <ronny@no42.org>
# SPDX-License-Identifier: GPL-3.0-or-later
#
# add-package.sh — publish one signed RPM into one or more OS targets of a
# component series and rebuild the metadata of each target.
#
# Usage: add-package.sh <signed-rpm> <component> <series> <os-arch> [<os-arch>...]
#
# The package bytes are stored once: the file is copied into the first target
# and hardlinked into every further target (same volume, same filesystem).
# Each target keeps its own repodata/, so subscriber URLs stay per OS.
#
# Target directories are owned by the component record (the auth service
# creates them from rpm_series x rpm_os_families x rpm_architectures). This
# script never creates them: a missing target means the component is not
# configured for that OS.
#
# Relies on the GHA concurrency group of the calling workflow to prevent
# concurrent createrepo_c runs on the same directory. No shell-level lock.
set -euo pipefail

RPM_FILE="${1:?signed RPM path required}"
COMPONENT="${2:?component required}"
SERIES="${3:?series required}"
shift 3
[ "$#" -ge 1 ] || { echo "ERROR: at least one os-arch target required (e.g. el9-x86_64)"; exit 1; }
ROOT="${RPM_ROOT:-/usr/share/nginx/html}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Same rule as the component API: one path segment, no traversal.
SEGMENT_RE='^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'
for value in "${COMPONENT}" "${SERIES}" "$@"; do
  if ! [[ "${value}" =~ ${SEGMENT_RE} ]] || [[ "${value}" == *..* ]]; then
    echo "ERROR: invalid path segment: ${value}"
    exit 1
  fi
done

[ -f "${RPM_FILE}" ] || { echo "ERROR: file not found: ${RPM_FILE}"; exit 1; }

# Validate every target before touching anything, so a typo in the last
# target does not leave the first ones half-published.
for os_arch in "$@"; do
  target="${ROOT}/rpm/${COMPONENT}/${SERIES}/${os_arch}"
  if [ ! -d "${target}" ]; then
    echo "ERROR: target dir not found: ${target}"
    echo "       Add the OS family and architecture to the component's rpm_os_families /"
    echo "       rpm_architectures (and the series to rpm_series) via the admin UI or"
    echo "       PATCH /api/v1/components/${COMPONENT}; the auth service creates the tree."
    exit 1
  fi
  [ -w "${target}" ] || { echo "ERROR: target dir not writable: ${target}"; exit 1; }
done

FILENAME="$(basename "${RPM_FILE}")"
FIRST=""
for os_arch in "$@"; do
  target="${ROOT}/rpm/${COMPONENT}/${SERIES}/${os_arch}"
  dest="${target}/${FILENAME}"
  if [ -z "${FIRST}" ]; then
    echo "Copying ${FILENAME} to ${target}/"
    cp "${RPM_FILE}" "${dest}"
    FIRST="${dest}"
  else
    rm -f "${dest}"
    if ln "${FIRST}" "${dest}" 2>/dev/null; then
      echo "Linking ${FILENAME} into ${target}/"
    else
      echo "WARN: hardlink failed, copying ${FILENAME} into ${target}/"
      cp "${FIRST}" "${dest}"
    fi
  fi
  "${SCRIPT_DIR}/rebuild-metadata.sh" "${ROOT}" "${COMPONENT}" "${SERIES}" "${os_arch}"
done
