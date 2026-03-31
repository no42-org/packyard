# Auth Keystore Restore Procedure

**Target RTO:** under 5 minutes

This procedure restores the packyard auth SQLite database from a backup in the `auth-backup` volume.

> **Security note (I5):** Backup files contain subscription key values — treat them as sensitive credential material. Restrict access to the `auth-backup` volume and any host paths where backups are stored or transferred.

---

## Prerequisites

- Docker Compose v2 (`docker compose`)
- Access to the host running the packyard stack
- At least one valid backup in the `auth-backup` volume (verify with step 1 below)

---

## Step 1 — Identify the backup to restore

List available backups in the `auth-backup` volume:

```bash
docker run --rm -v auth-backup:/backup alpine ls -lt /backup/auth-*.db
```

Choose the most recent backup file (e.g. `auth-20260330T120000Z.db`). Verify it is valid before proceeding:

```bash
docker run --rm -v auth-backup:/backup keinos/sqlite3 \
  sqlite3 /backup/auth-20260330T120000Z.db "SELECT count(*) FROM subscription_key"
```

A non-negative integer output confirms the backup is readable and structurally valid.

---

## Step 2 — Stop the auth service

```bash
docker compose stop auth
```

This gracefully stops the auth container. Traefik will forward auth requests to a stopped service and return 503 — subscribers cannot authenticate during the restore window.

---

## Step 3 — Copy the backup into the auth-db volume

```bash
docker run --rm \
  -v auth-db:/target \
  -v auth-backup:/backup \
  alpine \
  cp /backup/auth-20260330T120000Z.db /target/auth.db
```

Replace `auth-20260330T120000Z.db` with the filename identified in step 1.

---

## Step 4 — Restart the auth service

```bash
docker compose start auth
```

Wait for the health check to pass:

```bash
docker compose ps auth
# State should transition to: healthy
```

---

## Step 5 — Verify forwardAuth is operational

Use a subscription key that was valid at the time of the backup:

```bash
curl -s -o /dev/null -w '%{http_code}' \
  -u "subscriber:${VALID_KEY}" \
  https://pkg.mdn.opennms.com/rpm/el9-x86_64/core/2025/repodata/repomd.xml
# Expected: 200
```

A 200 response confirms the restored database is active and forwardAuth is functioning.

---

## Full one-liner (steps 2–5)

```bash
BACKUP_FILE="auth-20260330T120000Z.db"

docker compose stop auth && \
docker run --rm \
  -v auth-db:/target \
  -v auth-backup:/backup \
  alpine \
  cp /backup/${BACKUP_FILE} /target/auth.db && \
docker compose start auth
```

---

## Troubleshooting

| Symptom | Likely cause | Action |
|---------|-------------|--------|
| Auth container fails to start | Corrupt backup file | Restore from an older backup (repeat from step 1) |
| `SELECT count(*)` returns error in step 1 | Backup was interrupted or disk full | Choose a different backup file |
| forwardAuth returns 503 after restore | Auth container still starting | Wait for health check (`docker compose ps auth`) |
| Known-valid key returns 401 | Key was created after the backup was taken | Re-provision the key via the admin API |
