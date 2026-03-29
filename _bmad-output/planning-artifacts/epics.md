---
stepsCompleted: [1, 2, 3, 4]
storiesComplete: true
status: complete
completedAt: 2026-03-29
inputDocuments:
  - '_bmad-output/planning-artifacts/prd.md'
  - '_bmad-output/planning-artifacts/architecture.md'
---

# packyard - Epic Breakdown

## Overview

This document provides the complete epic and story breakdown for packyard, decomposing the requirements from the PRD and Architecture into implementable stories.

## Requirements Inventory

### Functional Requirements

FR1: Subscribers can download RPM packages for their licensed component using standard `dnf`/`yum` tooling
FR2: Subscribers can download DEB packages for their licensed component using standard `apt` tooling
FR3: Subscribers can pull OCI container images for their licensed component using standard Docker/containerd tooling
FR4: Subscribers can verify OCI image signatures offline using `cosign` without any outbound internet dependency
FR5: Subscribers can download the Meridian GPG public key without authentication
FR6: Package managers automatically verify GPG signatures on RPM and DEB packages without manual trust configuration beyond initial key import
FR7: OCI clients retrieve the architecture-appropriate image (x86_64 or ARM64) using a single image tag without specifying architecture explicitly
FR8: The platform authenticates subscribers via HTTP Basic Auth on all package serving endpoints
FR9: The platform enforces component scope — a key scoped to Core cannot access Minion or Sentinel package paths
FR10: The platform returns HTTP 401 on requests made with an invalid, revoked, or out-of-scope key
FR11: Key revocation takes effect on the next request with no server restart or configuration reload required
FR12: Subscribers can embed credentials in standard OS package manager configuration files for persistent automated access
FR13: Subscribers can embed credentials in Kubernetes image pull secrets for OCI access
FR14: Operations staff can create a subscription key scoped to a specific component (Core, Minion, or Sentinel)
FR15: Operations staff can assign a human-readable label to a subscription key at creation time
FR16: Operations staff can revoke a subscription key by ID
FR17: Operations staff can list all subscription keys with their active status and usage counts
FR18: Operations staff can inspect a specific key's details — component scope, active status, label, creation date, and usage count
FR19: Operations staff can filter the key list by component
FR20: The admin API returns structured error responses with a machine-readable code and a human-readable message containing diagnostic context
FR21: Build systems can stage unsigned package artefacts to the platform's object storage
FR22: Release engineers can trigger promotion of staged artefacts to a specific Meridian release repository
FR23: The promotion pipeline GPG-signs RPM and DEB packages during promotion
FR24: The promotion pipeline cosign-signs OCI images during promotion
FR25: The promotion pipeline rebuilds RPM repository metadata in a way that prevents corruption from concurrent publish operations
FR26: The promotion pipeline creates an immutable point-in-time DEB repository snapshot for each Meridian release
FR27: Multiple Meridian release years can be published and served simultaneously without URL conflicts
FR28: Subscribers access packages via year-versioned repository URLs that remain permanently stable after publication
FR29: RPM packages are accessible via distinct repository paths for each supported OS target (RHEL 8, RHEL 9, RHEL 10, CentOS 10)
FR30: DEB packages are accessible via distinct APT sources for each supported distribution (Debian 12, Debian 13, Ubuntu 22.04, Ubuntu 24.04)
FR31: Repository metadata (RPM repodata, APT Release/Packages, OCI manifests) is always consistent with the available packages at any point in time
FR32: Operations staff can deploy the complete platform stack on a single VM using container-based tooling
FR33: The platform operates on standard Linux VMs across Azure, AWS, and KVM without cloud-provider-specific dependencies
FR34: The admin API is accessible exclusively from internal/operations networks, not via the public package serving endpoints

### NonFunctional Requirements

NFR1: The forwardAuth service validates a subscription key and returns a response within 100ms
NFR2: Repository metadata endpoints deliver first byte within 2 seconds under normal load
NFR3: Package download throughput is bounded only by VM network capacity — no application-layer throttling
NFR4: All endpoints are served exclusively over TLS — no HTTP fallback or redirect accepted
NFR5: Subscription key values are never written to application logs, access logs, or error reports in plaintext
NFR6: GPG signing keys and cosign private keys are never written to disk on any server — all signing operations are ephemeral within the GHA promotion workflow only
NFR7: TLS certificates use standard ACME-compatible issuance — no certificate pinning — to remain compatible with enterprise TLS inspection proxies
NFR8: The admin API is unreachable via the public package serving network entrypoint — network-level isolation, not application-level filtering
NFR9: `pkg.mdn.opennms.com` achieves 99.9% monthly availability (measured at the package serving endpoints)
NFR10: Repository metadata is always in a consistent state — no partial repodata rebuild is visible to package managers at any point in time
NFR11: forwardAuth service failure results in fail-closed behaviour (HTTP 503 to package manager) — never fail-open (HTTP 200 granting unauthenticated access)
NFR12: The platform recovers to full operation after a VM restart without manual intervention beyond the container orchestration restart policy
NFR13: The platform serves 500 concurrent authenticated subscribers without measurable degradation in forwardAuth response time or metadata delivery
NFR14: Aptly snapshot and createrepo_c metadata storage grows predictably and is bounded by a defined retention policy — unbounded growth is not acceptable
NFR15: All package serving endpoints comply with their respective standards — OCI Distribution Spec v1, createrepo_c-compatible repomd.xml, standard Debian archive format
NFR16: HTTP Basic Auth is the sole authentication mechanism on serving endpoints — no custom headers, query parameters, or cookies
NFR17: The platform operates correctly behind enterprise HTTP proxies and TLS inspection appliances without requiring custom certificate trust configuration

