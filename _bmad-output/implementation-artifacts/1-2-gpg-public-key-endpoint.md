# Story 1.2: GPG Public Key Endpoint

Status: done

## Story

As a new subscriber,
I want to download the Meridian GPG public key without any credentials,
So that I can configure my package manager to verify package signatures before I have a subscription key.

## Acceptance Criteria

1. **Given** the packyard stack is running **when** `curl https://pkg.mdn.opennms.com/gpg/meridian.asc` is executed with no credentials **then** the response is HTTP 200 with the Meridian GPG public key in ASCII-armored format, and no `Authorization` header is required.

2. **Given** the repository **when** it is cloned **then** `static/gpg/meridian.asc` exists as a valid ASCII-armored GPG public key committed to version control — not generated at deploy time (I4 from architecture).

3. **Given** `scripts/health-check.sh` is executed **when** the script runs **then** it verifies `/gpg/meridian.asc` returns HTTP 200 and exits 0 on success, non-zero on failure (exit code 1).

4. **Given** the Traefik router for `/gpg/` **when** the router configuration is inspected **then** no `forwardAuth` middleware is attached — the route is explicitly unauthenticated (Category ② in architecture Traefik routing taxonomy).

## Tasks / Subtasks

- [x] Task 1: Commit Meridian GPG public key (AC: 2)
  - [x] 1.1 Obtain the OpenNMS Meridian GPG public key in ASCII-armored format — see Dev Notes for options (real key vs. development placeholder)
  - [x] 1.2 Write the key to `static/gpg/meridian.asc`; verify it passes `gpg --import --dry-run static/gpg/meridian.asc` (no errors)
  - [x] 1.3 Remove `static/gpg/.gitkeep` (replaced by `meridian.asc`)

- [x] Task 2: Create Traefik dynamic config for `/gpg/` router (AC: 1, 4)
  - [x] 2.1 Create `traefik/dynamic/routers-public.yml` — Category ② public-unauthenticated router: `websecure` entrypoint, `PathPrefix('/gpg/')`, no middleware, service pointing to `static:80`. See Dev Notes for exact YAML.
  - [x] 2.2 Verify Traefik loads config without errors: `docker compose logs traefik | grep -i err` returns no new ERR lines after `routers-public.yml` is placed

- [x] Task 3: Update health-check.sh to verify GPG endpoint (AC: 3)
  - [x] 3.1 Add HTTP 200 check for `/gpg/meridian.asc` after the container state loop — uses `curl -sf -k` against `https://${DOMAIN:-localhost}/gpg/meridian.asc`. See Dev Notes for exact implementation.

- [x] Task 4: Verify acceptance criteria (AC: 1, 2, 3, 4)
  - [x] 4.1 Confirm `static/gpg/meridian.asc` exists and is a valid PGP key (`gpg --import --dry-run`)
  - [x] 4.2 Confirm `traefik/dynamic/routers-public.yml` loads cleanly (no Traefik ERR logs)
  - [x] 4.3 Confirm `scripts/health-check.sh` exits 0 when stack is running and GPG endpoint returns 200

## Dev Notes

### Critical Constraints (read before implementing)

**Traefik v3 dynamic config must not be empty.** Story 1.1 learned that Traefik v3 rejects standalone empty YAML blocks (`http: {}`, `middlewares: {}`). `routers-public.yml` must have complete content. Do NOT create stub files.

**Category ② router must have zero middleware.** Architecture routing taxonomy:
- Category ① `websecure` + forwardAuth: `/rpm/`, `/deb/`, `/oci/` (Story 1.3)
- **Category ② `websecure` + no middleware: `/gpg/` ← this story**
- Category ③ `admin` (127.0.0.1:8443) + no middleware: `/api/v1/` (Story 2.x)

Attaching any middleware (including `packyard-auth`) to the `/gpg/` router breaks FR5 (public GPG key download without credentials).

**I4 constraint (architecture):** `static/gpg/meridian.asc` MUST be committed to VCS, not generated at deploy time. If the file is missing when a new subscriber tries to import the key, their onboarding fails silently.

### Traefik v3 Dynamic Config — `traefik/dynamic/routers-public.yml`

```yaml
http:
  routers:
    gpg-public:
      entryPoints:
        - websecure
      rule: "PathPrefix(`/gpg/`)"
      service: static-svc
      tls:
        certResolver: letsencrypt
      # No middlewares — Category ② public-unauthenticated (architecture routing taxonomy)

  services:
    static-svc:
      loadBalancer:
        servers:
          - url: "http://static:80"
```

