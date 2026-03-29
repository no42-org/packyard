---
stepsCompleted: [1, 2, 3, 4, 5, 6, 7, 8]
inputDocuments:
  - '_bmad-output/planning-artifacts/prd.md'
  - '_bmad-output/brainstorming/brainstorming-session-2026-03-27-1630.md'
workflowType: 'architecture'
lastStep: 8
status: 'complete'
completedAt: '2026-03-28'
project_name: 'packyard'
user_name: 'Indigo'
date: '2026-03-28'
---

# Architecture Decision Document

_This document builds collaboratively through step-by-step discovery. Sections are appended as we work through each architectural decision together._

## Project Context Analysis

### Requirements Overview

**Functional Requirements (34 total across 6 areas):**

- **Package Access & Delivery (FR1–FR7):** Serve RPM, DEB, and OCI artifacts to subscribers using standard tooling (dnf, apt, docker/containerd, cosign). OCI must support multi-arch image index (x86_64 + ARM64). GPG public key must be accessible without authentication.

- **Subscription Key Authentication (FR8–FR13):** HTTP Basic Auth on all serving endpoints. Component-scoped enforcement (a Core key cannot access Minion or Sentinel paths). Instant revocation with no server restart. Credentials must be embeddable in OS package manager config files and Kubernetes pull secrets.

- **Key Lifecycle Management (FR14–FR20):** Admin API v1 for create/revoke/list/inspect operations. Structured error responses. Filtering by component. Usage count tracking per key.

- **Package Publishing (FR21–FR27):** Staging to object storage, manual promotion trigger (workflow_dispatch), GPG signing of RPM/DEB, cosign signing of OCI, serialised RPM metadata rebuild, immutable Aptly snapshot per Meridian release, coexisting year-versioned repositories.

- **Repository Structure (FR28–FR31):** Permanently stable year-versioned URLs, distinct paths per OS target (el8/el9/el10) and per Debian distro (bookworm/trixie/jammy/noble), always-consistent metadata.

- **Platform Operations (FR32–FR34):** Single-VM deployment via container tooling, cloud-agnostic (Azure/AWS/KVM), admin API network-isolated from public entrypoint.

**Non-Functional Requirements (17 total — architecturally significant):**

- **Performance:** forwardAuth ≤ 100ms per validation; metadata endpoints ≤ 2s TTFB; no application-layer download throttling.
- **Security:** TLS-only (no HTTP fallback); no key values in logs; ephemeral signing keys (GHA secrets only, never on disk); ACME-compatible TLS (no cert pinning); admin API network-isolated.
- **Reliability:** 99.9% monthly availability; consistent metadata at all times (no partial repodata visible to package managers); fail-closed forwardAuth (HTTP 503 on auth service failure, never HTTP 200); auto-restart recovery.
- **Scalability:** 500 concurrent subscribers without degradation; bounded Aptly/createrepo_c storage growth.
- **Integration:** OCI Distribution Spec v1; createrepo_c-compatible repomd.xml; standard Debian archive format; HTTP Basic Auth as sole auth mechanism.

**Scale & Complexity:**

- Primary domain: API backend / infrastructure serving (no UI)
- Complexity level: medium — multi-format standards compliance, security-critical auth layer, but low concurrency at launch and no real-time features
- Estimated deployable components: 7–8

### Technical Constraints & Dependencies

- **No cloud-specific services** — must run on standard Linux VMs across Azure, AWS, and KVM without provider dependencies (FR33)
- **HTTP Basic Auth only** on serving endpoints — no custom headers, bearer tokens, or cookies (NFR16); required for compatibility with dnf/apt/docker/kubectl
- **OCI Distribution Spec v1** — Zot exposes `/v2/` API; Traefik PathStrip middleware removes `/oci/` prefix before forwarding
- **ACME-compatible TLS** — no certificate pinning; must work behind enterprise TLS inspection proxies (NFR7, NFR17)
- **Single-VM deployable** — all services must be orchestratable via Docker Compose or equivalent on one host (FR32)
- **createrepo_c has no file locking** — serialisation must be enforced externally at the GHA promotion pipeline level; concurrent RPM metadata rebuilds will corrupt `repomd.xml`
- **Ephemeral signing** — GPG and cosign private keys must never be written to disk on any server; all signing is ephemeral within GHA workflow runs (NFR6)

### Cross-Cutting Concerns Identified

1. **TLS termination** — handled by Traefik for all endpoints; ACME certificate issuance and renewal is a platform-level concern
2. **Authentication boundary** — forwardAuth middleware applied globally to all paths except `/gpg/meridian.asc`; admin API has separate network entrypoint with no forwardAuth (internal trust only)
3. **Network segmentation** — three logical networks: public serving, internal admin, internal forwardAuth callback (Traefik → forwardAuth service)
4. **Secrets management** — subscription keys (key store), GPG keys (GHA secrets), cosign keys (GHA secrets), RustFS credentials (GHA secrets / env)
5. **Storage lifecycle** — Aptly snapshot retention policy, createrepo_c repo tree growth, RustFS staging bucket cleanup after successful promotion
6. **Observability** — uptime measurement against 99.9% SLA, key usage count increments per forwardAuth validation, promotion pipeline audit trail

