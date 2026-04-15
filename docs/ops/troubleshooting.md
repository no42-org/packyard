# Troubleshooting

## Quick reference

| Symptom | Likely cause | Jump to |
|---------|-------------|---------|
| TLS cert not issuing after first `docker compose up` | DNS not propagated, or port 443 not open | [TLS certificate not issuing](#tls-certificate-not-issuing) |
| Subscribers getting `401` on a key that should work | Key scope mismatch, revoked key, or auth service down | [Subscribers getting 401](#subscribers-getting-401) |
| Subscribers getting `404` on a path that should exist | Component name in URL not listed in `config/packyard.yml` | [Subscribers getting 401](#subscribers-getting-401) |
| All requests returning `503` | Auth service is down (fail-closed by design) | [All requests returning 503](#all-requests-returning-503) |
| Admin API returns connection refused / `000` | SSH tunnel not open | [Admin API unreachable](#admin-api-unreachable) |
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

Open an SSH tunnel to the admin API, then look up the key:

```bash
ssh -L 8088:127.0.0.1:8088 deploy@pkg.example.org -N &
curl -s http://127.0.0.1:8088/api/v1/keys | jq '.[] | select(.id == "<KEY>")'
```

If the key is absent, it was created after the last backup and lost during a keystore restore. Re-provision it:

```bash
curl -s -X POST http://127.0.0.1:8088/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "re-provisioned"}'
```

If the key is present but `"active": false`, it has been revoked. Issue a new key.

**Step 3 — Is the key scoped to the right component?**

Each key is scoped to a single component (as defined in `config/packyard.yml`). A `core` key cannot access `/rpm/minion/` — that is the expected behaviour, not a bug. Confirm the subscriber is using a key whose `component` matches the path they are requesting.

```bash
curl -s http://127.0.0.1:8088/api/v1/keys | jq '.[] | {id, component, label, active}'
```

**Step 4 — Is the component marked public?**

If a component has `visibility: public` in `config/packyard.yml`, the auth service allows requests to its paths without inspecting credentials. Keys for public components are valid but are not checked during auth — any request, authenticated or not, returns `200`. If a subscriber reports getting `401` on a public component path, confirm the auth service loaded the updated config:

```bash
docker compose logs auth | grep "loaded components"
# expect the public component to appear in the list
```

If the config was changed but the service was not restarted, restart it:

```bash
docker compose restart auth
```

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

## Admin API unreachable

The admin API listens on `127.0.0.1:8088` (loopback only). It is not reachable from outside the VM without an SSH tunnel.

**Open the tunnel:**

```bash
ssh -L 8088:127.0.0.1:8088 deploy@pkg.example.org -N &
```

**Verify:**

```bash
curl -s http://127.0.0.1:8088/api/v1/keys
# Expected: JSON array (empty if no keys)
# Connection refused → tunnel not open, or auth service down
```

Note: port 8088 serves plain HTTP (not HTTPS) — do not use `-k` or `https://`.

If the admin API returns `404` on port 443 (the public entrypoint), that is correct — the admin route is intentionally not exposed publicly.

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
