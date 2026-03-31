#!/usr/bin/env bash
# backup-keystore.sh — SQLite online backup for the packyard auth keystore (Story 5.4)
#
# USAGE (inside the backup container):
#   /scripts/backup-keystore.sh
#
# USAGE (from host via docker exec):
#   docker exec $(docker compose ps -q backup) /scripts/backup-keystore.sh
#
# VOLUMES:
#   auth-db    mounted at /data/db   (read-only inside the backup container)
#   auth-backup mounted at /backup   (write)
#
# The backup uses sqlite3's .backup command, which is transaction-safe and
# does not require an EXCLUSIVE lock — safe to run while auth is active.
#
# Backups older than 7 days are pruned after a successful backup.
set -euo pipefail

DB_PATH="${DB_PATH:-/data/db/auth.db}"
BACKUP_DIR="${BACKUP_DIR:-/backup}"
TIMESTAMP="$(date -u +%Y%m%dT%H%M%SZ)"
BACKUP_FILE="${BACKUP_DIR}/auth-${TIMESTAMP}.db"

echo "[backup] Starting keystore backup: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "[backup] Source: ${DB_PATH}"
echo "[backup] Destination: ${BACKUP_FILE}"

if [ ! -f "${DB_PATH}" ]; then
  echo "[backup] ERROR: source database not found at ${DB_PATH}" >&2
  exit 1
fi

mkdir -p "${BACKUP_DIR}"

# sqlite3 .backup performs a hot backup using the SQLite backup API.
# It is safe to run while the database is in use (no exclusive lock needed).
sqlite3 "${DB_PATH}" ".backup '${BACKUP_FILE}'"
echo "[backup] Backup written."

# Verify backup integrity by querying the subscription_key table.
ROW_COUNT="$(sqlite3 "${BACKUP_FILE}" "SELECT count(*) FROM subscription_key")"
echo "[backup] Integrity check: subscription_key row count = ${ROW_COUNT}"

# Prune backups older than 7 days.
PRUNED=0
while IFS= read -r -d '' OLD_FILE; do
  echo "[backup] Pruning: ${OLD_FILE}"
  rm -f "${OLD_FILE}"
  PRUNED=$((PRUNED + 1))
done < <(find "${BACKUP_DIR}" -name 'auth-*.db' -mtime +7 -print0 2>/dev/null || true)

echo "[backup] Pruned ${PRUNED} old backup(s)."
echo "[backup] Done: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