### Additional Requirements

From Architecture — technical requirements that affect implementation:

- **Starter scaffold (Epic 1 Story 1):** Docker Compose v5.1.0 scaffold with Traefik, auth service (Go 1.26.1 + chi v5 + modernc.org/sqlite), Aptly, RPM service, Zot, RustFS, static file server
- **Docker Compose:** No `version:` field (Compose Spec v5.0 convention); auth service must have `restart: unless-stopped` and a `healthcheck` from day one
- **Named volumes required:** `aptly-data`, `rpm-data`, `zot-data`, `rustfs-data`, `auth-db`, `auth-backup`, `traefik-certs`
- **Two Traefik entrypoints:** `websecure` (0.0.0.0:443) and `admin` (127.0.0.1:8443, loopback only)
- **KeyStore interface:** Defined in `internal/store/store.go` before any handler code; 5 methods: CreateKey, GetByValue, ListKeys, RevokeKey, IncrementUsage
- **SQLite WAL mode:** `journal_mode=WAL`, `synchronous=NORMAL`, `busy_timeout=5000`, `foreign_keys=ON`; `SetMaxOpenConns(1)`
- **Key value format:** `crypto/rand` 32 bytes → 64-char lowercase hex (not UUID)
- **No forwardAuth caching:** Always read from SQLite directly — in-process caching violates FR11
- **GHA concurrency group (C1 — critical):** `rpm-publish-${{ inputs.component }}-${{ inputs.os }}` with `cancel-in-progress: false`
- **Aptly prune guard (C2 — critical):** `prune-snapshots.sh` must check `aptly publish list -raw` before deleting any snapshot
- **Traefik Authorization redaction (C3 — critical):** `accessLog.fields.headers.names.Authorization: redact` AND `ClientUsername: drop`
- **Artifact checksum verification (I3):** CI writes `.sha256` files to RustFS; promotion workflow verifies before signing
- **GPG key in VCS (I4):** `static/gpg/meridian.asc` committed to repository; verified in `health-check.sh`
- **SQLite backup:** Daily `sqlite3 .backup` to `auth-backup` volume; 7-day retention
- **External uptime check:** HTTP check on `/gpg/meridian.asc` from existing monitoring infrastructure
- **Aptly snapshot retention policy:** 2 most recent published snapshots per year × component × format; older to cold path, never deleted
- **Promotion trigger:** Manual `workflow_dispatch` (component + year inputs); no automatic trigger on release tag (post-MVP)
- **Signing is GHA-only:** GPG key and cosign key live in GHA secrets — never written to the VM disk

### UX Design Requirements

N/A — packyard has no customer-facing UI. All user interaction occurs through standard OS tools (dnf, apt, docker, kubectl), config files, and the admin API (curl / internal tooling). No UX documentation exists or is required.

### FR Coverage Map

| FR | Epic | Description |
|---|---|---|
| FR1 | Epic 5 | RPM download via dnf/yum |
| FR2 | Epic 5 | DEB download via apt |
| FR3 | Epic 5 | OCI pull via Docker/containerd |
| FR4 | Epic 5 | Offline cosign OCI verification |
| FR5 | Epic 1 | Public GPG key download |
| FR6 | Epic 5 | Automatic GPG signature verification |
| FR7 | Epic 5 | Multi-arch OCI image index |
| FR8 | Epic 2 | HTTP Basic Auth on all serving endpoints |
| FR9 | Epic 2 | Component scope enforcement |
| FR10 | Epic 2 | HTTP 401 on invalid/revoked/out-of-scope key |
| FR11 | Epic 2 | Instant key revocation |
| FR12 | Epic 2 | Credentials embeddable in OS package manager config |
| FR13 | Epic 2 | Credentials embeddable in K8s pull secrets |
| FR14 | Epic 3 | Create component-scoped subscription key |
| FR15 | Epic 3 | Assign human-readable label at key creation |
| FR16 | Epic 3 | Revoke key by ID |
| FR17 | Epic 3 | List all keys with active status + usage counts |
| FR18 | Epic 3 | Inspect key detail |
| FR19 | Epic 3 | Filter key list by component |
| FR20 | Epic 3 | Structured admin API error responses |
| FR21 | Epic 4 | Stage unsigned artefacts to object storage |
| FR22 | Epic 4 | Trigger promotion to specific Meridian release repo |
| FR23 | Epic 4 | GPG-sign RPM/DEB during promotion |
| FR24 | Epic 4 | cosign-sign OCI during promotion |
| FR25 | Epic 4 | Serialised RPM metadata rebuild |
| FR26 | Epic 4 | Immutable Aptly snapshot per Meridian release |
| FR27 | Epic 4 | Multiple Meridian years served simultaneously |
| FR28 | Epic 1 | Year-versioned repository URLs |
| FR29 | Epic 1 | Distinct RPM paths per OS target |
| FR30 | Epic 1 | Distinct DEB APT sources per distro |
| FR31 | Epic 4 | Repository metadata always consistent |
| FR32 | Epic 1 | Single-VM deployment via container tooling |
| FR33 | Epic 1 | Cloud-agnostic |
| FR34 | Epic 2 | Admin API network-isolated from public entrypoint |

