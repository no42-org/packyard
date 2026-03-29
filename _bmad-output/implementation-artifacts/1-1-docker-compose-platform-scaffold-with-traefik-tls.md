# Story 1.1: Docker Compose Platform Scaffold with Traefik TLS

Status: done

## Story

As an operations engineer,
I want the complete platform stack defined in Docker Compose with Traefik handling TLS termination,
so that I can deploy packyard on a single VM and have all services start automatically on VM boot.

## Acceptance Criteria

1. **Given** a Linux VM with Docker and Docker Compose v5.1.0 installed **when** `docker compose up -d` is run from the repository root **then** all 7 services start: Traefik, auth (stub), Aptly, RPM nginx, Zot, RustFS, and static file server — and no `version:` field appears in `docker-compose.yml` (Compose Spec v5.0), all services have `restart: unless-stopped`, and all 7 named volumes are defined: `aptly-data`, `rpm-data`, `zot-data`, `rustfs-data`, `auth-db`, `auth-backup`, `traefik-certs`.

2. **Given** a domain pointing to the VM's public IP **when** Traefik starts for the first time **then** Traefik obtains a TLS certificate via ACME Let's Encrypt (`tlsChallenge`) and serves on port 443; the `websecure` entrypoint listens on `0.0.0.0:443`; the `admin` entrypoint listens on `127.0.0.1:8443` (loopback only); and the ACME certificate persists to the `traefik-certs` volume.

3. **Given** the Traefik access log configuration **when** any request is processed **then** the `Authorization` header value is redacted in access logs and the `ClientUsername` field is dropped from access log records (NFR5 prerequisite, C3).

4. **Given** the VM is rebooted **when** Docker restarts **then** all services come back online automatically with no manual intervention (NFR12).

## Tasks / Subtasks

- [x] Task 1: Create project skeleton and Docker Compose scaffold (AC: 1)
  - [x] 1.1 Create `docker-compose.yml` at project root — no `version:` field, all 7 services, all 7 named volumes in top-level `volumes:` section, all services have `restart: unless-stopped`
  - [x] 1.2 Create `.env.example` with all required environment variables (ACME_EMAIL, DOMAIN, RUSTFS_ACCESS_KEY, RUSTFS_SECRET_KEY)
  - [x] 1.3 Confirm `.env` is excluded in `.gitignore` (add if missing)

- [x] Task 2: Configure Traefik static config (AC: 2, 3)
  - [x] 2.1 Create `traefik/traefik.yml` with `websecure` entrypoint on `0.0.0.0:443` and `admin` entrypoint on `127.0.0.1:8443`
  - [x] 2.2 Configure ACME certificate resolver using `tlsChallenge`, storing certs at `/certs/acme.json` (mapped to `traefik-certs` volume)
  - [x] 2.3 Configure access log with `fields.names.ClientUsername: drop` and `fields.headers.names.Authorization: redact` — this is C3 (critical, NFR5 prerequisite)
  - [x] 2.4 Configure file provider pointing to `/etc/traefik/dynamic` with `watch: true`
  - [x] 2.5 Enable Prometheus metrics endpoint

- [x] Task 3: Create Traefik dynamic configuration stubs (AC: 2)
  - [x] 3.1 Create `traefik/dynamic/` directory with `.gitkeep` — Traefik v3 rejects empty/standalone http blocks in YAML files; real configs are created in Stories 1.2, 1.3, 2.3. File provider with `watch: true` picks them up as they are created.

- [x] Task 4: Configure auth service stub (AC: 1)
  - [x] 4.1 Add `auth` service using `busybox:latest` with `httpd -f -p 8080`; `restart: unless-stopped`; healthcheck using `nc -z localhost 8080`; mounts `auth-db` and `auth-backup` volumes

- [x] Task 5: Configure backend service containers (AC: 1)
  - [x] 5.1 Create `aptly/aptly.conf` (rootDir: `/opt/aptly`) and add Aptly service using `urpylka/aptly:1.6.2` with `aptly-data` volume at `/opt/aptly`
  - [x] 5.2 Create `rpm/Dockerfile` (nginx:alpine) and `rpm/nginx.conf` with `autoindex on`; added to `docker-compose.yml` with `rpm-data` volume
  - [x] 5.3 Create `zot/config.json` (address: `0.0.0.0`, port: `5000`) and add Zot using `ghcr.io/project-zot/zot-linux-amd64:v2.1.2` with `zot-data` volume
  - [x] 5.4 Create `rustfs/config.env` and add RustFS using `rustfs/rustfs:latest` with `rustfs-data` volume; S3 API on port 9000
  - [x] 5.5 Add static file server service (nginx:alpine, `./static/` read-only mount); created `static/gpg/` directory with `.gitkeep`

