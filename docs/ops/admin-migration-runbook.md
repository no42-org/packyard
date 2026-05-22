# Admin migration runbook

This runbook covers the deploy that flips the Packyard admin surface from
the legacy loopback model (`127.0.0.1:8088`, no auth) to the new
OAuth-gated public host (`https://admin.pkg.example.org/`).

The implementation work is already merged. This runbook is the operational
checklist for cutting over a live deployment. Run it in order; do not skip
the snapshot or the rehearsal.

---

## 0. Pre-flight assumptions

- You have shell access to the production VM as `deploy`.
- You have control of the DNS zone for `pkg.example.org`.
- You have admin rights in the GitHub organisation (and / or Microsoft
  Entra tenant) that will gate operator access.
- You have read both
  [Production deployment §3 (Secrets & Environment)](production-deployment.md#3-secrets--environment)
  and [Operator onboarding §1 (Register the IdP apps)](operator-onboarding.md#1-register-the-idp-apps-one-time-per-deployment).

---

## 1. Size the migration window

The legacy schema had subscription keys without an `account_id` column.
The deployed migration creates a single synthetic "legacy" account and
backfills every key with `account_id = <legacy id>`. The migration is
done inside one serialisable transaction and is bounded by the active key
count.

**Capture the count from production:**

```bash
ssh deploy@pkg.example.org -- \
  docker compose exec -T auth sqlite3 /data/db/auth.db \
    "SELECT COUNT(*) FROM subscription_key WHERE active = 1;"
```

Record the number in this runbook (commit the update before the deploy):

```
Active key count (pre-deploy):  ____
Migration window estimate:       < N seconds (target: under 5 s for any count below 100k)
```

For reference: the legacy-account backfill is a single `UPDATE ...` plus a
schema rebuild for the NOT-NULL constraint. SQLite with WAL mode + single
writer handles ~50k rows per second on the production VM hardware, so any
plausible count finishes well inside the deploy's restart window.

---

## 2. Verify the implementation

The migration is already coded — these steps confirm it's present on the
release tag you are about to deploy.

```bash
# 1. The legacy-account backfill lives in store/sqlite.go.
grep -n "rebuildSubscriptionKeyNotNull\|legacy" auth/internal/store/sqlite.go | head -10

# 2. The /admin/* handler is wired in main.go.
grep -n 'r.Route("/admin"' auth/cmd/server/main.go

# 3. The Dockerfile multi-stage SPA build is present.
grep -n "FROM node" auth/Dockerfile
```

All three greps must return matches. If any are empty, the release tag is
stale — rebuild from `main`.

---

## 3. Snapshot the database

Take a fresh backup **before** any migration step. The existing daily
`backup` container produces a snapshot every 24 hours; do not rely on
it — take an on-demand snapshot at the start of the deploy window.

```bash
ssh deploy@pkg.example.org -- bash <<'EOF'
cd ~/packyard
TS=$(date -u +%Y%m%d-%H%M%S)
docker compose exec -T auth sqlite3 /data/db/auth.db ".backup '/data/backup/auth.db.pre-admin-migration.${TS}'"
ls -lh /data/backup/auth.db.pre-admin-migration.*
EOF
```

The snapshot lives in the `auth-backup` volume. If anything goes wrong
during the migration, restore via the existing
[Restore keystore](restore-keystore.md) runbook.

---

## 4. Verify the OAuth apps

Both providers must be reachable and reachable's redirect URIs must
exactly match the production callback URLs. **Pre-deploy, before flipping
DNS for the new admin host**:

### GitHub

```bash
curl -sI "https://github.com/login/oauth/authorize?client_id=${PACKYARD_GITHUB_CLIENT_ID}"
# Expect: HTTP/2 302 with a `Location:` header that includes the configured
# callback URI ("redirect_uri=https://admin.pkg.example.org/api/v1/auth/callback/github").
```

Cross-check the OAuth App settings:

- **Authorization callback URL** is exactly
  `https://admin.pkg.example.org/api/v1/auth/callback/github`.
- The app has access to query the configured `PACKYARD_GITHUB_ORG`'s
  membership (`read:org` scope, org-owner approval if your org's
  third-party app policy requires it).

### Microsoft Entra

```bash
# Inspect the OpenID Connect discovery document to confirm the tenant is
# reachable.
curl -s "https://login.microsoftonline.com/${PACKYARD_MICROSOFT_TENANT_ID}/v2.0/.well-known/openid-configuration" \
  | jq '.issuer, .authorization_endpoint, .token_endpoint'
```

Cross-check the Entra app registration:

- **Redirect URI (Web)** is exactly
  `https://admin.pkg.example.org/api/v1/auth/callback/microsoft`.
- Tenant id matches `PACKYARD_MICROSOFT_TENANT_ID`.
- Delegated permissions for `openid`, `profile`, `email` are added and
  admin consent is granted.

Record the verification results:

```
GitHub OAuth app reachable:        [ ]
GitHub callback URL exact match:   [ ]
GitHub org `read:org` approved:    [ ]
Entra OpenID discovery reachable:  [ ]
Entra callback URL exact match:    [ ]
Entra admin consent granted:       [ ]
```

---

## 5. Seed the bootstrap operator

Pick the human who will allowlist the rest of the operators. They MUST
have an account on whichever provider you're starting with (GitHub or
Microsoft) and their primary verified email on that provider MUST be the
exact email you set below.

Add to the production `.env`:

```dotenv
PACKYARD_BOOTSTRAP_OPERATOR_EMAIL=ops@example.org
```

The behaviour is idempotent: the env var is honoured **only** when the
`operators` table is empty. Once any operator exists (allowlist or
bootstrap), the variable is ignored. You can leave it set forever; it does
nothing on subsequent deploys.

Add `ADMIN_DOMAIN` and the OAuth env-var sets at the same time per
[Production deployment §3](production-deployment.md#3-secrets--environment).

---

## 6. Deploy

Standard deploy sequence. The migration runs at auth-service startup; the
loopback admin entrypoint is removed by the same Compose / Traefik
config change.

```bash
ssh deploy@pkg.example.org -- bash <<'EOF'
cd ~/packyard
git fetch
git checkout <release-tag>
docker compose pull
docker compose up -d --remove-orphans
# Tail the auth + traefik logs for the next 60 seconds.
docker compose logs auth traefik -f --since=60s
EOF
```

Expected log lines (auth):

- `loaded components from database` — component snapshot loaded.
- `bootstrap operator created` (only the first time
  `PACKYARD_BOOTSTRAP_OPERATOR_EMAIL` runs on an empty table).
- `oauth provider registered provider=github` (and / or `microsoft`).
- `starting packyard-auth ... db=/data/db/auth.db`.

Expected log lines (traefik):

- `Certificate obtained successfully domain=admin.pkg.example.org` (if
  ACME has not yet seen this hostname).

---

## 7. Post-deploy verification

Every check below must pass before declaring the migration complete.

### 7.1 Schema migration

```bash
ssh deploy@pkg.example.org -- \
  docker compose exec -T auth sqlite3 /data/db/auth.db <<'EOF'
.schema subscription_key
SELECT COUNT(*) AS keys_without_account FROM subscription_key WHERE account_id IS NULL;
SELECT COUNT(*) AS legacy_account_keys
  FROM subscription_key sk JOIN accounts a ON a.id = sk.account_id
  WHERE a.email = 'legacy@packyard.local';
EOF
```

Pass criteria:

- `keys_without_account` is `0`.
- `account_id` column is `NOT NULL` in the printed schema.
- `legacy_account_keys` equals the active-key count recorded in §1.

### 7.2 Loopback entrypoint is gone

```bash
ssh deploy@pkg.example.org -- ss -tlnp | grep ':8088 ' || echo OK
```

Pass criteria: `OK` is printed (no process is listening on 8088).

### 7.3 Admin host serves the SPA

From an external host:

```bash
curl -sI https://admin.pkg.example.org/admin/login
# Expect: HTTP/2 200, Content-Type: text/html

curl -sI https://admin.pkg.example.org/api/v1/operators
# Expect: HTTP/2 401, body: {"code":"UNAUTHORIZED",...}
```

### 7.4 OAuth round-trip on each provider

For each configured provider, sign in via a browser at
`https://admin.pkg.example.org/admin/login` and confirm:

- The IdP prompts you (and on subsequent runs, silent-auths).
- The redirect lands at `/admin/accounts` with the operator chrome
  showing your email + role.
- DevTools → Application → Cookies shows `packyard_session` with
  `HttpOnly`, `Secure`, `SameSite=Strict`.

Repeat for the second provider if both are configured.

### 7.5 Audit log captured the verification logins

```bash
ssh deploy@pkg.example.org -- \
  docker compose exec -T auth sqlite3 /data/db/auth.db \
    "SELECT ts, action, details FROM audit_log
     WHERE action IN ('login.success','login.failure')
     ORDER BY id DESC LIMIT 10;"
```

Pass criteria: at least one `login.success` row per provider you tested,
with the IdP captured in `details`.

### 7.6 Subscriber surface unaffected

The subscriber path is independent of the admin migration but worth a
smoke test:

```bash
# From an external host
curl -sI -u subscriber:${TEST_KEY} \
  https://pkg.example.org/rpm/core/2025/el9-x86_64/repodata/repomd.xml
# Expect: HTTP/2 200 (or 404 if no artifacts) — NOT 401.
```

---

## 8. Rollback

If §7 fails on a check that cannot be fixed in-place within the deploy
window:

```bash
ssh deploy@pkg.example.org -- bash <<'EOF'
cd ~/packyard
git checkout <previous-release-tag>
docker compose pull
docker compose stop auth
# Restore the snapshot taken in §3.
TS_BACKUP=<filename from §3>
docker compose exec -T auth sh -c "rm -f /data/db/auth.db /data/db/auth.db-wal /data/db/auth.db-shm"
docker run --rm -v packyard_auth-db:/db -v packyard_auth-backup:/backup alpine \
  cp /backup/${TS_BACKUP} /db/auth.db
docker compose up -d --remove-orphans
EOF
```

After rollback, the loopback admin entrypoint returns (the previous
release still ships it). Subscribers are unaffected — their keys and
forward-auth paths do not change between the legacy and new
implementations.

Open an incident issue documenting which §7 check failed and why; the
migration can be retried after the issue is fixed.

---

## 9. Sign-off

Migration window: ____ to ____ (UTC).

Operator who ran the migration: ____

Verification results: §7.1 ☐  §7.2 ☐  §7.3 ☐  §7.4 ☐  §7.5 ☐  §7.6 ☐

Snapshot retained until: ____ (recommended: ≥ 30 days post-deploy).