**FR Coverage: 34/34 ✅ | NFR Coverage: 17/17 ✅**

## Epic List

### Epic 1: Platform Foundation
Operations staff can deploy the complete platform stack on a single VM, verify that `pkg.mdn.opennms.com` is reachable over TLS, and confirm the public GPG key endpoint is accessible. The full URL structure (year-versioned, per-OS, per-distro paths) is in place and routing is wired correctly — ready to serve packages once auth and content are added.

**FRs covered:** FR5, FR28, FR29, FR30, FR32, FR33
**NFRs covered:** NFR4, NFR7, NFR12

### Epic 2: Subscription Authentication
Subscribers can authenticate with their subscription key via standard HTTP Basic Auth on all serving endpoints. The forwardAuth service validates credentials and enforces component scope — a Core key cannot access Minion or Sentinel paths. Keys can be revoked instantly with no server restart. The admin API is network-isolated from public serving endpoints.

**FRs covered:** FR8, FR9, FR10, FR11, FR12, FR13, FR34
**NFRs covered:** NFR8, NFR11, NFR16

### Epic 3: Key Management API
Operations staff can provision, revoke, and inspect subscription keys via the admin API. Keys are component-scoped with human-readable labels, and the API returns structured, machine-readable error responses. Operations staff have full visibility into key status and usage counts.

**FRs covered:** FR14, FR15, FR16, FR17, FR18, FR19, FR20

### Epic 4: Promotion Pipeline
Release engineers can stage unsigned artefacts to object storage, trigger a GHA promotion workflow, and publish a GPG-signed RPM / DEB / cosign-signed OCI release to a year-versioned Meridian repository. Multiple Meridian release years coexist without conflicts. RPM metadata rebuilds are serialised to prevent corruption. Aptly snapshots are immutable and bounded by a retention policy.

**FRs covered:** FR21, FR22, FR23, FR24, FR25, FR26, FR27, FR31
**NFRs covered:** NFR6, NFR10, NFR14

### Epic 5: Package Delivery & Production Hardening
Subscribers can download RPM, DEB, and OCI packages using standard tooling (`dnf`, `apt`, `docker`, `cosign`) and verify package authenticity without internet dependency. The platform meets all NFR targets: <100ms forwardAuth, <2s metadata, 500 concurrent subscribers, 99.9% availability, credential non-logging, and standards compliance. The full signing chain (GPG + cosign) is verifiable end-to-end.

**FRs covered:** FR1, FR2, FR3, FR4, FR6, FR7
**NFRs covered:** NFR1, NFR2, NFR3, NFR5, NFR9, NFR13, NFR15, NFR17

---

## Epic 1: Platform Foundation

Operations staff can deploy the complete platform stack on a single VM, verify that `pkg.mdn.opennms.com` is reachable over TLS, and confirm the public GPG key endpoint is accessible. The full URL routing structure is in place — ready for auth and content to be layered on.

### Story 1.1: Docker Compose Platform Scaffold with Traefik TLS

As an operations engineer,
I want the complete platform stack defined in Docker Compose with Traefik handling TLS termination,
So that I can deploy packyard on a single VM and have all services start automatically on VM boot.

**Acceptance Criteria:**

**Given** a Linux VM with Docker and Docker Compose v5.1.0 installed
**When** `docker compose up -d` is run from the repository root
**Then** all services start: Traefik, auth (stub), Aptly, RPM nginx, Zot, RustFS, and static file server
**And** no `version:` field appears in `docker-compose.yml` (Compose Spec v5.0)
**And** all services have `restart: unless-stopped`
**And** all 7 named volumes are defined: `aptly-data`, `rpm-data`, `zot-data`, `rustfs-data`, `auth-db`, `auth-backup`, `traefik-certs`

**Given** a domain pointing to the VM's public IP
**When** Traefik starts for the first time
**Then** Traefik obtains a TLS certificate via ACME Let's Encrypt (tlsChallenge) and serves on port 443
**And** the `websecure` entrypoint listens on `0.0.0.0:443`
**And** the `admin` entrypoint listens on `127.0.0.1:8443` (loopback only)
**And** the ACME certificate persists to the `traefik-certs` volume

**Given** the Traefik access log configuration
**When** any request is processed
**Then** the `Authorization` header value is redacted in access logs
**And** the `ClientUsername` field is dropped from access log records (NFR5 prerequisite, C3)

**Given** the VM is rebooted
**When** Docker restarts
**Then** all services come back online automatically with no manual intervention (NFR12)

### Story 1.2: GPG Public Key Endpoint

As a new subscriber,
I want to download the Meridian GPG public key without any credentials,
So that I can configure my package manager to verify package signatures before I have a subscription key.

**Acceptance Criteria:**

**Given** the packyard stack is running
**When** `curl https://pkg.mdn.opennms.com/gpg/meridian.asc` is executed with no credentials
**Then** the response is HTTP 200 with the Meridian GPG public key in ASCII-armored format
**And** no `Authorization` header is required

**Given** the repository
**When** it is cloned
**Then** `static/gpg/meridian.asc` exists as a valid ASCII-armored GPG public key committed to version control — not generated at deploy time (I4)

**Given** `scripts/health-check.sh` is executed
**When** the script runs
**Then** it verifies `/gpg/meridian.asc` returns HTTP 200 and exits 0 on success, non-zero on failure