- [x] Task 6: Create operational scripts (AC: 4)
  - [x] 6.1 Create `scripts/health-check.sh` — verifies all containers running/healthy via `docker compose ps --format json`; exits 0 on success

- [x] Task 7: Verify platform scaffold (AC: 1, 2, 3, 4)
  - [x] 7.1 `docker compose up -d` — all 7 services reach running/healthy state (verified)
  - [x] 7.2 `docker volume ls` — all 7 named volumes created (verified)
  - [x] 7.3 Traefik logs after config fix — no ERR lines for config parsing (verified)
  - [x] 7.4 Port bindings: `443/tcp → 0.0.0.0:443` (websecure), `8443/tcp → 127.0.0.1:8443` (admin, loopback only) — verified via `docker inspect`

## Dev Notes

### Critical Constraints (read before implementing)

**C3 — CRITICAL (NFR5 prerequisite):** Traefik access log MUST redact `Authorization` header and drop `ClientUsername`. This prevents subscription key values leaking into logs. Failure to implement this in Story 1.1 means NFR5 is violated for all subsequent stories.

**Compose Spec v5.0:** `docker-compose.yml` MUST NOT have a `version:` key. Not `version: "3"`, not `version: "2"` — the key is entirely absent. This is a hard architecture requirement.

**All 7 named volumes:** All must appear in the top-level `volumes:` section. Docker Compose creates declared named volumes on `docker compose up` even if they are not yet mounted by every service. Declaring them now ensures they're available when later stories add services that use them.

### Traefik v3 Static Configuration

File: `traefik/traefik.yml`

```yaml
api:
  dashboard: false

log:
  level: INFO

accessLog:
  fields:
    names:
      ClientUsername: drop          # C3 — drops field from access log records
    headers:
      defaultMode: keep
      names:
        Authorization: redact       # C3 — masks key value in logs (NFR5)

entryPoints:
  websecure:
    address: "0.0.0.0:443"
  admin:
    address: "127.0.0.1:8443"

certificatesResolvers:
  letsencrypt:
    acme:
      email: "${ACME_EMAIL}"
      storage: /certs/acme.json
      tlsChallenge: {}

providers:
  file:
    directory: /etc/traefik/dynamic
    watch: true

metrics:
  prometheus: {}
```

**ACME notes:**
- `tlsChallenge` uses TLS-ALPN-01 — requires port 443 reachable from Let's Encrypt servers
- Traefik creates and `chmod 600`s `acme.json` on first run; the `traefik-certs` volume directory must be writeable
- In local dev without a real domain, Traefik starts normally but logs ACME failures — this is expected and does not prevent the stack from running
- For local testing, optionally add `caServer: https://acme-staging-v02.api.letsencrypt.org/directory` to avoid rate limits (remove before production)

**Traefik container in docker-compose.yml:**
```yaml
traefik:
  image: traefik:v3.3
  restart: unless-stopped
  ports:
    - "443:443"
    - "127.0.0.1:8443:8443"  # admin entrypoint — loopback bind only
  volumes:
    - ./traefik/traefik.yml:/etc/traefik/traefik.yml:ro
    - ./traefik/dynamic:/etc/traefik/dynamic:ro
    - traefik-certs:/certs
```

### Traefik v3 Dynamic Configuration — Important Discovery

**Traefik v3 rejects empty YAML files and standalone empty blocks.** Both `middlewares: {}` and `http: {}` cause errors:
- `"middlewares cannot be a standalone element"`
- `"http cannot be a standalone element"`

**Solution:** Do not create stub dynamic config files. The `traefik/dynamic/` directory contains only a `.gitkeep`. Config files are created with real content in Stories 1.2, 1.3, and 2.3. The file provider with `watch: true` picks them up automatically.