---

## Starter Template Evaluation

### Primary Technology Domain

Infrastructure backend — no UI, no web framework. The platform is a composed
set of off-the-shelf services (Traefik, Aptly, createrepo_c, Zot, RustFS) with
one custom-built HTTP service (forwardAuth + admin API). Starter evaluation
focuses on the deployment scaffold and the custom service scaffold only.

### Starter Options Considered

| Option | Fit | Notes |
|---|---|---|
| Web app starters (Next.js, Remix, etc.) | Not applicable | No UI |
| Generic Go service scaffold (stdlib) | Good for MVP | Minimal deps, testable |
| chi router scaffold | Good | Lightweight, stdlib-compatible routing |
| Docker Compose project scaffold | Required | The primary deployment unit |

### Selected Foundation: Docker Compose + Go stdlib/chi

**Rationale:**
- Docker Compose v5.1.0 orchestrates all 7–8 services on a single VM (FR32)
- No `version:` field in docker-compose.yml (Compose Spec v5.0 convention)
- Go 1.26.1 for the forwardAuth + admin API service — matches team capability,
  produces a single static binary, minimal runtime overhead (meets NFR1 100ms)
- Standard library `net/http` with `chi` router — no heavy framework overhead for
  a service with ~6 endpoints; chi provides clean middleware chaining for logging,
  request IDs, and the forwardAuth endpoint without transitive dependencies
- Pure-Go SQLite via `modernc.org/sqlite` avoids CGo build complexity in
  containers; abstracted behind a `KeyStore` interface for Phase 2 migration

**Initialization:**

```bash
# Docker Compose scaffold
mkdir -p packyard/{traefik,aptly,rpm,zot,rustfs,auth}
touch packyard/docker-compose.yml
touch packyard/traefik/traefik.yml
touch packyard/.env.example

# Go service scaffold (forwardAuth + admin API)
mkdir -p packyard/auth/cmd/server
mkdir -p packyard/auth/internal/{handler,store,middleware}
cd packyard/auth && go mod init github.com/opennms/packyard-auth
go get github.com/go-chi/chi/v5
go get modernc.org/sqlite
```

**Architectural Decisions Established by This Foundation:**

**Language & Runtime:**
Go 1.26.1 — single static binary, containerised; no external runtime dependency.

**Routing:**
`chi` v5 — stdlib-compatible, middleware-friendly. Routes:
`POST/GET/DELETE /api/v1/keys`, `GET /auth` (forwardAuth endpoint).

**Storage — KeyStore Interface:**
The subscription key store is accessed exclusively via a `KeyStore` interface.
SQLite (`modernc.org/sqlite`, pure Go, no CGo) backs it for MVP. The interface
contract is fixed from day one — no handler code touches SQLite directly:

```go
type KeyStore interface {
    CreateKey(ctx context.Context, component, label string, expiresAt *time.Time) (*Key, error)
    GetByValue(ctx context.Context, value string) (*Key, error)
    ListKeys(ctx context.Context, component string) ([]*Key, error)
    RevokeKey(ctx context.Context, id string) error
    IncrementUsage(ctx context.Context, id string) error
}
```

Swapping to Postgres or Redis in Phase 2 is a single implementation file change
plus a connection string environment variable — zero handler changes required.

**Deployment:**
Docker Compose v5.1.0 (Compose Spec v5.0). No `version:` field in YAML.
Single `docker-compose.yml` at project root; per-service config in subdirectories.

The auth container **must** include a restart policy and health check from day
one — not a hardening afterthought. This is what delivers NFR11 (fail-closed)
at the infrastructure level:

```yaml
# docker-compose.yml (auth service excerpt)
auth:
  build: ./auth
  restart: unless-stopped
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:8080/health"]
    interval: 10s
    timeout: 3s
    retries: 3
    start_period: 5s
```

Traefik's `healthCheck` on the auth service backend means that if the auth
container fails its health check, Traefik returns HTTP 503 to all package manager
requests — never HTTP 200. NFR11 is satisfied structurally, not procedurally.

**Testing:**
Go standard library `testing` package. Integration tests use a real SQLite
in-memory database — no mocks for the key store (real interface, real SQL).

**Build:**
`CGO_ENABLED=0 go build -o /auth ./cmd/server` — produces a static binary
suitable for a `FROM gcr.io/distroless/static` container image.

**Note:** Epic 1, Story 1 should initialise the Docker Compose scaffold and verify
that Traefik starts and routes a health check request correctly. The Go service
scaffold (including `KeyStore` interface definition) is Epic 3, Story 1.

---

## Core Architectural Decisions

### Decision Priority Analysis

**Critical Decisions (block implementation):**
- Key value format — affects key generation in admin API (Story 1 of auth epic)
- forwardAuth caching strategy — affects auth service design directly
- Traefik admin entrypoint binding — affects Docker Compose scaffold (Epic 1)

**Important Decisions (shape architecture):**
- TLS certificate issuance method
- Structured logging approach
- Observability stack
- Key store backup strategy