**Given** the Traefik router for `/gpg/`
**When** the router configuration is inspected
**Then** no `forwardAuth` middleware is attached — the route is explicitly unauthenticated

### Story 1.3: Package Serving URL Structure and Backend Containers

As an operations engineer,
I want the complete URL structure for RPM, DEB, and OCI endpoints defined and routed to the correct backends,
So that when packages are published and auth is enabled, subscribers can access them at permanently stable versioned URLs.

**Acceptance Criteria:**

**Given** the RPM nginx container is running
**When** a request is made to `/rpm/core/2025/el9-x86_64/`
**Then** it routes to the RPM nginx backend at the correct directory path
**And** distinct paths exist for all OS targets: `el8-x86_64`, `el9-x86_64`, `el10-x86_64`, `centos10-x86_64` (FR29)

**Given** the Aptly container is running
**When** a request is made to `/deb/core/2025/`
**Then** it routes to the Aptly backend
**And** the APT source structure supports all four distros: `bookworm`, `trixie`, `jammy`, `noble` (FR30)

**Given** the Zot OCI registry container is running
**When** a request is made to `/oci/v2/meridian-core/manifests/2025`
**Then** Traefik strips the `/oci` prefix before forwarding to Zot
**And** `packyard-strip-oci` is declared after `packyard-auth` in the middleware chain (forwardAuth sees full path)

**Given** year-versioned URL paths
**When** requests for `/rpm/core/2025/` and `/rpm/core/2026/` are both made
**Then** both resolve to distinct directory trees on the RPM backend with no conflicts (FR28, FR27 foundation)

**Given** the RustFS staging container is running
**When** the RustFS health endpoint is queried from within the Docker network
**Then** RustFS responds healthy and the staging bucket is accessible at the configured S3 endpoint

---

## Epic 2: Subscription Authentication

Subscribers can authenticate with their subscription key via standard HTTP Basic Auth on all serving endpoints. The forwardAuth service validates credentials and enforces component scope — a Core key cannot access Minion or Sentinel paths. Keys can be revoked instantly with no server restart. The admin API is network-isolated from public serving endpoints.

### Story 2.1: Auth Service Scaffold with KeyStore Interface and SQLite

As a platform developer,
I want the Go auth service scaffold with a defined KeyStore interface and SQLite implementation,
So that the forwardAuth endpoint and admin API have a tested, swappable data layer from the start.

**Acceptance Criteria:**

**Given** the `auth/` directory structure per architecture
**When** `CGO_ENABLED=0 go build -o /auth ./cmd/server` is run
**Then** it produces a static binary with no external dependencies

**Given** `internal/store/store.go`
**When** the file is inspected
**Then** it defines the `KeyStore` interface with exactly 5 methods: `CreateKey`, `GetByValue`, `ListKeys`, `RevokeKey`, `IncrementUsage`
**And** sentinel errors `ErrNotFound` and `ErrRevoked` are defined in this file

**Given** the SQLite implementation in `internal/store/sqlite.go`
**When** the store is opened
**Then** WAL mode, `synchronous=NORMAL`, `busy_timeout=5000`, and `foreign_keys=ON` are set
**And** `SetMaxOpenConns(1)` is set on the connection pool
**And** the `subscription_key` table is created if it does not exist (lowercase singular table name)

**Given** `internal/store/sqlite_test.go`
**When** `go test ./internal/store/...` is run
**Then** all tests pass using an in-memory SQLite database (`:memory:` DSN)
**And** `GetByValue` returns `sql.ErrNoRows` for a non-existent key

**Given** the auth service Docker container
**When** `docker compose up auth` is run
**Then** the container starts and the health check at `GET /health` returns HTTP 200
**And** the container has `restart: unless-stopped` and `healthcheck` defined in `docker-compose.yml`

### Story 2.2: forwardAuth Endpoint

As a Traefik ingress,
I want to call a `/auth` endpoint that validates a subscriber's HTTP Basic Auth credentials against the component they are requesting,
So that only subscribers with valid, correctly scoped keys can access package serving endpoints.

**Acceptance Criteria:**

**Given** a request to `GET /auth` with `Authorization: Basic base64(subscriber:VALID_CORE_KEY)` and `X-Forwarded-Uri: /rpm/el9-x86_64/core/2025/`
**When** the handler processes the request
**Then** it returns HTTP 200 with an empty body

**Given** a request with a key scoped to `minion` and `X-Forwarded-Uri: /rpm/el9-x86_64/core/2025/meridian-core-2025.rpm`
**When** the handler processes the request
**Then** it returns HTTP 401 with an empty body (scope mismatch — FR9, FR10)

**Given** a request with a revoked key (active = false)
**When** the handler processes the request
**Then** it returns HTTP 401 with an empty body (FR10, FR11)

**Given** a request with no `Authorization` header, or a malformed one, or a key of the wrong length
**When** the handler processes the request
**Then** it returns HTTP 401 with an empty body

**Given** the SQLite store returns an unexpected error
**When** the handler processes the request
**Then** it returns HTTP 503 with an empty body — never HTTP 200 (NFR11 fail-closed)

**Given** the handler under any condition
**When** it returns any response
**Then** the response body is always empty
**And** the `Authorization` header value is never written to `log/slog` output at any log level (NFR5)