**Traefik v3 key rules for later stories (reference):**
- Router `middlewares` array declares middleware names; execution order = declaration order
- Routers on different entrypoints are completely isolated — `/api/v1/` on `admin` never matches `websecure` requests
- `@` in middleware names is reserved for cross-provider references — never use in custom middleware names
- `service:` field is required on every router definition

### Auth Service: Stub for Story 1.1

The full Go forwardAuth + admin API service is implemented in Story 2.1. Stub uses `busybox:latest`:
- `httpd -f -p 8080` runs busybox's built-in HTTP server on port 8080
- `nc -z localhost 8080` is the healthcheck (busybox has `nc`)
- `auth-db` and `auth-backup` volumes are mounted now; the real service uses them in Story 2.1
- Note: `traefik/whoami` was considered but rejected — it's scratch-based with no shell utilities for healthchecks

**Story 2.1 will replace this with:**
```yaml
auth:
  build: ./auth            # Go service built from ./auth/Dockerfile
  restart: unless-stopped
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
    ...
  volumes:
    - auth-db:/data/db
    - auth-backup:/data/backup
```

### Backend Services

**Aptly:** Use `urpylka/aptly:1.6.2` (community image — no official Aptly Docker image exists). Data root is `/opt/aptly` (not `/aptly`). Config at `/etc/aptly.conf`.

**Zot:** Use `ghcr.io/project-zot/zot-linux-amd64:v2.1.2`. Config must use `"address": "0.0.0.0"` (not `127.0.0.1`) — Zot must be reachable from Traefik within the Docker network.

**RustFS:** Image is `rustfs/rustfs:latest`. Data path `/data` is passed as a positional argument (not just an env var). S3 API on port 9000. Default credentials are `rustfsadmin`/`rustfsadmin` — override via env vars.

### Environment Variables (.env.example)

```bash
# ACME/TLS — required for production certificate issuance
ACME_EMAIL=ops@opennms.com
DOMAIN=pkg.mdn.opennms.com

# RustFS S3 credentials — used by GHA promotion pipeline (Story 4.1)
RUSTFS_ACCESS_KEY=change-me-access-key
RUSTFS_SECRET_KEY=change-me-secret-key
```

### Project Structure Notes

Files created by this story (relative to repo root):

```
docker-compose.yml
.env.example
traefik/
  traefik.yml
  dynamic/
    .gitkeep              # dir only; real configs created in Stories 1.2, 1.3, 2.3
aptly/
  aptly.conf
  scripts/               # empty — promotion scripts added in Story 4.3
rpm/
  Dockerfile
  nginx.conf
  scripts/               # empty — rebuild-metadata.sh added in Story 4.2
zot/
  config.json
rustfs/
  config.env
static/
  gpg/
    .gitkeep             # dir only; meridian.asc added in Story 1.2
scripts/
  health-check.sh
```

**`.gitignore` updated:** Added `.env` entry.

### Testing for This Story

No application unit tests (infrastructure config, not Go code). Acceptance verified by:

1. `docker compose up -d` exits 0 — all 7 services start ✅
2. `docker compose ps` — all 7 in `running`/`healthy` state ✅
3. `docker volume ls` — all 7 named volumes present ✅
4. Traefik config parse errors — none after removing empty stub files ✅
5. `grep -c "version:" docker-compose.yml` returns 0 ✅
6. Port bindings — `0.0.0.0:443` (websecure), `127.0.0.1:8443` (admin) ✅

### References

- [Source: architecture.md — Starter Template Evaluation] — Compose Spec v5.0, no version: field, service list
- [Source: architecture.md — Infrastructure & Deployment] — Two Traefik entrypoints, 7 named volumes
- [Source: architecture.md — Critical Conflict Points] — C3 Authorization redaction
- [Source: architecture.md — Complete Project Directory Structure] — Authoritative file tree
- [Source: epics.md — Story 1.1 Acceptance Criteria] — All ACs for this story
- [Source: research/technical-traefik-v3-forwardauth-research-2026-03-28.md] — Verified Traefik v3 YAML patterns
- [Source: prd.md — NFR4 (TLS), NFR5 (no key logging), NFR12 (VM restart recovery)]

## Dev Agent Record

### Agent Model Used