**Deferred Decisions (post-MVP):**
- OpenAPI spec generation
- In-process auth cache (only if SQLite benchmarks show it's needed)
- Migration from SQLite to Postgres/Redis (Phase 2)

### Data Architecture

**Subscription key value format:** `crypto/rand` 32 bytes → 64-char lowercase hex
string. Not UUID — subscription keys are credentials and must be high-entropy
opaque strings with no guessable structure.

**forwardAuth validation caching:** None for MVP. Always read from SQLite directly.
SQLite reads are ~microseconds on SSD; 500 concurrent subscribers is well within
the 100ms forwardAuth budget (NFR1). In-process caching introduces stale-revocation
risk that violates FR11 (instant revocation). Revisit only if benchmarks show
SQLite is the bottleneck.

**Aptly snapshot retention:** 2 most recent published snapshots per
`{year} × {component} × {format}` combination. Current snapshot + one rollback.
Older snapshots archived to cold path — never deleted, disk is cheap.

**RustFS staging bucket structure:** `/{component}/{year}/{format}/{os-arch}/`
mirrors the serving URL path structure. Example:
`/core/2025/rpm/el9-x86_64/opennms-core-2025.0.rpm`. Obvious staging-to-production
mapping reduces operator error during promotion.

### Authentication & Security

**TLS certificate issuance:** Traefik ACME with Let's Encrypt, HTTP-01 challenge.
Cloud-agnostic, auto-renewal, zero operator effort post-setup. DNS-01 as fallback
if the host is behind a load balancer blocking HTTP-01 port 80.

**Admin API network isolation:** Second Traefik entrypoint bound to
`127.0.0.1:8443` (loopback only). Operators access via SSH tunnel or jump host.
Simple, effective, no complex Docker network topology required. Achieves NFR8/FR34
structurally.

**Key store backup:** Daily `sqlite3 .backup` to a dedicated `auth-backup` Docker
volume, 7-day retention. Executed by a scheduled service or host cron. Closes the
backup/restore gap identified in the implementation readiness report. Recovery
procedure: stop auth container, restore backup file, restart container.

### API & Communication Patterns

**Structured logging:** Go stdlib `log/slog` (Go 1.21+, fully supported in 1.26).
JSON output handler, no external dependencies. Subscription key values are never
logged — `Authorization` header value is not logged at any level (NFR5). Only the
key ID (after successful lookup) may appear in log output.

**Admin API documentation:** Architecture document is the specification for MVP.
No OpenAPI spec required for 5 internal endpoints. Generate OpenAPI in Phase 2
if integration partners require it.

### Frontend Architecture

Not applicable. packyard has no user-facing UI.

### Infrastructure & Deployment

**Observability:**
- Internal: Traefik Prometheus metrics endpoint (enabled in `traefik.yml`) —
  request counts, latency histograms, error rates per backend service
- External uptime: HTTP check against `pkg.mdn.opennms.com/gpg/meridian.asc`
  (public endpoint, no auth) from existing team monitoring infrastructure.
  This endpoint being reachable validates the full serving stack (TLS, Traefik,
  static file serving). Measures 99.9% SLA target (NFR9).

**Deployment topology:**
- Single Docker Compose file at project root
- Two Traefik entrypoints: `websecure` (0.0.0.0:443, public) and
  `admin` (127.0.0.1:8443, loopback-only)
- All services on a shared Docker bridge network
- Named volumes: `aptly-data`, `rpm-data`, `zot-data`, `rustfs-data`,
  `auth-db`, `auth-backup`, `traefik-certs`

### Decision Impact Analysis

**Implementation sequence implications:**
1. Docker Compose scaffold + Traefik entrypoints (Epic 1) — must precede all other epics
2. Auth service + KeyStore interface + SQLite (Epic 3) — must precede key management API
3. Aptly + createrepo_c + Zot backends (Epic 2) — can proceed in parallel with Epic 3
4. GHA promotion pipeline (Epic 4) — depends on all three backends being deployed
5. Signing + hardening (Epic 5) — depends on promotion pipeline

**Cross-component dependencies:**
- forwardAuth service is on the critical path for every subscriber-facing operation
- Traefik ACME config must be correct before any subscriber can reach any endpoint
- `KeyStore` interface is the contract between Epic 3 (auth service) and Phase 2
  (subscription management integration) — must not be violated
- RustFS staging bucket structure must match GHA promotion workflow path assumptions

---

## Implementation Patterns & Consistency Rules

### Critical Conflict Points Identified

7 areas where AI agents could make incompatible choices without explicit rules:
JSON field naming, Go package layout, error propagation, middleware chain order,
SQLite schema naming, HTTP status code usage, and log sanitization.

### Naming Patterns

**SQLite Schema (snake_case throughout):**
- Tables: lowercase singular — `subscription_key`, not `Keys` or `subscription_keys`
- Columns: `snake_case` — `created_at`, `usage_count`, `expires_at`
- Primary key: `id TEXT PRIMARY KEY` (stores the 64-char hex key value directly)
- Indexes: `idx_{table}_{column}` — e.g., `idx_subscription_key_component`

**API JSON field naming: `snake_case`**
All admin API request and response bodies use `snake_case` field names.
This matches the PRD-specified schemas and is consistent with standard Go JSON
serialisation using struct tags:
```go
type Key struct {
    ID         string     `json:"id"`
    Component  string     `json:"component"`
    Active     bool       `json:"active"`
    Label      string     `json:"label"`
    CreatedAt  time.Time  `json:"created_at"`
    ExpiresAt  *time.Time `json:"expires_at"`
    UsageCount int64      `json:"usage_count"`
}
```

**URL path parameters: `{id}` style (chi convention)**
`r.Delete("/api/v1/keys/{id}", handler.RevokeKey)` — use chi's `chi.URLParam(r, "id")`.
Never `:id` (Express-style), never query parameters for resource identity.

**Query parameters: `snake_case`**
`?component=core` — not `?componentName=core` or `?comp=core`.

**Go code naming: standard Go conventions**
- Exported identifiers: `PascalCase` (`KeyStore`, `CreateKey`, `ForwardAuthHandler`)
- Unexported identifiers: `camelCase` (`parseComponent`, `extractKeyValue`)
- Files: `snake_case.go` (`forward_auth.go`, `key_store.go`, `create_key.go`)
- Packages: lowercase single word (`handler`, `store`, `middleware`)

### Structure Patterns

**Go package layout (enforced):**
```
auth/
├── cmd/server/main.go          # binary entry point only; no business logic
├── internal/
│   ├── handler/                # HTTP handlers (one file per route group)
│   │   ├── keys.go             # admin API: POST/GET/DELETE /api/v1/keys
│   │   └── forward_auth.go     # forwardAuth: GET /auth
│   ├── store/
│   │   ├── store.go            # KeyStore interface definition
│   │   ├── sqlite.go           # SQLite implementation
│   │   └── sqlite_test.go      # integration tests (real SQLite in-memory)
│   └── middleware/
│       └── logging.go          # slog request logger (sanitizes Authorization header)
└── go.mod
```

**Tests: co-located `_test.go` files** — never a separate `tests/` directory.
Integration tests for the store use `modernc.org/sqlite` in-memory mode:
`db, _ := sql.Open("sqlite", ":memory:")`. No mocks for the `KeyStore` — test
against the real SQLite implementation.

**The `KeyStore` interface lives in `internal/store/store.go`** — not in `handler/`
or `cmd/`. Handlers import `store.KeyStore`, never a concrete implementation.

### Format Patterns

**API response: direct JSON, no wrapper envelope**
Admin API returns the resource directly — not `{"data": {...}}`. Successful
`POST /api/v1/keys` returns `201 Created` with the `Key` object body. Successful
`DELETE /api/v1/keys/{id}` returns `204 No Content` with no body.

**Error responses: Code + Message (admin API only)**
```json
{
  "code": "KEY_SCOPE_MISMATCH",
  "message": "Key 'abc123...' is scoped to 'core' but requested path requires 'minion' scope",
  "component_requested": "minion",
  "key_scope": "core"
}
```
Serving endpoints (forwardAuth-rejected requests) return bare HTTP status with
no body — package managers do not parse response bodies on auth failure.

**Date/time: RFC3339 strings in JSON**
`"created_at": "2025-01-15T10:00:00Z"` — always UTC, always RFC3339.
`time.Time` fields serialise via `json:"...",omitempty` only for optional fields
(`expires_at`). Required fields never use `omitempty`.

**HTTP status codes (canonical mapping):**

| Scenario | Code |
|---|---|
| Key created | 201 Created |
| Key retrieved / listed | 200 OK |
| Key revoked | 204 No Content |
| Invalid request body | 400 Bad Request |
| Auth failure (serving endpoints) | 401 Unauthorized |
| Key not found (admin API) | 404 Not Found |
| forwardAuth service unavailable | 503 Service Unavailable |
| Unexpected error | 500 Internal Server Error |

### Process Patterns

**Error propagation: errors wrap at store layer, unwrap at handler layer**
Store methods return `fmt.Errorf("get key: %w", err)` — never log errors in
the store. Handlers call `errors.Is(err, store.ErrNotFound)` to branch on
sentinel errors. Sentinel errors live in `internal/store/store.go`:
```go
var (
    ErrNotFound = errors.New("key not found")
    ErrRevoked  = errors.New("key is revoked")
)
```

**Log sanitization: Authorization header is never logged**
The request logger middleware logs method, path, status, and latency.
It explicitly omits `Authorization`, `X-Forwarded-For`, and any header
containing key material. Only the key `id` (not value) may appear in logs
after a successful `GetByValue` lookup.

**Middleware chain order (chi):**
```
Router → RequestID → Logger → (route-specific) → Handler
```
The forwardAuth endpoint (`GET /auth`) does not apply the admin API middleware.
The admin API routes (`/api/v1/`) do not apply forwardAuth.

**forwardAuth response contract (never deviate):**
- Key valid + component matches path → `200 OK`, empty body
- Key invalid / revoked / scope mismatch → `401 Unauthorized`, empty body
- Store error / service panic → `503 Service Unavailable`, empty body
- Never return `200 OK` on any error condition (NFR11)

### Enforcement Guidelines

**All AI agents implementing the auth service MUST:**
- Never import a concrete SQLite type outside `internal/store/sqlite.go`
- Never log the value of the `Authorization` header or subscription key string
- Never return HTTP 200 from the `GET /auth` endpoint on any failure condition
- Always use `chi.URLParam(r, "id")` for path parameters — never `r.URL.Query()`
- Always use `time.RFC3339` for JSON date serialisation
- Always use `snake_case` for JSON field names (struct tags)
- Always write tests against the real SQLite implementation — no mocks for `KeyStore`

**Anti-patterns (explicitly forbidden):**
- `json:"userId"` — must be `json:"user_id"`
- `return nil, fmt.Errorf("not found")` — must use sentinel `store.ErrNotFound`
- Logging `r.Header.Get("Authorization")` at any log level
- Returning HTTP 200 with `{"error": "..."}` — errors use non-2xx status codes
- Calling `store.NewSQLiteStore()` from `handler/` packages

---

## Project Structure & Boundaries

### Complete Project Directory Structure

```
packyard/
├── docker-compose.yml              # FR32: single-VM orchestration of all services
├── .env.example                    # Environment variable template (committed)
├── .env                            # Local/production values (gitignored)
├── .gitignore
│
├── traefik/                        # Traefik ingress, TLS, routing
│   ├── traefik.yml                 # Static config: entrypoints, ACME, Prometheus metrics
│   └── dynamic/
│       ├── middlewares.yml         # forwardAuth middleware definition
│       ├── routers-public.yml      # websecure (0.0.0.0:443) route rules + backends
│       └── routers-admin.yml       # admin (127.0.0.1:8443) route rules
│
├── auth/                           # Custom forwardAuth + admin API service (Go)
│   ├── Dockerfile                  # FROM gcr.io/distroless/static; CGO_ENABLED=0 build
│   ├── go.mod
│   ├── go.sum
│   ├── cmd/
│   │   └── server/
│   │       └── main.go             # Entry point: wire router, store, start server
│   └── internal/
│       ├── handler/
│       │   ├── keys.go             # POST/GET/DELETE /api/v1/keys (FR14–FR20)
│       │   └── forward_auth.go     # GET /auth — forwardAuth endpoint (FR8–FR13)
│       ├── store/
│       │   ├── store.go            # KeyStore interface + ErrNotFound/ErrRevoked
│       │   ├── sqlite.go           # SQLite implementation (modernc.org/sqlite)
│       │   └── sqlite_test.go      # Integration tests (in-memory SQLite)
│       └── middleware/
│           └── logging.go          # slog JSON logger; sanitizes Authorization header
│
├── aptly/                          # Debian repository manager
│   ├── aptly.conf                  # Aptly storage root, S3 config (optional)
│   └── scripts/
│       ├── create-snapshot.sh      # Create immutable point-in-time snapshot (FR26)
│       ├── publish-snapshot.sh     # Publish snapshot to /deb/{component}/{year}/
│       └── prune-snapshots.sh      # Retention policy: keep 2 per year×component×format
│
├── rpm/                            # RPM metadata + static file server
│   ├── Dockerfile                  # nginx for static file serving of RPM trees
│   ├── nginx.conf
│   └── scripts/
│       ├── add-package.sh          # Move RPM to tree + invoke rebuild-metadata.sh
│       └── rebuild-metadata.sh     # createrepo_c with serialisation (FR25)
│
├── zot/                            # OCI registry
│   └── config.json                 # Zot: storage path, auth passthrough, GC policy
│
├── rustfs/                         # S3-compatible staging object storage
│   └── config.env                  # RustFS root path, credentials, bucket config
│
├── static/                         # Static file serving (GPG public key)
│   └── gpg/
│       └── meridian.asc            # Meridian GPG public key — served without auth (FR5)
│
├── scripts/                        # Operational scripts
│   ├── backup-keystore.sh          # Daily sqlite3 .backup to auth-backup volume
│   └── health-check.sh             # Verify all containers healthy + Traefik routes
│
└── .github/
    └── workflows/
        ├── promote-meridian.yml    # FR22: workflow_dispatch (inputs: component, year)
        └── sign-and-publish.yml    # FR23/FR24: GPG sign RPM/DEB + cosign sign OCI
```

**Named Docker volumes:**
```
aptly-data      → Aptly database + published DEB repos
rpm-data        → createrepo_c trees (/component/year/os-arch/)
zot-data        → Zot blob storage + OCI manifests
rustfs-data     → RustFS staging bucket data
auth-db         → SQLite database file (subscription_key table)
auth-backup     → Daily SQLite backups, 7-day retention
traefik-certs   → Let's Encrypt ACME certificate store
```

### Architectural Boundaries

**API Boundaries:**

| Boundary | Address | Who reaches it |
|---|---|---|
| Public serving | `pkg.mdn.opennms.com:443` | All subscribers (RPM, DEB, OCI, GPG) |
| Admin API | `127.0.0.1:8443` | Operations staff via SSH tunnel/jump host only |
| forwardAuth callback | `auth:8080/auth` (Docker internal) | Traefik only — never external |
| RustFS staging | `rustfs:9000` (Docker internal) | GHA promotion workflows only |

**Component Boundaries:**

- **Traefik → auth service:** HTTP `GET /auth` with `Authorization: Basic` and
  `X-Forwarded-Uri` headers. Auth service returns 200/401/503 — no body.
- **Traefik → backends:** Proxied HTTP after forwardAuth approves. PathStrip
  middleware removes `/oci/` prefix before forwarding to Zot.
- **GHA → RustFS:** S3 API (PutObject/GetObject) for staging unsigned artifacts.
- **GHA → backends:** Direct write — Aptly API for DEB, shell script for RPM,
  `crane push` / `cosign` for OCI.

**Data Boundaries:**

- **Auth service ↔ SQLite:** `KeyStore` interface only. No direct SQL outside
  `internal/store/sqlite.go`.
- **Aptly ↔ disk:** Aptly manages its own database; scripts interact only via
  the `aptly` CLI — never by modifying the database directly.
- **createrepo_c ↔ RPM tree:** Scripts own the RPM file tree under the `rpm-data`
  volume. GHA writes packages; scripts run `createrepo_c --update` with a
  serialisation lock (GHA concurrency group per component+OS).

### Requirements to Structure Mapping

| FR Category | Primary Location |
|---|---|
| FR1–FR2 RPM/DEB download | `traefik/dynamic/`, `rpm/`, `aptly/` |
| FR3–FR4 OCI pull + cosign verify | `zot/`, `.github/workflows/sign-and-publish.yml` |
| FR5–FR6 GPG key + auto-verify | `static/gpg/meridian.asc`, `rpm/scripts/` |
| FR7 Multi-arch OCI | `zot/config.json`, GHA OCI push (image index) |
| FR8–FR11 HTTP Basic Auth + forwardAuth | `auth/internal/handler/forward_auth.go` |
| FR12–FR13 Package manager + K8s credential | `traefik/dynamic/middlewares.yml` (contract only) |
| FR14–FR20 Admin API key CRUD | `auth/internal/handler/keys.go` |
| FR21–FR22 Stage + promote | `.github/workflows/promote-meridian.yml` |
| FR23–FR24 GPG/cosign signing | `.github/workflows/sign-and-publish.yml` |
| FR25 RPM serialisation | `rpm/scripts/rebuild-metadata.sh` + GHA concurrency group |
| FR26 Aptly snapshot | `aptly/scripts/create-snapshot.sh` |
| FR27–FR28 Year-versioned coexisting repos | `aptly/`, `rpm/` directory layout |
| FR29–FR30 OS/distro distinct paths | `rpm/` tree layout, Aptly component config |
| FR31 Consistent metadata | createrepo_c + Aptly transactional publish model |
| FR32 Single-VM deploy | `docker-compose.yml` |
| FR33 Cloud-agnostic | Docker Compose + named volumes (no cloud-specific APIs) |
| FR34 Admin API isolation | `traefik/traefik.yml` (127.0.0.1:8443 binding) |

### Data Flow

**Subscriber package download:**
```
Subscriber → pkg.mdn.opennms.com:443
  → Traefik (TLS termination, path routing)
  → forwardAuth: auth:8080/auth (key validation, component scope check)
  → Backend: Aptly | rpm-nginx | Zot
  → Package bytes returned
```

**Meridian promotion pipeline:**
```
CI build (CircleCI / GHA)
  → RustFS: s3://staging/{component}/{year}/{format}/{os-arch}/
  → GHA workflow_dispatch (inputs: component, year)
  → Pull artifacts from RustFS
  → GPG sign (RPM/DEB) + cosign sign (OCI) — ephemeral GHA secrets
  → Push to: Aptly API | rpm-data volume | Zot registry
  → Rebuild metadata (Aptly publish | createrepo_c serialised | Zot manifest)
  → Promotion complete — packages immediately available to subscribers
```

### Development Workflow Integration

**Local development:** `docker compose up` starts the full stack. A test
subscription key and dummy packages in `static/` allow end-to-end testing of
the auth flow locally before any real packages are promoted.

**Auth service development:** `cd auth && go test ./...` runs all integration
tests against in-memory SQLite. No running containers required.

**Promotion testing:** A `test` input on `promote-meridian.yml` routes to a
staging Zot/Aptly/createrepo_c instance rather than production, allowing pipeline
testing without subscriber impact.

---

## Architecture Validation Results

### Coherence Validation

**Decision Compatibility: ✅ Pass**
- Go 1.26.1 + chi v5 + modernc.org/sqlite: no version conflicts; all maintained and
  production-ready as of 2026
- Docker Compose v5.1.0: all services run as standard Linux containers with no
  cloud-provider-specific runtime dependencies (FR33 satisfied)
- Traefik forwardAuth contract: standard `Authorization: Basic` +
  `X-Forwarded-Uri` headers — no custom protocol, no chi conflicts
- ACME Let's Encrypt: built into Traefik static config; no third-party dependency
- No contradictory decisions found

**Pattern Consistency: ✅ Pass**
- `snake_case` JSON fields consistent with PRD-specified schemas throughout
- `KeyStore` interface in `store/store.go`, SQLite implementation in `store/sqlite.go` —
  boundary enforced by package structure
- Error propagation pattern (store wraps, handler unwraps with `errors.Is`) coherent
  with sentinel error definitions in `store.go`
- `log/slog` sanitization rules consistent with NFR5 (no key values in logs)

**Structure Alignment: ✅ Pass**
- `auth/internal/` properly encapsulates all implementation; `cmd/server/main.go`
  is wiring only
- Traefik dynamic config split: `routers-public.yml` and `routers-admin.yml` map
  directly to the two entrypoints
- GHA workflows separated by concern — `promote-meridian.yml` (triggering) and
  `sign-and-publish.yml` (signing) — matches single-responsibility principle

### Traefik Routing Taxonomy (Three Categories)

The architecture defines three distinct Traefik routing categories. All three must
be explicitly implemented — none are implied defaults:

| Category | Entrypoint | forwardAuth | Examples |
|---|---|---|---|
| ① public-authenticated | websecure (0.0.0.0:443) | ✅ Required | `/rpm/`, `/deb/`, `/oci/` |
| ② public-unauthenticated | websecure (0.0.0.0:443) | ❌ None | `/gpg/` (and future `/status/`) |
| ③ admin-internal | admin (127.0.0.1:8443) | ❌ None (internal trust) | `/api/v1/` |

Category ② requires a dedicated router rule in `routers-public.yml` with no
middleware. An agent implementing Traefik without this rule will route `/gpg/`
through forwardAuth, breaking FR5 (public GPG key download).

### Requirements Coverage Validation

**Functional Requirements: ✅ All 34 FRs covered**

| FR Group | Architecture Support | Status |
|---|---|---|
| FR1–FR7 Package Access & Delivery | Traefik → nginx/Aptly/Zot, static GPG, GHA signing, OCI image index | ✅ |
| FR8–FR13 Subscription Key Auth | forwardAuth handler, no-cache SQLite reads, HTTP 401 contract | ✅ |
| FR14–FR20 Key Lifecycle Management | Admin API handlers, KeyStore interface, structured errors | ✅ |
| FR21–FR27 Package Publishing | RustFS staging, workflow_dispatch, GPG/cosign, createrepo_c serialisation, Aptly snapshots | ✅ |
| FR28–FR31 Repository Structure | Year-versioned paths, OS/distro subdirs, Aptly + createrepo_c consistency model | ✅ |
| FR32–FR34 Platform Operations | docker-compose.yml, named volumes, 127.0.0.1:8443 admin entrypoint | ✅ |

**Non-Functional Requirements: ✅ All 17 NFRs covered**

| NFR Group | Architecture Support | Status |
|---|---|---|
| NFR1–NFR3 Performance | SQLite microsecond reads; nginx static serving; no app-layer throttling | ✅ |
| NFR4–NFR8 Security | ACME TLS; slog + Traefik log sanitization; ephemeral GHA signing; 127.0.0.1 admin binding | ✅ |
| NFR9–NFR12 Reliability | `restart: unless-stopped`; Traefik health check → 503 fail-closed; external uptime check | ✅ |
| NFR13–NFR14 Scalability | 500 concurrent well within SQLite budget; retention scripts bound storage growth | ✅ |
| NFR15–NFR17 Integration | Zot (OCI Dist Spec v1); createrepo_c (repomd.xml); Aptly (Debian archive); HTTP Basic Auth only; ACME TLS | ✅ |

### Gap Analysis Results

#### 🔴 Critical Gaps (must resolve before implementation)

**C1 — GHA concurrency group key must be specified exactly (FR25)**
The exact GHA concurrency group string is a correctness requirement, not an
implementation detail. Coarser granularity causes unnecessary pipeline
serialisation; finer granularity allows `repomd.xml` corruption:
```yaml
# In promote-meridian.yml
concurrency:
  group: rpm-publish-${{ inputs.component }}-${{ inputs.os }}
  cancel-in-progress: false
```
One lock per (component, OS) pair. This exact string must appear in the workflow.

**C2 — Aptly prune script must not delete currently-serving snapshots**
`aptly/scripts/prune-snapshots.sh` must check which snapshots are currently
published before pruning. Deleting a live snapshot breaks all DEB subscribers
for that component/year immediately and silently:
```bash
# Required guard in prune-snapshots.sh
PUBLISHED=$(aptly publish list -raw | awk '{print $NF}')
# Never prune a snapshot name that appears in $PUBLISHED
```

**C3 — Traefik access logs must redact Authorization header (NFR5)**
Default Traefik access log configuration records the full `Authorization: Basic`
header, logging subscriber key values in plaintext. This is a critical NFR5
violation. The following must be present in `traefik/traefik.yml`:
```yaml
accessLog:
  fields:
    headers:
      defaultMode: keep
      names:
        Authorization: redact
```

#### 🟠 Important Gaps (address in implementation stories)

**I1 — TLS cert expiry monitoring**
Traefik ACME renewal failures are logged but not alerted. Add cert expiry
monitoring to the Prometheus/uptime setup — alert at 30 days before expiry.

**I2 — Aptly snapshot publish smoke test**
After `publish-snapshot.sh`, the promotion workflow should run `apt-get update`
against the published endpoint to verify the snapshot is complete and accessible
before marking the promotion successful.

**I3 — Promotion pipeline artifact checksum verification**
CI must write `{artifact}.sha256` files to RustFS alongside each staged artifact.
The promotion workflow must verify checksums before GPG/cosign signing to prevent
malicious artifact injection into staging:
```bash
sha256sum --check "${ARTIFACT}.sha256" || exit 1
```

**I4 — GPG public key file in version control + health check**
`static/gpg/meridian.asc` must be committed to the repository (not generated at
deploy time) and verified in `scripts/health-check.sh`. Accidental deletion or
corruption breaks all new subscriber onboarding silently.

**I5 — SQLite key values stored in plaintext (known MVP limitation)**
Key values are stored as plaintext 64-char hex strings in the `subscription_key`
table. The `auth-db` volume backup must be treated as sensitive credential
material. Phase 2 hardening: introduce argon2id hashing at rest. The `KeyStore`
interface accommodates this change without handler code modification.

#### 🟡 Minor Gaps (acceptable deferrals)

**m1 — Traefik dynamic config validation**
A malformed `routers-public.yml` can cause routes to disappear without container
restart. Add a config lint step to the operator runbook.

**m2 — Disk space monitoring on rpm-data volume**
Add `rpm-data` volume usage to the Prometheus metrics dashboard.

**m3 — Zot blob recovery path**
Zot blobs can be re-promoted from the source Git tag if `zot-data` is corrupted.
Document: re-run the promotion workflow from the original release tag.

**m4 — Admin API host-level audit logging**
SSH session metadata (actor identity) should be captured in the host audit log
for admin API operations. `GET /api/v1/keys` provides key-level audit trail;
host-level SSH logging provides actor attribution.

**m5 — Key store recovery procedure**
Recovery: stop auth container → `cp auth-backup/latest.db auth-db/auth.db` →
restart auth container. RTO: under 5 minutes. Backup script execution scheduling
(host cron vs. Docker Compose scheduled service) is deferred to implementation.

### Architecture Completeness Checklist

**✅ Requirements Analysis**
- [x] Project context thoroughly analyzed from PRD + brainstorming session
- [x] Scale and complexity assessed (medium, 500 concurrent, no real-time)
- [x] Technical constraints identified (HTTP Basic Auth only, OCI Dist Spec, single-VM)
- [x] Cross-cutting concerns mapped (TLS, auth boundary, secrets, storage lifecycle, observability)

**✅ Architectural Decisions**
- [x] Critical decisions documented with verified versions (Go 1.26.1, Docker Compose v5.1.0)
- [x] Technology stack fully specified (7–8 components, all named and versioned)
- [x] Integration patterns defined (forwardAuth contract, PathStrip middleware, S3 staging)
- [x] Performance considerations addressed (no-cache forwardAuth, nginx static serving)

**✅ Implementation Patterns**
- [x] Naming conventions established (snake_case JSON, Go stdlib conventions, SQLite schema)
- [x] Structure patterns defined (Go package layout, co-located tests, interface boundaries)
- [x] Process patterns documented (error propagation, log sanitization, middleware chain)
- [x] Enforcement guidelines and anti-patterns explicitly listed

**✅ Project Structure**
- [x] Complete directory structure defined with all files annotated
- [x] Component boundaries established (API, data, deployment)
- [x] Integration points mapped (Traefik→auth, GHA→RustFS→backends, data flow diagrams)
- [x] All 34 FRs mapped to specific files/directories

### Architecture Readiness Assessment

**Overall Status: READY FOR IMPLEMENTATION** (with 3 critical gaps resolved in Epic 1)

**Confidence Level: High**

**Key Strengths:**
- Every subscriber-facing requirement has a named, configured component
- The only custom code (auth service) is a small, well-bounded Go binary with
  clear interfaces and a defined test strategy
- NFR11 (fail-closed) is delivered structurally via Traefik health checks
- The `KeyStore` interface ensures Phase 2 integration requires zero handler changes
- GHA as the only write path means no signing keys ever touch the server
- Three-category Traefik routing taxonomy prevents auth bypass misconfiguration

**Areas for Future Enhancement:**
- OpenAPI spec for admin API (Phase 2)
- argon2id hashing for key store at rest (Phase 2 hardening)
- In-process forwardAuth cache (only if SQLite benchmarks show bottleneck)
- CDN insertion point for Meridian (when subscriber scale demands it)
- Multi-region deployment (Vision phase)

### Implementation Handoff

**Critical items for Epic 1 (must not defer):**
- Implement Traefik routing taxonomy: all three categories explicitly configured
- Add `Authorization: redact` to `traefik/traefik.yml` access log config
- Specify GHA concurrency group `rpm-publish-{component}-{os}` in workflow
- Add Aptly prune guard before any snapshot retention runs

**AI Agent Guidelines:**
- Follow all architectural decisions exactly as documented
- The `KeyStore` interface in `internal/store/store.go` must never be violated
- Never log the Authorization header value at any log level
- Never return HTTP 200 from `GET /auth` on any failure condition
- All tests run against real SQLite (in-memory) — no mocks for the key store
- The three Traefik routing categories are explicit requirements, not suggestions

**First Implementation Priority:**
```bash
# Epic 1, Story 1: Initialize Docker Compose scaffold
mkdir -p packyard/{traefik/dynamic,auth,aptly/scripts,rpm/scripts,zot,rustfs,static/gpg,scripts,.github/workflows}
touch packyard/docker-compose.yml packyard/.env.example
# Verify: docker compose up → Traefik starts → health check responds
```