**Given** `handler/forward_auth_test.go`
**When** `go test ./internal/handler/...` is run
**Then** all test cases pass using a `mockStore` — covering: valid key, scope mismatch, revoked key, missing auth header, store error

### Story 2.3: Traefik forwardAuth Middleware Wiring and Admin Network Isolation

As a subscriber,
I want my HTTP Basic Auth credentials validated on every package request,
So that only my licensed component's packages are accessible with my subscription key.

**Acceptance Criteria:**

**Given** the Traefik dynamic config
**When** `traefik/dynamic/middlewares.yml` is inspected
**Then** the `packyard-auth` forwardAuth middleware is defined pointing at `http://auth:8080/auth`
**And** `authRequestHeaders` includes only `Authorization`
**And** `authResponseHeaders` is empty
**And** `maxResponseBodySize` is set to `4096`
**And** `trustForwardHeader` is `false`

**Given** the routers in `traefik/dynamic/routers-public.yml`
**When** inspected
**Then** `/rpm/`, `/deb/`, and `/oci/` routers all have `packyard-auth` in their `middlewares` list
**And** the `/gpg/` router has no `packyard-auth` middleware

**Given** a request to `/rpm/core/2025/el9-x86_64/` with no credentials
**When** the request reaches Traefik
**Then** Traefik returns HTTP 401 (FR8, FR10)

**Given** a request to `/api/v1/keys` via the `websecure` entrypoint (port 443)
**When** the request reaches Traefik
**Then** it is not routed — the admin API is only reachable via the `admin` entrypoint (127.0.0.1:8443) (FR34, NFR8)

**Given** a subscriber's `.repo` file with `baseurl=https://subscriber:KEY@pkg.mdn.opennms.com/rpm/core/2025/el9-x86_64/`
**When** `dnf repolist` is run
**Then** the repo is listed (credentials embedded in standard package manager config are valid — FR12)

**Given** a Kubernetes `imagePullSecret` with `auths["pkg.mdn.opennms.com/oci"]` containing base64-encoded `subscriber:KEY`
**When** the secret is referenced by a pod spec
**Then** the format is valid for use with standard container runtimes (FR13)

---

## Epic 3: Key Management API

Operations staff can provision, revoke, and inspect subscription keys via the admin API. Keys are component-scoped with human-readable labels, and the API returns structured, machine-readable error responses. Operations staff have full visibility into key status and usage counts.

### Story 3.1: Create Subscription Key

As an operations engineer,
I want to create a subscription key scoped to a specific Meridian component with a human-readable label,
So that I can provision access for a new subscriber.

**Acceptance Criteria:**

**Given** a `POST /api/v1/keys` request with body `{"component": "core", "label": "Acme Corp - Core", "expires_at": null}`
**When** the request is processed
**Then** the response is HTTP 201 Created with a `Key` object body
**And** the `id` field is a 64-char lowercase hex string generated via `crypto/rand`
**And** the `active` field is `true`
**And** the `created_at` field is a valid RFC3339 UTC timestamp
**And** the `usage_count` is `0`

**Given** a `POST /api/v1/keys` request with `{"component": "invalid", "label": "test"}`
**When** the request is processed
**Then** the response is HTTP 400 with `{"code": "INVALID_COMPONENT", "message": "..."}` (FR20)
**And** `component` must be one of `core`, `minion`, or `sentinel`

**Given** all three valid component values (`core`, `minion`, `sentinel`)
**When** a key is created for each
**Then** each returns HTTP 201 with the correct `component` field (FR14)

**Given** the `label` field
**When** a key is created
**Then** the label is stored and returned in the response (FR15)

### Story 3.2: List and Filter Keys

As an operations engineer,
I want to list all subscription keys and optionally filter by component,
So that I can audit which subscribers have access to which components.

**Acceptance Criteria:**

**Given** `GET /api/v1/keys` with no query parameters
**When** the request is processed
**Then** the response is HTTP 200 with a JSON array of all keys
**And** each key object includes `id`, `component`, `active`, `label`, `created_at`, `expires_at`, `usage_count`

**Given** `GET /api/v1/keys?component=core`
**When** the request is processed
**Then** the response contains only keys where `component` is `core` (FR19)

**Given** `GET /api/v1/keys?component=invalid`
**When** the request is processed
**Then** the response is HTTP 400 with `{"code": "INVALID_COMPONENT", "message": "..."}` (FR20)

**Given** no keys exist in the store
**When** `GET /api/v1/keys` is called
**Then** the response is HTTP 200 with an empty array `[]`

**Given** a mix of active and revoked keys
**When** `GET /api/v1/keys` is called
**Then** both active and revoked keys are returned (with `active: false` for revoked) (FR17)

### Story 3.3: Inspect Key Detail

As an operations engineer,
I want to inspect a specific subscription key by its ID,
So that I can verify its scope, label, active status, and usage count.

**Acceptance Criteria:**

**Given** `GET /api/v1/keys/{id}` where `{id}` is a valid existing key ID
**When** the request is processed
**Then** the response is HTTP 200 with the full `Key` object
**And** all fields are present: `id`, `component`, `active`, `label`, `created_at`, `expires_at`, `usage_count` (FR18)

**Given** `GET /api/v1/keys/{id}` where `{id}` does not exist
**When** the request is processed
**Then** the response is HTTP 404 with `{"code": "KEY_NOT_FOUND", "message": "..."}` (FR20)

