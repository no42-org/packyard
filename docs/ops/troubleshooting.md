# Troubleshooting

## Quick reference

| Symptom | Likely cause | Jump to |
|---------|-------------|---------|
| TLS cert not issuing after first `docker compose up` | DNS not propagated, or port 443 not open | [TLS certificate not issuing](#tls-certificate-not-issuing) |
| Subscribers getting `401` on a key that should work | Key scope mismatch, revoked key, or auth service down | [Subscribers getting 401](#subscribers-getting-401) |
| Subscribers getting `404` on a path that should exist | Component name in URL not provisioned via the components API | [Subscribers getting 401](#subscribers-getting-401) |
| All requests returning `503` | Auth service is down (fail-closed by design) | [All requests returning 503](#all-requests-returning-503) |
| Admin login redirects back to /login with error | OAuth / allowlist / org / role failure | [Operator login fails](#operator-login-fails) |
| Admin API returns `401 UNAUTHORIZED` to the browser | Session expired or missing | [Admin API rejects the SPA](#admin-api-rejects-the-spa) |
| `https://admin.pkg.example.org/` does not resolve | DNS / TLS / Traefik routing | [Admin host unreachable](#admin-host-unreachable) |
| Container shows `unhealthy` or `exited` | Crash loop, disk full, or DB permissions | [Container unhealthy or crashing](#container-unhealthy-or-crashing) |
| Promotion workflow fails at GPG step | `lts.asc` is still a placeholder, or secrets not set | [Promotion pipeline failures](#promotion-pipeline-failures) |

---

## TLS certificate not issuing

Traefik uses the **TLS-ALPN-01** challenge — it completes over port 443 with no dependency on port 80.

**Check 1 — DNS propagation**

The TLS-ALPN-01 challenge requires `pkg.example.org` to resolve to the VM's IP before Traefik can obtain a certificate. Verify from an external host:

```bash
dig +short pkg.example.org A
# must return the VM's public IP
```

If DNS has not propagated, wait and retry. Do not restart Traefik repeatedly — Let's Encrypt [rate limits](https://letsencrypt.org/docs/rate-limits/) allow 5 failed validation attempts per domain per hour. Exhausting this limit will block cert issuance for up to an hour.

**Check 2 — Port 443 open**

Confirm the VM's firewall allows inbound TCP 443:

```bash
# From an external host
curl -v https://pkg.example.org/gpg/lts.asc
# Connection refused → firewall blocking 443
# Certificate error  → port open, cert not yet issued (normal on first boot)
```

**Check 3 — Traefik logs**

```bash
docker compose logs traefik | grep -i "acme\|certificate\|error"
```

Successful issuance looks like:
```
msg="Certificate obtained successfully" domain=pkg.example.org
```

**Check 4 — `acme.json` in the certs volume**

If the `traefik-certs` volume was deleted (e.g. `docker compose down -v`), Traefik will attempt to re-issue. After a successful issuance, the cert is stored in the volume — do not delete it with `-v` unless you intend to re-request.

```bash
docker run --rm -v traefik-certs:/certs alpine sh -c 'cat /certs/acme.json'
```

---

## Subscribers getting 401

A `401` response can come from three independent causes. Work through them in order.

**Step 1 — Is the auth service running?**

```bash
docker compose ps auth
# State must be: running (healthy)
```

If the state is `unhealthy` or `exited`, see [Container unhealthy or crashing](#container-unhealthy-or-crashing). If auth is down, Traefik returns `503`, not `401` — but some HTTP clients or package managers may display this differently.

**Step 2 — Does the key exist and is it active?**

Sign in at `https://admin.pkg.example.org/admin/` and find the subscriber's
account; the keys table on the account-detail page shows id, component,
active, and revoked rows. For CLI use, capture the session cookie from a
browser login and query the API:

```bash
COOKIE="packyard_session=…"
curl -s "https://admin.pkg.example.org/api/v1/keys?account=${ACCOUNT_ID}" \
  --cookie "$COOKIE" | jq '.[] | {id, component, label, active}'
```

If the key is absent, it was created after the last backup and lost during
a keystore restore. Re-issue it via the SPA's "Issue key" action on the
account, or via the API:

```bash
curl -s -X POST "https://admin.pkg.example.org/api/v1/accounts/${ACCOUNT_ID}/keys" \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"component":"core","label":"re-provisioned"}' | jq .
```

If the key is present but `"active": false`, it has been revoked. Issue a
new key.

**Step 3 — Is the key scoped to the right component?**

Each key is scoped to a single component. A `core` key cannot access
`/rpm/minion/` — that is the expected behaviour, not a bug. The account
detail page shows the `component` column; in CLI form:

```bash
curl -s "https://admin.pkg.example.org/api/v1/keys?account=${ACCOUNT_ID}" \
  --cookie "$COOKIE" | jq '.[] | {id, component, active}'
```

**Step 4 — Is the component marked public?**

If a component has `visibility: public` (set via `POST /api/v1/components` or
updated via `PATCH /api/v1/components/{name}`), the auth service allows
requests to its paths without inspecting credentials. Forward-auth reads
visibility from the database on every request — changes take effect
immediately without a restart. If a subscriber reports getting `401` on a
public component path, confirm the component's current visibility:

```bash
curl -s "https://admin.pkg.example.org/api/v1/components/core" \
  --cookie "$COOKIE" | jq .visibility
```

**Restart semantics — when a restart is and is not required:**

- **Key creation** (`POST /api/v1/keys`) and **forward-auth decisions** both query the database on every request — a restart is **not** needed for new components, deleted components, or visibility changes to take effect.
- **Key list filter** (`GET /api/v1/keys?component=<name>`) validates the component name against an in-memory map loaded at startup — it returns `400 INVALID_COMPONENT` for a newly provisioned component until the service is restarted.
- **`component_visibility` in key responses** is also derived from the startup-loaded map — it may show a stale value after a `PATCH /api/v1/components/{name}` visibility change until the service is restarted. This is cosmetic only; forward-auth always uses the live value.

---

## POST /api/v1/components returns 500 RPM_INIT_FAILED

The auth service failed to create the RPM directory tree for the new component. The component record was rolled back — the name is safe to reuse.

**Common causes:**

| Cause | How to confirm | Fix |
|-------|---------------|-----|
| `rpm-data` volume not mounted to auth container | `docker compose exec auth ls /data/rpm` — should list a `rpm/` subdirectory | Verify `compose.yml` has `rpm-data:/data/rpm` under the `auth` service volumes |
| `RPM_DATA_ROOT` mismatch | `docker compose exec auth env \| grep RPM_DATA_ROOT` | Ensure `RPM_DATA_ROOT` matches the volume mount point (default: `/data/rpm`) |
| Wrong permissions on the volume | `docker compose exec auth ls -la /data/rpm` | `docker compose exec rpm chown -R nobody:nobody /usr/share/nginx/html/rpm` or adjust mount ownership |
| Disk full | `docker compose exec auth df -h /data/rpm` | Free disk space |

After fixing the underlying cause, retry `POST /api/v1/components` with the same body — the name is available again.

---

## All requests returning 503

The auth service is configured fail-closed: if it is unreachable or returns an unexpected error, Traefik returns `503` rather than allowing the request through.

**Diagnose:**

```bash
docker compose ps auth
docker compose logs auth --tail=50
```

Common causes:

| Cause | Indicator in logs | Action |
|-------|------------------|--------|
| Auth container exited | `exited (1)` in `ps` | `docker compose start auth` then check logs |
| SQLite DB corrupt or missing | `unable to open database` | Follow [Restore Keystore](restore-keystore.md) |
| Out of disk space | `no space left on device` | Free disk space, then `docker compose restart auth` |
| OOM killed | `exit code 137` | Increase VM RAM or reduce other workloads |

---

## Admin host unreachable

The admin UI + API live at `https://admin.pkg.example.org/` (or whatever
`ADMIN_DOMAIN` you set). The historical loopback admin entrypoint
(`127.0.0.1:8088`) has been retired — there is no SSH tunnel any more.

**Step 1 — DNS for the admin host?**

```bash
dig +short admin.pkg.example.org A
# must return the VM's public IP
```

If empty, add the record (see [§1 of the deployment guide](production-deployment.md#1-dns-records))
and wait for propagation.

**Step 2 — TLS cert covers `admin.*`?**

Traefik's ACME resolver issues a separate cert for `admin.pkg.example.org`.
Successful issuance log line:

```
msg="Certificate obtained successfully" domain=admin.pkg.example.org
```

Same Let's Encrypt rate-limit caveats apply as for the primary host —
don't restart Traefik in a loop.

**Step 3 — `ADMIN_DOMAIN` env var set?**

The Traefik router templates `{{ env "ADMIN_DOMAIN" }}` into its `Host()`
rule. Missing env → compose-up fails with a clear error; mismatched env →
404 because the rule does not match the request.

```bash
docker compose exec traefik env | grep ADMIN_DOMAIN
```

**Step 4 — Auth service healthy?**

The admin host routes to `http://auth:8080`. If `auth` is unhealthy
Traefik returns `503`, not 404.

```bash
docker compose ps auth
docker compose logs auth --tail=50
```

## Operator login fails

The browser ends up at `/admin/login?error=CODE` after an unsuccessful OAuth
round-trip. The `CODE` query parameter names the failure mode:

| Code                       | Meaning                                                                                         | Fix                                                              |
|----------------------------|-------------------------------------------------------------------------------------------------|------------------------------------------------------------------|
| `OPERATOR_NOT_ALLOWED`     | The OAuth identity's verified email is not in the `operators` allowlist                          | Allowlist the email (see [operator onboarding](operator-onboarding.md)) |
| `OPERATOR_DISABLED`        | Email is allowlisted but the row has `status='disabled'`                                         | PATCH `{"status":"active"}` via the SPA or API                   |
| `ORG_MEMBERSHIP_REQUIRED`  | GitHub user is not an active member of `PACKYARD_GITHUB_ORG`                                     | Add the user to the org, or use the other provider               |
| `EMAIL_NOT_VERIFIED`       | IdP returned an unverified email                                                                 | Operator verifies their email at the IdP, then retries           |
| `INVALID_OAUTH_STATE`      | OAuth state cookie missing / mismatched (browser stripped cookies, or callback hit twice)        | Retry the login                                                  |
| `OAUTH_EXCHANGE_FAILED`    | IdP rejected our token request (bad client secret, missing scope, SAML SSO challenge unfulfilled) | Verify env-var values; for GitHub, ensure `read:org` is approved |
| `UNKNOWN_PROVIDER`         | `/login/{provider}` path named a provider not configured at startup                              | Verify the provider's env vars are set as a complete set         |
| `RATE_LIMITED`             | Source IP exceeded the OAuth bucket (10 req capacity, 1/6s refill)                               | Wait a minute; investigate the source if persistent              |

Server-side log lines (`docker compose logs auth`) carry richer context —
e.g. the `login.failure` audit row includes the IdP-reported email even when
the request is rejected, which is the fastest path to identifying the
mis-allowlisted address.

## Admin API rejects the SPA

A successful login lands the operator in the SPA, but a subsequent admin
action returns `401 UNAUTHORIZED` or `401 SESSION_EXPIRED`. Both surface in
the SPA's red error banner.

- **`UNAUTHORIZED`**: the session cookie is missing — usually because the
  browser dropped it (cleared cookies, switched profile) or it never landed
  (Traefik routed `/admin/` to the SPA without first servicing the OAuth
  callback). The SPA detects this and redirects to `/admin/login`.
- **`SESSION_EXPIRED`**: the session row exists but exceeded the 8-hour
  idle timeout or the 24-hour absolute lifetime. Re-login fixes it.
- **`CSRF_DENIED`** on a mutating request: the `Origin`/`Referer` header
  does not match `PACKYARD_ADMIN_HOST`. Cause is almost always a
  proxy/header-rewriting layer in front of the deployment, or a stale
  bookmark that uses a different hostname. Verify the operator is on
  `https://admin.pkg.example.org/`.
- **`ROLE_DENIED`** on the Operators page (e.g.): the operator is `readonly`
  and tried a mutating action. The SPA hides the page entirely from
  readonly operators — if they reached it, they bookmarked the URL.

---

## Container unhealthy or crashing

**Check all container states:**

```bash
docker compose ps
```

**Get logs for a specific service:**

```bash
docker compose logs <service> --tail=100
# services: traefik, auth, rpm, deb, zot, aptly, rustfs, static, backup
```

**Auth container crash loop — common causes:**

```bash
docker compose logs auth | tail -20
```

| Log message | Cause | Fix |
|-------------|-------|-----|
| `unable to open database file` | `auth-db` volume missing or wrong permissions | `docker compose down && docker compose up -d` |
| `no space left on device` | Disk full | Free space, then `docker compose restart auth` |
| `exit code 137` | OOM killed | Increase VM RAM |

**Restart a single service without affecting others:**

```bash
docker compose restart <service>
```

---

## Promotion pipeline failures

### `ERROR: lts.asc is still a placeholder`

The promotion workflows verify `static/content/gpg/lts.asc` contains a real GPG public key before signing. This error means the placeholder has not been replaced.

Follow §4.1 of [Production Deployment](production-deployment.md#41-gpg-signing-key) to generate a key and commit `lts.asc`.

### GPG signing fails — `secret key not available`

The `GPG_PRIVATE_KEY` or `GPG_KEY_ID` secret is missing or incorrect in GitHub Actions repository settings. Verify all 10 secrets are set (§3 of [Production Deployment](production-deployment.md#3-secrets--environment)).

### SSH connection to VM fails

The promotion workflows SSH into the VM to run `docker exec` commands. If the connection fails:

1. Verify `SSH_PRIVATE_KEY` and `SSH_KNOWN_HOST` secrets match the current VM.
2. Check the VM's `deploy` user still has the correct authorized key:
   ```bash
   cat /home/deploy/.ssh/authorized_keys
   ```
3. If the VM was rebuilt, re-run `ssh-keyscan pkg.example.org` and update `SSH_KNOWN_HOST`.

### RustFS artifact not found during promotion

The promotion workflow downloads a staged artifact from RustFS before signing. If the artifact is not found:

1. Confirm the artifact was staged with the correct `component`, `series`, and `os` values that match the workflow inputs.
2. Open an SSH tunnel to RustFS and list the staging bucket:
   ```bash
   ssh -L 9000:localhost:9000 deploy@pkg.example.org -N &
   AWS_ACCESS_KEY_ID=<key> AWS_SECRET_ACCESS_KEY=<secret> \
     aws s3 ls s3://staging/ \
     --endpoint-url http://localhost:9000 \
     --region us-east-1 \
     --recursive
   ```