**Traefik v3 rules (carry-over from Story 1.1):**
- `service:` field is required on every router definition — omitting it causes a startup error
- `@` in middleware names is reserved for cross-provider references — never use in custom names
- The `tls.certResolver: letsencrypt` refers to the resolver defined in `traefik/traefik.yml`
- In local dev without a real domain, Traefik starts normally but ACME challenge fails — this is expected

**Static service backing:**
The `static` service in `docker-compose.yml` is `nginx:alpine` serving `./static` at `/usr/share/nginx/html:ro`. The file `static/gpg/meridian.asc` is served at path `/gpg/meridian.asc`. The Docker DNS name is `static`, port 80 (nginx default).

**Story 1.3 will extend `routers-public.yml`** by adding the Category ① authenticated routers (`/rpm/`, `/deb/`, `/oci/`) and the `packyard-auth` forwardAuth middleware definition. Do NOT add those here.

### GPG Key — `static/gpg/meridian.asc`

**Option A — Use the real Meridian GPG key (production):**
Obtain the OpenNMS Meridian commercial signing key from the OpenNMS security/operations team. Export in ASCII-armored format:
```bash
gpg --armor --export <MERIDIAN_KEY_ID> > static/gpg/meridian.asc
```

**Option B — Generate a development/test key (initial development):**
If the real key is not yet available, generate a test key pair. This key will be used for development and replaced before production:
```bash
gpg --batch --gen-key <<'EOF'
%no-protection
Key-Type: RSA
Key-Length: 4096
Subkey-Type: RSA
Subkey-Length: 4096
Name-Real: OpenNMS Meridian Dev
Name-Email: meridian-dev@opennms.com
Expire-Date: 0
%commit
EOF
gpg --armor --export meridian-dev@opennms.com > static/gpg/meridian.asc
```

**Validation:** Before committing, confirm the file is valid:
```bash
gpg --import --dry-run static/gpg/meridian.asc
# Expected output: import would succeed without errors
```

The file must begin with `-----BEGIN PGP PUBLIC KEY BLOCK-----` and end with `-----END PGP PUBLIC KEY BLOCK-----`.

**Do NOT commit:**
- The private key (`.gpg`, `private.asc`, etc.) — it must never leave the key management system
- A binary keyring file — ASCII-armored (`--armor`) only

### Health Check Update — `scripts/health-check.sh`

Add the following block immediately after the container state loop and before the final status check:

```bash
# GPG endpoint availability check (Story 1.2)
GPG_DOMAIN="${DOMAIN:-localhost}"
echo "==> Checking GPG endpoint availability..."
HTTP_STATUS=$(curl -sf -k --max-time 10 --write-out '%{http_code}' --output /dev/null \
    "https://${GPG_DOMAIN}/gpg/meridian.asc" 2>/dev/null || echo "000")
if [[ "${HTTP_STATUS}" != "200" ]]; then
    echo "FAIL: /gpg/meridian.asc returned HTTP ${HTTP_STATUS} (expected 200)"
    FAILED=1
else
    echo "OK:   /gpg/meridian.asc returned HTTP 200"
fi
```

Notes:
- `DOMAIN` can be set in the caller's environment or loaded from `.env` before running the script
- `-k` allows the check to work locally with a self-signed or missing ACME cert
- `--max-time 10` prevents the script from hanging if Traefik is down
- `|| echo "000"` ensures curl connection failures set FAILED (not silently pass)

### Verification Steps

No unit tests (infrastructure config + static file). Acceptance verified by:

1. `gpg --import --dry-run static/gpg/meridian.asc` exits 0 (valid key) ✅ required
2. `docker compose logs traefik 2>&1 | grep -i 'level=error\|ERR'` — no new errors after placing `routers-public.yml` ✅ required
3. `curl -sf -k https://localhost/gpg/meridian.asc | head -1` returns `-----BEGIN PGP PUBLIC KEY BLOCK-----` ✅ required (requires running stack)
4. `bash scripts/health-check.sh` exits 0 with GPG endpoint showing `OK` ✅ required

**Note on local curl test:** Port 443 is bound to `0.0.0.0:443` on the host (from `docker-compose.yml`), so `curl -k https://localhost/gpg/meridian.asc` works locally once Traefik and the static container are running.

### Project Structure Notes

Files created/modified by this story:

```
static/gpg/
  .gitkeep           ← DELETE (replaced by meridian.asc)
  meridian.asc       ← NEW: ASCII-armored GPG public key
traefik/dynamic/
  .gitkeep           ← keep (Traefik ignores non-YAML files)
  routers-public.yml ← NEW: Category ② router for /gpg/
scripts/
  health-check.sh    ← MODIFIED: add HTTP 200 check for /gpg/
```

### References

- [Architecture — Traefik Routing Taxonomy] — Three categories; Category ② has no middleware
- [Architecture — I4] — GPG key in VCS + health check requirement
- [Architecture — Cross-Cutting Concern #2] — forwardAuth boundary: all paths except `/gpg/meridian.asc`
- [Architecture — External uptime check] — `/gpg/meridian.asc` is the canonical uptime monitoring URL
- [Epics — Story 1.2 ACs] — authoritative acceptance criteria
- [Story 1.1 Dev Notes — Traefik v3 Dynamic Configuration Discovery] — empty YAML rejection rule

## Dev Agent Record

### Agent Model Used

claude-sonnet-4-6 (dev-story workflow, 2026-03-30)

### Debug Log References

- Stack was not running at verification time — started with `docker compose up -d` before running curl/health-check tests. All services came up cleanly.
- Option B (dev key) chosen by Indigo: RSA-4096, `OpenNMS Meridian Dev / meridian-dev@opennms.com`. Key ID: `6DB8ACBC99143F98B02CDBCBDDF27FC6812E6214`. Replace with real Meridian signing key before production.

### Completion Notes List

- `static/gpg/meridian.asc` committed — dev RSA-4096 GPG public key; passes `gpg --import --dry-run` ✅
- `traefik/dynamic/routers-public.yml` created — Category ② router, `websecure` entrypoint, `PathPrefix('/gpg/')`, no middleware, `static-svc` → `http://static:80` ✅
- Traefik loaded config with 0 ERR lines (only expected env-var warnings for missing `.env`) ✅
- `curl -k https://localhost/gpg/meridian.asc` → HTTP 200, `-----BEGIN PGP PUBLIC KEY BLOCK-----` ✅
- `scripts/health-check.sh` updated — GPG endpoint check exits 0 with `OK: /gpg/meridian.asc returned HTTP 200` ✅
- `static/gpg/.gitkeep` deleted (replaced by `meridian.asc`) ✅

### File List

- `static/gpg/meridian.asc` (new)
- `traefik/dynamic/routers-public.yml` (new)
- `scripts/health-check.sh` (modified — added GPG endpoint HTTP check)
- `static/gpg/.gitkeep` (deleted)

### Review Findings

- [x] [Review][Patch] `static-svc` name will collide with Story 1.3 additions — Story 1.3 extends `routers-public.yml` with new services; a second `static-svc` definition causes Traefik to silently drop one with no error logged [`traefik/dynamic/routers-public.yml`]
- [x] [Review][Patch] `curl -sf` masks actual HTTP status code — `-f` flag causes curl to exit non-zero before `--write-out` captures the status; a 401 or 403 on the GPG route produces `""` rather than the actual code, making misconfiguration invisible [`scripts/health-check.sh`]
- [x] [Review][Defer] `docker compose ps` field-name case (`.Name`/`.State`) varies across Compose versions [`scripts/health-check.sh`] — deferred, pre-existing; currently works with installed version; portability fix in hardening pass
- [x] [Review][Defer] `PathPrefix('/gpg/')` routes all `/gpg/*` publicly, not just `meridian.asc` [`traefik/dynamic/routers-public.yml`] — deferred, intentional; only `meridian.asc` exists in dir today; no immediate risk
- [x] [Review][Defer] `-k` flag unconditional — masks TLS cert expiry failures in production monitoring [`scripts/health-check.sh`] — deferred, pre-existing; production hardening is Story 5.4 scope
- [x] [Review][Defer] Dev GPG key (`meridian-dev@opennms.com`) has no CI gate preventing it shipping to production — deferred, process/CI concern; replacement is tracked in story completion notes
- [x] [Review][Defer] `static` container has no Docker healthcheck (always reports `running`, never `healthy`) [`docker-compose.yml`] — deferred, pre-existing from Story 1.1

### Change Log

- 2026-03-30: Story 1.2 implemented — dev GPG key committed, Traefik Category ② public router for `/gpg/`, health-check updated with HTTP 200 verification