**Given** a key that has been used to authenticate
**When** `GET /api/v1/keys/{id}` is called
**Then** the `usage_count` reflects the number of successful forwardAuth validations for that key (FR18)

### Story 3.4: Revoke Key

As an operations engineer,
I want to revoke a subscription key by its ID,
So that a subscriber loses access immediately on the next request without any server restart.

**Acceptance Criteria:**

**Given** `DELETE /api/v1/keys/{id}` where `{id}` is a valid active key
**When** the request is processed
**Then** the response is HTTP 204 No Content with no body (FR16)
**And** the key's `active` field is set to `false` in the store

**Given** a key that has just been revoked via `DELETE /api/v1/keys/{id}`
**When** the next forwardAuth request arrives using that key's value
**Then** the forwardAuth endpoint returns HTTP 401 — no cache, no delay, no restart required (FR11)

**Given** `DELETE /api/v1/keys/{id}` where `{id}` does not exist
**When** the request is processed
**Then** the response is HTTP 404 with `{"code": "KEY_NOT_FOUND", "message": "..."}` (FR20)

**Given** `DELETE /api/v1/keys/{id}` on an already-revoked key
**When** the request is processed
**Then** the response is HTTP 204 No Content (idempotent)

---

## Epic 4: Promotion Pipeline

Release engineers can stage unsigned artefacts to object storage, trigger a GHA promotion workflow, and publish a GPG-signed RPM / DEB / cosign-signed OCI release to a year-versioned Meridian repository. Multiple Meridian release years coexist without conflicts. RPM metadata rebuilds are serialised to prevent corruption. Aptly snapshots are immutable and bounded by a retention policy.

### Story 4.1: RustFS Staging Bucket and Artifact Upload

As a build system,
I want to upload unsigned package artefacts to an S3-compatible staging bucket,
So that the promotion workflow has a reliable source of artefacts to sign and publish.

**Acceptance Criteria:**

**Given** the RustFS service running in Docker Compose
**When** a build system uploads an artefact using standard S3 PUT to `/{component}/{year}/{format}/{os-arch}/{filename}`
**Then** the upload succeeds and the artefact is retrievable at that path (FR21)

**Given** an RPM artefact `meridian-core-2025.1.0.x86_64.rpm` uploaded to `/core/2025/rpm/el9-x86_64/`
**When** the promotion workflow runs
**Then** it can download the artefact from RustFS using the same path structure

**Given** each staged artefact
**When** it is uploaded
**Then** a corresponding `{filename}.sha256` checksum file is uploaded alongside it (I3)

**Given** the RustFS bucket configuration
**When** the service starts
**Then** the staging bucket is created automatically if it does not exist

### Story 4.2: RPM Promotion — Sign, Rebuild Metadata, Publish

As a release engineer,
I want to trigger a GHA workflow that GPG-signs RPMs from staging and publishes them with correct `repomd.xml` metadata,
So that subscribers can install Meridian RPM packages with automatic GPG verification.

**Acceptance Criteria:**

**Given** a `workflow_dispatch` trigger with inputs `component=core` and `year=2025`
**When** the GHA workflow runs
**Then** it downloads all RPMs for that component/year from RustFS staging
**And** verifies each artefact against its `.sha256` checksum before proceeding (I3)
**And** aborts if any checksum fails

**Given** verified artefacts
**When** the GPG signing step runs
**Then** each RPM is signed using the Meridian GPG private key stored in a GHA secret
**And** the GPG key is never written to disk on the VM — signing is ephemeral in GHA (NFR6, FR23)

**Given** signed RPMs for `component=core`, `year=2025`, `os=el9`
**When** `createrepo_c` runs to rebuild repository metadata
**Then** `repomd.xml`, `primary.xml.gz`, and related files are generated correctly
**And** the GHA workflow uses concurrency group `rpm-publish-${{ inputs.component }}-${{ inputs.os }}` with `cancel-in-progress: false` — exactly this string (C1, FR25)

**Given** concurrent promotion runs for `core/el9` and `core/el8`
**When** both trigger simultaneously
**Then** they run in parallel (different concurrency groups) without blocking each other
**And** two concurrent runs for the same `core/el9` are serialised — the second waits, not cancels

**Given** published RPM metadata
**When** a subscriber runs `dnf repolist` or `dnf install meridian-core`
**Then** the `repomd.xml` is consistent — no partial metadata is visible at any point (NFR10, FR31)

**Given** multiple Meridian years (e.g., 2025 and 2026) both published
**When** both year paths are served simultaneously
**Then** there are no URL conflicts — each year's metadata is independent (FR27)

### Story 4.3: DEB Promotion — Sign, Aptly Snapshot, Publish

As a release engineer,
I want to trigger a GHA workflow that GPG-signs DEBs and creates an immutable Aptly snapshot per Meridian release,
So that DEB subscribers get a consistent, verifiable repository that will never change after publication.

**Acceptance Criteria:**

**Given** a `workflow_dispatch` trigger with inputs `component=core` and `year=2025`
**When** the GHA workflow runs
**Then** it downloads all DEB packages for that component/year from RustFS
**And** verifies checksums before proceeding (I3)
**And** GPG-signs each DEB using the Meridian GPG key in GHA secrets (NFR6, FR23)

**Given** signed DEBs
**When** Aptly ingests them and creates a snapshot
**Then** the snapshot name encodes the component, year, and timestamp (e.g., `core-2025-20260329T120000Z`)
**And** the snapshot is published to Aptly's HTTP serving path (FR26)
**And** the snapshot is immutable — its contents cannot be modified after creation

