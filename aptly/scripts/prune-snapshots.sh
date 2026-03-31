#!/usr/bin/env bash
# prune-snapshots.sh — remove old Aptly snapshots, retaining the 2 most recent
# Usage: prune-snapshots.sh <component> <year>
# Example: prune-snapshots.sh core 2025
#
# C2 CONSTRAINT: MUST use `aptly publish list -raw` to identify published snapshots.
# NEVER deletes a snapshot that is currently published.
# Retains the 2 most recent snapshots per component/year (NFR14).
# Supports dry-run mode: DRY_RUN=1 prune-snapshots.sh core 2025
set -euo pipefail

COMPONENT="${1:?component required}"
YEAR="${2:?year required}"
KEEP_COUNT="${KEEP_COUNT:-2}"
DRY_RUN="${DRY_RUN:-0}"

PREFIX="${COMPONENT}-${YEAR}-"

echo "Pruning snapshots for ${COMPONENT}/${YEAR} (keeping ${KEEP_COUNT} most recent)..."
[ "${DRY_RUN}" = "1" ] && echo "  [DRY RUN — no deletions will occur]"

# C2: identify snapshot names for all currently-published endpoints
# aptly publish list -raw outputs: "{prefix} {distro}" — NF is the distro, NOT the snapshot name
# Must query each endpoint with aptly publish show to resolve the actual snapshot in use
PUBLISHED=""
while IFS=' ' read -r pub_prefix pub_distro; do
  [ -z "${pub_prefix}" ] && continue
  snap=$(aptly publish show "${pub_distro}" "${pub_prefix}" 2>/dev/null \
    | awk '/\[snapshot\]/{print $2; exit}' || true)
  [ -n "${snap}" ] && PUBLISHED="${PUBLISHED}${snap}"$'\n'
done < <(aptly publish list -raw 2>/dev/null || true)
echo "Currently published snapshots:"
if [ -z "${PUBLISHED}" ]; then
  echo "  (none)"
else
  echo "${PUBLISHED}" | sed 's/^/  /'
fi

# List all snapshots matching this component/year prefix, sorted by name
# Timestamp in name (YYYYMMDDTHHMMSSZ) ensures correct chronological sort
ALL=$(aptly snapshot list -raw 2>/dev/null | grep "^${PREFIX}" | sort || true)

if [ -z "${ALL}" ]; then
  echo "No snapshots found matching prefix '${PREFIX}'. Nothing to prune."
  exit 0
fi

TOTAL=$(echo "${ALL}" | wc -l | tr -d ' ')
echo "Found ${TOTAL} snapshot(s) for ${COMPONENT}/${YEAR}:"
echo "${ALL}" | sed 's/^/  /'

if [ "${TOTAL}" -le "${KEEP_COUNT}" ]; then
  echo "Total (${TOTAL}) ≤ keep count (${KEEP_COUNT}). Nothing to prune."
  exit 0
fi

# Candidates for deletion: all but the KEEP_COUNT most recent (head of sorted list)
TO_DELETE=$(echo "${ALL}" | head -n "-${KEEP_COUNT}")

echo ""
echo "Candidates for deletion:"
echo "${TO_DELETE}" | sed 's/^/  /'

DELETED=0
SKIPPED=0

while IFS= read -r snap; do
  [ -z "$snap" ] && continue
  # C2 guard: never delete a currently-published snapshot
  if echo "${PUBLISHED}" | grep -qF "${snap}"; then
    echo "SKIP (published): ${snap}"
    SKIPPED=$((SKIPPED + 1))
    continue
  fi

  if [ "${DRY_RUN}" = "1" ]; then
    echo "DRY RUN — would delete: ${snap}"
    DELETED=$((DELETED + 1))
  else
    echo "Deleting: ${snap}"
    aptly snapshot drop "${snap}"
    DELETED=$((DELETED + 1))
  fi
done <<< "${TO_DELETE}"

echo ""
echo "Done. Deleted: ${DELETED}, Skipped (published): ${SKIPPED}"