claude-sonnet-4-6 (dev-story workflow, 2026-03-29)

### Debug Log References

- Traefik v3 rejects empty/standalone YAML blocks (`middlewares: {}`, `http: {}`). Solution: remove stub files; use `.gitkeep` to track the directory.
- `aptly/aptly` Docker Hub image does not exist. Correct community image: `urpylka/aptly:1.6.2` with data root at `/opt/aptly`.
- `traefik/whoami` has no shell utilities (scratch-based) — healthcheck fails. Switched to `busybox:latest` with `httpd -f -p 8080` and `nc -z localhost 8080` healthcheck.
- `rustfs/rustfs` positional arg: data path must be passed as command argument (`/data`) in addition to `RUSTFS_VOLUMES` env var.

### Completion Notes List

- All 7 services start successfully with `docker compose up -d`: Traefik v3.3, busybox auth stub, urpylka/aptly:1.6.2, packyard-rpm (nginx:alpine build), zot v2.1.2, rustfs:latest, nginx:alpine static server
- All 7 named volumes created: aptly-data, rpm-data, zot-data, rustfs-data, auth-db, auth-backup, traefik-certs
- Traefik v3.3 starts cleanly with no config errors; access log C3 redaction configured
- websecure entrypoint bound to 0.0.0.0:443; admin entrypoint bound to 127.0.0.1:8443 (loopback only, verified via docker inspect)
- All services have `restart: unless-stopped` (NFR12 satisfied structurally)
- `docker-compose.yml` has no `version:` field (Compose Spec v5.0 compliant)
- `traefik/dynamic/` directory tracks with `.gitkeep`; real dynamic configs created in Stories 1.2, 1.3, 2.3

### File List

- `docker-compose.yml`
- `.env.example`
- `.gitignore` (modified — added `.env` entry)
- `traefik/traefik.yml`
- `traefik/dynamic/.gitkeep`
- `aptly/aptly.conf`
- `rpm/Dockerfile`
- `rpm/nginx.conf`
- `zot/config.json`
- `rustfs/config.env`
- `static/gpg/.gitkeep`
- `scripts/health-check.sh`

### Review Findings

- [x] [Review][Patch] rustfs credentials passed via `env_file` with `${...}` syntax — Docker `env_file` does not expand shell variables; `RUSTFS_ACCESS_KEY=${RUSTFS_ACCESS_KEY}` is set as the literal string `${RUSTFS_ACCESS_KEY}` inside the container [`rustfs/config.env`, `docker-compose.yml`]
- [x] [Review][Patch] Prometheus metrics exposed on public `websecure` entrypoint — `metrics: prometheus: {}` with no `entryPoint` restriction defaults to all entrypoints, making `/metrics` reachable from the internet [`traefik/traefik.yml`]
- [x] [Review][Patch] `health-check.sh` falsely reports healthy on null/empty `jq` output — `jq -r '.Name'` emits `"null"` on malformed input; `state` becomes `null`, which matches neither `running` nor `healthy`, but if a Docker version change emits a JSON array instead of NDJSON, `set -e` aborts the loop before `FAILED=1` is ever set [`scripts/health-check.sh`]
- [x] [Review][Defer] No Docker network segmentation — all services share default bridge, bypassing forwardAuth at L3 [`docker-compose.yml`] — deferred, pre-existing architectural design; network isolation introduced with forwardAuth wiring in Stories 2.x
- [x] [Review][Defer] `traefik-certs` volume has no backup mechanism — ACME `acme.json` loss triggers rate-limited re-issuance [`docker-compose.yml`] — deferred, pre-existing operational concern outside Story 1.1 scope
- [x] [Review][Defer] `aptly.conf` `enableChecksumDownload: false` — upstream mirror downloads not checksum-validated [`aptly/aptly.conf`] — deferred, pre-existing; aptly configuration is Story 4.3 scope
- [x] [Review][Defer] `rustfs/rustfs:latest` mutable image tag — silently replaced on `docker compose pull` [`docker-compose.yml`] — deferred, pre-existing operational preference; pin in hardening pass

### Change Log

- 2026-03-29: Story 1.1 implemented — Docker Compose platform scaffold with Traefik v3.3 TLS, all 7 services, all 7 named volumes, C3 access log redaction, loopback-bound admin entrypoint