**Given** `aptly/scripts/prune-snapshots.sh` running after promotion
**When** the script executes
**Then** it first calls `aptly publish list -raw` to identify currently-published snapshots (C2)
**And** it never deletes any snapshot that is currently published
**And** it retains the 2 most recent published snapshots per component/year combination (NFR14)

**Given** an Aptly snapshot publish smoke test step in the workflow
**When** the snapshot is published
**Then** the workflow runs `apt-get update` against the published repo and verifies it succeeds before marking promotion complete (I2)

**Given** DEB metadata served by Aptly
**When** a subscriber runs `apt-get update`
**Then** `Release`, `Packages.gz`, and `InRelease` are consistent with the published snapshot (NFR10, FR31)

### Story 4.4: OCI Promotion — cosign Sign and Zot Publish

As a release engineer,
I want to trigger a GHA workflow that cosign-signs OCI images and pushes them to Zot with a multi-arch manifest index,
So that subscribers can pull the correct architecture image and verify its signature offline.

**Acceptance Criteria:**

**Given** a `workflow_dispatch` trigger with inputs `component=core` and `year=2025`
**When** the GHA workflow runs
**Then** it downloads OCI image artefacts from RustFS (x86_64 and ARM64 variants)
**And** verifies checksums before proceeding (I3)

**Given** verified OCI artefacts
**When** the cosign signing step runs
**Then** each image is signed using a cosign private key stored in a GHA secret (FR24)
**And** the cosign key is never written to disk on the VM — signing is ephemeral in GHA (NFR6)

**Given** signed x86_64 and ARM64 images for `meridian-core:2025`
**When** they are pushed to Zot
**Then** a multi-arch OCI image index manifest is created at `pkg.mdn.opennms.com/oci/v2/meridian-core:2025`
**And** the index references both architecture manifests (FR7 foundation)

**Given** multiple Meridian years published (e.g., `meridian-core:2025` and `meridian-core:2026`)
**When** both tags are queried via the OCI Distribution Spec v1 API
**Then** both resolve correctly with no conflicts (FR27)

**Given** a subscriber running `cosign verify` with the Meridian cosign public key
**When** they verify a pulled image
**Then** verification succeeds without any outbound internet dependency — the signature is stored alongside the image in Zot (FR4 foundation)

---

## Epic 5: Package Delivery & Production Hardening

Subscribers can download RPM, DEB, and OCI packages using standard tooling (`dnf`, `apt`, `docker`, `cosign`) and verify package authenticity without internet dependency. The platform meets all NFR targets: <100ms forwardAuth, <2s metadata, 500 concurrent subscribers, 99.9% availability, credential non-logging, and standards compliance. The full signing chain (GPG + cosign) is verifiable end-to-end.

### Story 5.1: End-to-End RPM Subscriber Download and GPG Verification

As a subscriber,
I want to install Meridian RPM packages using standard `dnf` tooling with my subscription key,
So that I can get Meridian software on my RHEL/CentOS systems with automatic GPG signature verification.

**Acceptance Criteria:**

**Given** the complete packyard stack running via `docker compose up -d` (no mocking of Aptly, createrepo_c, or the auth service)
**When** a subscriber configures a `.repo` file with `baseurl=https://subscriber:VALID_CORE_KEY@pkg.mdn.opennms.com/rpm/core/2025/el9-x86_64/` and runs `dnf install meridian-core`
**Then** the package installs successfully (FR1)
**And** GPG signature verification passes automatically — no manual trust step beyond initial `rpm --import` of `meridian.asc` (FR6)

**Given** a tampered RPM (any byte in the package payload modified after signing)
**When** a subscriber attempts `dnf install` on that package
**Then** `dnf` rejects the package with a GPG verification error — installation does not proceed (FR6 negative test)

**Given** an invalid or revoked subscription key in the `.repo` file
**When** `dnf install` is attempted
**Then** `dnf` receives HTTP 401 and reports an authentication error

**Given** the test exercises a live platform stack
**When** Docker Compose is required in the test environment
**Then** the test suite documents this infrastructure dependency (required for CI)

### Story 5.2: End-to-End DEB Subscriber Download and GPG Verification

As a subscriber,
I want to install Meridian DEB packages using standard `apt` tooling with my subscription key,
So that I can get Meridian software on my Debian/Ubuntu systems with automatic GPG signature verification.

**Acceptance Criteria:**

**Given** the complete packyard stack running via `docker compose up -d` (no mocking of Aptly or the auth service)
**When** a subscriber configures `/etc/apt/sources.list.d/meridian.list` with `https://subscriber:VALID_CORE_KEY@pkg.mdn.opennms.com/deb/core/2025/ bookworm main` and runs `apt-get install meridian-core`
**Then** the package installs successfully (FR2)
**And** GPG signature verification passes automatically via the `InRelease` file — no manual trust step beyond initial `apt-key add` of `meridian.asc` (FR6)

**Given** a tampered DEB (any byte in the package payload modified after signing)
**When** a subscriber attempts `apt-get install` on that package
**Then** `apt-get` rejects the package with a signature verification error — installation does not proceed (FR6 negative test)

**Given** an invalid or revoked subscription key in the sources list
**When** `apt-get update` is attempted
**Then** `apt-get` receives HTTP 401 and reports an authentication failure

**Given** the test exercises a live platform stack
**When** Docker Compose is required in the test environment
**Then** the test suite documents this infrastructure dependency (required for CI)

### Story 5.3: End-to-End OCI Pull, Multi-Arch, and Offline cosign Verification

As a subscriber,
I want to pull Meridian container images using standard Docker tooling and verify signatures offline with `cosign`,
So that my Kubernetes deployments have a verified, tamper-evident image supply chain.

**Acceptance Criteria:**

**Given** the complete packyard stack running via `docker compose up -d` (no mocking of Zot or the auth service)
**When** a subscriber runs `docker pull pkg.mdn.opennms.com/oci/meridian-core:2025` with a valid Core subscription key
**Then** the pull succeeds and the image is stored locally (FR3)

**Given** a host that is x86_64
**When** `docker pull pkg.mdn.opennms.com/oci/meridian-core:2025` is executed
**Then** Docker selects the amd64 manifest automatically from the multi-arch image index — no `--platform` flag required (FR7)
**And** both `amd64` and `arm64` manifests are present in the image index (FR7)

**Given** the Traefik middleware configuration
**When** a `docker pull` request traverses the `/oci/` route
**Then** `packyard-auth` (forwardAuth) executes before `packyard-strip-oci` (stripPrefix) — verified by confirming the auth service receives the full `/oci/v2/...` path in `X-Forwarded-Uri`
**And** the pull succeeds only with valid credentials, and returns HTTP 401 with invalid credentials

**Given** a successfully pulled image
**When** a subscriber runs `cosign verify --key meridian.pub pkg.mdn.opennms.com/oci/meridian-core:2025` on a host with no internet access
**Then** verification succeeds — the signature is co-located with the image in Zot (FR4)

**Given** an invalid or revoked subscription key
**When** `docker pull` is attempted
**Then** Docker receives HTTP 401 and reports an authentication error

### Story 5.4: Platform Observability, Backup, and Log Sanitization

As an operations engineer,
I want Prometheus metrics, daily SQLite backups, and verified credential non-logging,
So that I can monitor platform health, recover from failure, and comply with NFR5 credential security requirements.

**Acceptance Criteria:**

**Given** the auth service is running
**When** Prometheus scrapes `http://auth:9090/metrics`
**Then** the scrape succeeds with HTTP 200 and exposes at minimum: `packyard_auth_requests_total` (by status) and `packyard_auth_duration_seconds` (histogram)

**Given** the platform has processed at least one authenticated request containing a valid subscription key
**When** `docker logs traefik` and `docker logs auth` are grepped for any known subscription key value
**Then** zero matches are found — key values do not appear in any log stream at any log level (NFR5)
**And** `docker logs traefik` contains no `Authorization` header values in access log lines
**And** `docker logs traefik` contains no `ClientUsername` field in access log records (C3 — both redaction vectors)

**Given** the daily backup cron (or manual trigger) running `sqlite3 .backup`
**When** the backup completes
**Then** the backup file is present in the `auth-backup` volume with a timestamped filename
**And** running `sqlite3 {backup-file} "SELECT count(*) FROM subscription_key"` returns a non-negative integer (backup is valid and readable)
**And** backups older than 7 days are removed from the `auth-backup` volume

**Given** a restore scenario where the `auth-db` volume is replaced with the latest backup
**When** `docker compose up auth` is run against the restored volume
**Then** the auth service starts healthy and the forwardAuth endpoint returns HTTP 200 for a previously-valid key

**Demo checkpoint:** A single sprint review demo should show: (1) Prometheus scrape returning metrics, (2) a `docker logs` grep for a known key value returning zero results, and (3) a restore from backup producing a running auth service.

### Story 5.5: Performance Validation and Concurrent Subscriber Load Test

As an operations engineer,
I want automated load tests that validate packyard's NFR performance targets,
So that I have a repeatable baseline confirming the platform handles 500 concurrent subscribers within defined latency bounds.

**Acceptance Criteria:**

**Note:** This story depends on Stories 5.1–5.3 passing in CI before the load test baseline is established. Requires a live Docker Compose stack — document this infrastructure dependency.

**Given** 50 concurrent subscribers each making repeated authenticated RPM metadata requests
**When** the k6 load test at `tests/load/` runs for 60 seconds
**Then** p95 forwardAuth response time is ≤100ms (NFR1)
**And** p95 repository metadata (`repomd.xml`) time-to-first-byte is ≤2s (NFR2)
**And** zero requests result in HTTP 200 without authentication (NFR11 — fail-closed verification)

**Given** 500 concurrent subscribers (NFR13 target)
**When** the k6 load test scales to 500 VUs
**Then** forwardAuth p95 latency remains ≤100ms with no measurable degradation versus the 50-VU baseline
**And** no HTTP 5xx errors are returned (excluding deliberate 503 fail-closed test cases)

**Given** a package download during the load test
**When** throughput is measured
**Then** no application-layer throttling is applied — download speed is bounded only by VM network capacity (NFR3)

**Given** the load test tooling
**When** the test suite is inspected
**Then** the load test is implemented with k6 and the script lives at `tests/load/packyard-load-test.js`
**And** the k6 script accepts `--env BASE_URL`, `--env KEY`, and `--env VUS` parameters for flexible execution

**Given** the load test results
**When** any NFR target is missed
**Then** the k6 run exits with a non-zero status code (threshold configured in the k6 script options)
