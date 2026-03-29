---
stepsCompleted: ['step-01-init', 'step-02-discovery', 'step-02b-vision', 'step-02c-executive-summary', 'step-03-success', 'step-04-journeys', 'step-05-domain-skipped', 'step-06-innovation-skipped', 'step-07-project-type', 'step-08-scoping', 'step-09-functional', 'step-10-nonfunctional', 'step-11-polish', 'step-12-complete']
workflowStatus: 'complete'
completedAt: '2026-03-28'
classification:
  projectType: 'developer_tool + api_backend'
  domain: 'DevOps / Developer Infrastructure'
  complexity: 'medium-high'
  projectContext: 'greenfield'
inputDocuments: ['_bmad-output/brainstorming/brainstorming-session-2026-03-27-1630.md']
workflowType: 'prd'
brainstormingCount: 1
briefCount: 0
researchCount: 0
projectDocsCount: 0
---

# Product Requirements Document - packyard

**Author:** Indigo
**Date:** 2026-03-28

## Executive Summary

packyard is a self-hosted, authenticated artifact distribution platform for **OpenNMS Meridian** — the commercial long-term support distribution of the OpenNMS network monitoring platform. It serves as the operational infrastructure for Meridian's subscription model, delivering signed RPM, Debian, and OCI container packages to paying subscribers through standard package manager tooling (`dnf`, `apt`, `docker pull`/`crane`).

The platform hosts three Meridian components — **Core** (main server and web application), **Minion** (edge network agent), and **Sentinel** (Elasticsearch flow processor) — across four RPM targets (RHEL 8/9/10, CentOS 10), four Debian targets (Debian 12/13, Ubuntu 22.04/24.04), and OCI (x86_64 + ARM64). Meridian releases are year-versioned (`2024`, `2025`, `2026`), with multiple LTS releases coexisting permanently at stable, immutable URLs.

Access is controlled by component-scoped subscription keys validated via HTTP Basic Auth. A Core subscription key grants access exclusively to Core packages across all formats — it cannot pull Minion or Sentinel content. Keys are designed to be embedded in configuration management systems (Ansible, Terraform, Puppet) and package manager config files for years without rotation.

Community distribution of OpenNMS Horizon (the public bleeding-edge release) is out of scope — Horizon RPM/DEB packages are distributed via Cloudsmith and OCI images via GHCR. packyard is exclusively a commercial platform.

### What Makes This Special

**Component-scoped subscription enforcement at the infrastructure level.** Commercial boundaries are not enforced by policy or honour system — a subscription key physically cannot retrieve packages outside its licensed scope. This makes Meridian entitlement auditable, revocable, and automatable without any user-facing portal or manual process.

**Year-versioned LTS repositories that never change.** A customer pinned to `pkg.mdn.opennms.com/rpm/core/2025/` receives exactly what was published for Meridian 2025, indefinitely. No forced upgrades, no surprise package changes, no URL migrations between LTS cycles. Multiple Meridian years coexist on the same platform simultaneously.

**Standard tooling, zero friction.** Subscribers configure one URL, one HTTP Basic Auth credential, and one GPG key import — then interact entirely through tools they already operate. packyard has no customer-facing UI; it is infrastructure, not an application.

## Project Classification

- **Project Type:** Developer Tool + API Backend (authenticated artifact distribution platform)
- **Domain:** DevOps / Developer Infrastructure
- **Complexity:** Medium-High — multiple artifact formats, subscription key lifecycle, GPG/cosign signing pipeline, multi-OS/arch matrix, LTS versioning model
- **Project Context:** Greenfield
- **Horizon Distribution:** Out of scope (Cloudsmith for RPM/DEB, GHCR for OCI)

## Success Criteria

### User Success

A Meridian subscriber receives a subscription key and, without support intervention, completes the following within 5 minutes:

1. Configures the correct repo file for their OS (`/etc/yum.repos.d/` or `/etc/apt/sources.list.d/`) using the provided URL and HTTP Basic Auth credential
2. Imports the Meridian GPG public key
3. Successfully executes `dnf install`, `apt install`, or `docker pull` for their licensed component (Core, Minion, or Sentinel)

The same credential and repo configuration works unchanged in Ansible playbooks, Terraform provisioners, and CI/CD pipelines. No interactive steps, no portal login, no support ticket required.

### Business Success

At 3 months post-launch: **zero support tickets attributable to package access failures** — failed authentication, missing packages, corrupt metadata, or unreachable endpoints.

Secondary indicator: all ~500 Meridian subscribers at launch are able to access their licensed components without manual intervention from the OpenNMS team.

### Technical Success

- `pkg.mdn.opennms.com` available 99.9% of the time (measured monthly)
- Zero `repomd.xml` or APT `Release` metadata corruption incidents across any component/OS/year combination
- All published packages pass `gpgcheck=1` (RPM), APT signature verification (DEB), and `cosign verify` (OCI) without manual key trust steps beyond initial GPG import
- A Core subscription key returns `HTTP 401` on Minion or Sentinel paths — component scope enforcement is correct 100% of the time
- Full promotion pipeline (build → RustFS staging → GHA promotion → installable package) completes without manual intervention

### Measurable Outcomes

| Outcome | Target | How Measured |
|---|---|---|
| Subscriber onboarding time | ≤ 5 minutes from key receipt to successful install | User acceptance testing |
| Package access support tickets | 0 at 3 months | Support ticket tracking |
| Platform availability | ≥ 99.9% monthly | Uptime monitoring on pkg.mdn.opennms.com |
| Metadata integrity incidents | 0 | Promotion pipeline validation + monitoring |
| Key scope enforcement accuracy | 100% | Automated integration test suite |
| Subscriber coverage at launch | 500 served without incident | Operational metrics |

## Product Scope

### MVP — Minimum Viable Product

Everything required to serve 500 Meridian subscribers at launch:

- `pkg.mdn.opennms.com` serving RPM, DEB, and OCI for Core, Minion, and Sentinel
- Subscription key authentication via HTTP Basic Auth + forwardAuth service
- Component-scoped key enforcement (Core / Minion / Sentinel)
- **Mock admin API** — create, revoke, list, and inspect subscription keys (`POST /api/v1/keys`, `DELETE /api/v1/keys/{id}`, `GET /api/v1/keys`, `GET /api/v1/keys/{id}`)
- Year-versioned Meridian repositories (`/2025/`, `/2026/`, etc.) with coexisting LTS releases
- RPM support: RHEL 8/9/10, CentOS 10 — x86_64 only
- DEB support: Debian 12 (bookworm), Debian 13 (trixie), Ubuntu 22.04 (jammy), Ubuntu 24.04 (noble)
- OCI support: x86_64 + ARM64 multi-arch image index via Zot
- GPG-signed RPM and DEB packages (one Meridian GPG key)
- cosign key-based OCI image signing (offline verification)
- GHA promotion pipeline: RustFS staging → sign → publish
- createrepo_c serialisation lock preventing metadata corruption on concurrent pushes
- Aptly snapshot retention policy

### Growth Features (Post-MVP)

- **Real subscription management software integration** — replace mock admin API with integration to OpenNMS subscription management system
- **Bootstrap script** — OS detection, repo file write, GPG key import, subscription key prompt; one-command customer onboarding
- **`/status` endpoint** — published component versions, last-updated timestamps, backend health
- **Immutable path enforcement** — Traefik blocks `PUT`/`DELETE` on published package paths
- **Test subscription key** — publicly documented, read-only, dummy packages for toolchain verification

### Vision (Future)

- Usage analytics — per-key request counts, component popularity, geographic distribution
- Automated key expiry and renewal workflow integrated with subscription management
- Webhook notifications on anomalous key usage patterns
- CDN layer for Meridian (if subscriber scale demands it)
- Multi-region deployment for geographic redundancy

## User Journeys

### Journey 1: Alex — Meridian Subscriber, First Install (Primary User — Success Path)

**Alex** is a senior sysadmin at a mid-sized telecom company. Their team runs network monitoring across hundreds of routers and switches. The operations director has just approved upgrading from a community OpenNMS install to Meridian for the SLA-backed support. Alex has received a welcome email with three subscription keys — one each for Core, Minion, and Sentinel — and has 2 hours before the change window opens.

**Opening scene:** Alex opens the email, sees three keys and a link to the documentation. No portal, no wizard. Just credentials and a URL. Alex has configured package repos hundreds of times. This should be familiar.

**Rising action:** Alex creates `/etc/yum.repos.d/meridian-core.repo`, pastes in the `baseurl` with their Core key as the HTTP Basic Auth password, imports the Meridian GPG public key with `rpm --import`, and runs `dnf makecache`. Metadata downloads cleanly. `dnf install opennms-core` resolves dependencies and prompts for confirmation.

**Climax:** The package installs. GPG verification passes automatically — no manual trust prompt, no override flags needed. Alex runs the same steps for Minion and Sentinel using the respective keys on those two servers.

**Resolution:** The change window opens on time. All three components are installed, verified, and operational. Alex's team didn't need to open a support ticket. The keys go into the team's secrets manager for use in future Ansible runs.

*Reveals requirements for: Repo URL structure, HTTP Basic Auth, GPG key distribution, DNF/APT compatibility, per-component key enforcement.*

---

### Journey 2: Morgan — DevOps Engineer, Automated Deployment (Primary User — CI/CD Path)

**Morgan** works at a managed services provider running OpenNMS Meridian for 12 customers. Every customer environment is provisioned from the same Ansible playbooks. Morgan has been handed the Meridian subscription keys and needs to integrate them into the existing automation before the next customer provisioning run.

**Opening scene:** Morgan opens the Ansible role for OpenNMS deployment. It currently points at a community repo with no auth. The change is straightforward: swap the `baseurl`, add the `url_username` and `url_password` variables, and store the key in Ansible Vault.

**Rising action:** Morgan templates the repo file:
```ini
baseurl=https://subscriber:{{ meridian_core_key }}@pkg.mdn.opennms.com/rpm/core/2025/el9/x86_64/
```
The key lives in Vault. The playbook runs in CI against a test VM. First run: `dnf makecache` returns `HTTP 401`. Morgan checks — wrong variable name in the template. Fixes it. Second run succeeds.

**Climax:** The playbook provisions a complete Meridian environment — Core, Minion, and Sentinel — across three VMs in a single run, using three different keys from Vault. No interactive steps, no browser, no manual intervention.

**Resolution:** Morgan commits the role update. All 12 customer environments are re-provisionable from a single playbook run. The key rotation process is documented: update Vault, re-run playbook.

*Reveals requirements for: Standard HTTP Basic Auth URL embedding, meaningful HTTP 401 responses (not silent failures), stable URLs that don't change between Ansible runs.*

---

### Journey 3: Jordan — OpenNMS Operations Engineer, Key Management (Admin — Success Path)

**Jordan** is on the OpenNMS operations team. Every time a new Meridian subscription is sold, Jordan is responsible for provisioning the subscription keys and sending them to the customer. With the launch approaching and 500 subscribers to onboard, Jordan needs the mock admin API to work reliably from day one.

**Opening scene:** Jordan receives a sales handoff: "New customer — Acme Corp — licensed for Core and Minion. Provision keys." The real subscription management software isn't integrated yet. Jordan uses the mock admin API directly.

**Rising action:**
```bash
curl -X POST /api/v1/keys \
  -d '{"component": "core", "label": "Acme Corp - Core"}'
# → returns key ID and value

curl -X POST /api/v1/keys \
  -d '{"component": "minion", "label": "Acme Corp - Minion"}'
# → returns key ID and value
```
Jordan records both keys and sends them to the customer's technical contact. Two weeks later, Acme Corp's subscription lapses. Jordan revokes both keys:
```bash
curl -X DELETE /api/v1/keys/{core-key-id}
curl -X DELETE /api/v1/keys/{minion-key-id}
```

**Climax:** Acme Corp's next `dnf makecache` returns `HTTP 401`. Access is revoked without any changes to the platform — no server restart, no config reload.

**Resolution:** Jordan's daily workflow is manageable: a few API calls per new customer, a few more per lapsed one. Usage counts on each key give a rough picture of which customers are actively using what they've licensed.

*Reveals requirements for: Mock admin API (create/revoke/list/inspect), near-instant revocation, usage count tracking, meaningful key labels.*

---

### Journey 4: Sam — OpenNMS Support Engineer, Access Troubleshooting (Support Path)

**Sam** is on the OpenNMS support team. A Meridian customer opens a ticket: "I can't install the Minion package. Getting authentication errors since yesterday."

**Opening scene:** Sam pulls up the ticket. The customer is on RHEL 9, running `dnf install opennms-minion`. They get `HTTP 401`. They had it working last week.

**Rising action:** Sam checks the admin API:
```bash
curl /api/v1/keys?label=*CustomerName*
# → returns key list with active: true/false and usage_count
```
The Minion key is active. Sam checks the customer's repo file — they're using their **Core** key on the Minion repo URL. Component mismatch. Sam explains the error: the Core key returns 401 on the `/minion/` path by design. The customer needs their Minion key, which is a separate credential.

**Climax:** Sam sends the customer their correct Minion key. The customer updates their repo file. `dnf install` succeeds immediately.

**Resolution:** Sam closes the ticket in under 15 minutes. The error was clear and diagnosable — the 401 response was unambiguous, the admin API showed the key was active, and the component scope mismatch was the only possible explanation. No platform logs needed, no server access required.

*Reveals requirements for: Unambiguous HTTP 401 responses, admin API key inspection (active status, usage count, label), component scope as a visible attribute of each key.*

---

### Journey 5: Riley — Platform Engineer, Container Deployment (OCI / Integration Path)

**Riley** is a platform engineer at a large enterprise building a Kubernetes-based OpenNMS deployment. Their cluster runs on mixed hardware — some x86_64 nodes, some ARM64. They need to pull Meridian Core and Sentinel container images.

**Opening scene:** Riley has a Kubernetes deployment manifest that references container images. They need to configure an image pull secret for `pkg.mdn.opennms.com`.

**Rising action:**
```bash
kubectl create secret docker-registry meridian-core \
  --docker-server=pkg.mdn.opennms.com \
  --docker-username=subscriber \
  --docker-password=CORE_KEY
```
Riley references this secret in the deployment manifest. Kubernetes pulls `pkg.mdn.opennms.com/oci/core:2025` — the image resolves to ARM64 automatically on the ARM nodes and x86_64 on the Intel nodes without any manifest changes.

**Climax:** Riley runs `cosign verify pkg.mdn.opennms.com/oci/core:2025 --key cosign.pub` as part of the CI admission check. Verification passes offline — no outbound Sigstore dependency, which matters because the cluster is air-gapped from the public internet.

**Resolution:** The deployment is fully automated, image signatures are verified in CI, and the multi-arch manifest means the same deployment spec works on both node types. Riley adds the Sentinel pull secret using the same pattern.

*Reveals requirements for: OCI HTTP Basic Auth (standard Docker pull secret format), multi-arch image index, key-based cosign signatures verifiable offline, `pkg.mdn.opennms.com` as a valid OCI registry hostname.*

---

### Journey Requirements Summary

| Capability Area | Revealed By |
|---|---|
| HTTP Basic Auth on all endpoints (RPM, DEB, OCI) | Alex, Morgan, Riley |
| Component-scoped key enforcement with HTTP 401 | Alex, Sam |
| Stable, immutable year-versioned URLs | Alex, Morgan |
| GPG-signed packages (auto-verified by dnf/apt) | Alex |
| Meaningful 401 responses (not silent failures) | Morgan, Sam |
| Mock admin API: create, revoke, list, inspect keys | Jordan, Sam |
| Near-instant key revocation (no restart required) | Jordan |
| Key labels and usage counts | Jordan, Sam |
| Multi-arch OCI image index (x86_64 + ARM64) | Riley |
| Key-based cosign signatures, offline verification | Riley |
| OCI standard pull secret authentication | Riley |
| Per-component key scoping visible via admin API | Sam |

## Developer Tool + API Backend Specific Requirements

### Package Manager & Client Matrix

| Client | Format | Auth Method | Config Location |
|---|---|---|---|
| `dnf` / `yum` | RPM | HTTP Basic Auth in baseurl or `/etc/yum.conf` | `/etc/yum.repos.d/meridian-{component}.repo` |
| `apt` | DEB | `/etc/apt/auth.conf` (machine/login/password) | `/etc/apt/sources.list.d/meridian-{component}.list` |
| `docker` / `containerd` | OCI | Docker pull secret / `docker login` | `/etc/containerd/config.toml` or `~/.docker/config.json` |
| `crane` / `skopeo` | OCI | `--username` / `--password` flags or Docker config | CLI flags or Docker config |
| `cosign` | OCI signature | Standard OCI auth (same pull secret) | Docker config |
| Kubernetes | OCI | `imagePullSecrets` referencing docker-registry Secret | Deployment manifest |

### Installation Methods

**RPM (dnf/yum):**
```ini
# /etc/yum.repos.d/meridian-core.repo
[meridian-core-2025]
name=OpenNMS Meridian Core 2025
baseurl=https://subscriber:KEY@pkg.mdn.opennms.com/rpm/core/2025/el9/x86_64/
enabled=1
gpgcheck=1
gpgkey=https://pkg.mdn.opennms.com/gpg/meridian.asc
```

**DEB (apt):**
```
# /etc/apt/sources.list.d/meridian-core.list
deb [signed-by=/etc/apt/keyrings/meridian.gpg] https://pkg.mdn.opennms.com/deb/core/2025/ bookworm main

# /etc/apt/auth.conf
machine pkg.mdn.opennms.com login subscriber password KEY
```

**OCI (docker/containerd):**
```bash
docker login pkg.mdn.opennms.com -u subscriber -p KEY
docker pull pkg.mdn.opennms.com/oci/core:2025
```

### API Surface

**Serving endpoints** (Traefik → backends, authenticated via forwardAuth):

| Method | Path Pattern | Backend | Description |
|---|---|---|---|
| `GET` | `/rpm/{component}/{year}/{os}/{arch}/` | createrepo_c file server | RPM repo root + metadata |
| `GET` | `/rpm/{component}/{year}/{os}/{arch}/*.rpm` | createrepo_c file server | RPM package download |
| `GET` | `/deb/{component}/{year}/` | Aptly | DEB repo metadata (Release, Packages.gz) |
| `GET` | `/deb/{component}/{year}/pool/` | Aptly | DEB package download |
| `GET` | `/oci/v2/` | Zot (PathStrip `/oci`) | OCI Distribution Spec v1 API |
| `GET` | `/gpg/meridian.asc` | Static file | Meridian GPG public key (public, no auth) |

**Admin API** (versioned, internal/ops use only):

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/v1/keys` | Create subscription key |
| `GET` | `/api/v1/keys` | List all keys (optional `?component=` filter) |
| `GET` | `/api/v1/keys/{id}` | Get key detail + usage count |
| `DELETE` | `/api/v1/keys/{id}` | Revoke key (sets `active: false`, immediate effect) |

**forwardAuth validation endpoint** (called by Traefik only, not externally exposed):

| Method | Path | Description |
|---|---|---|
| `GET` | `/auth` | Validates `Authorization: Basic` header against `X-Forwarded-Uri`; returns 200 or 401 |

### Authentication Model

- **Protocol:** HTTP Basic Auth — `Authorization: Basic base64(subscriber:KEY)`
- **Username:** Fixed string `subscriber` for all clients and formats
- **Password:** Subscription key (component-scoped, long-lived)
- **Scope enforcement:** forwardAuth service extracts path segment 2 (`/rpm/{component}/...`) and validates against key's permitted component
- **Revocation:** Setting `active: false` takes effect on the next request — no cache, no delay, no server restart
- **Admin API access:** Restricted to internal/ops networks — not exposed via the same Traefik entrypoint as package serving endpoints

### Error Codes

Admin API error responses follow the Code + Message pattern (see CLAUDE.md API Error Response Guideline):

| HTTP Status | Code | Scenario |
|---|---|---|
| `400` | `INVALID_COMPONENT` | `component` value not in `[core, minion, sentinel]` |
| `401` | `KEY_INVALID` | Key not found in store |
| `401` | `KEY_REVOKED` | Key exists but `active: false` |
| `401` | `KEY_SCOPE_MISMATCH` | Key scope does not match requested path component |
| `404` | `KEY_NOT_FOUND` | Key ID not found (admin API only) |
| `500` | `INTERNAL_ERROR` | Unexpected failure |

**Example response (401 KEY_SCOPE_MISMATCH):**
```json
{
  "code": "KEY_SCOPE_MISMATCH",
  "message": "Key 'abc123...' is scoped to 'core' but requested path requires 'minion' scope",
  "component_requested": "minion",
  "key_scope": "core"
}
```

Serving endpoints (RPM/DEB/OCI) return bare `HTTP 401` with no body — package managers do not parse response bodies on auth failure.

### Data Schemas

**Key creation request:**
```json
{
  "component": "core",
  "label": "Acme Corp - Core",
  "expires_at": null
}
```

**Key response object:**
```json
{
  "id": "uuid",
  "component": "core",
  "active": true,
  "label": "Acme Corp - Core",
  "created_at": "2025-01-15T10:00:00Z",
  "expires_at": null,
  "usage_count": 1423
}
```

### Implementation Considerations

- Admin API must be accessible only from internal/ops networks — not exposed via the same Traefik entrypoint as package serving endpoints
- `GET /api/v1/keys` supports `?component=` query parameter for filtering by component scope
- Rate limiting: explicitly out of scope for MVP and post-MVP
- `/gpg/meridian.asc` is a public endpoint (no auth required) — customers import the key before configuring the authenticated repo

## Project Scoping & Phased Development

### MVP Strategy & Philosophy

**MVP Approach:** Platform MVP — minimum operationally viable for 500 Meridian subscribers at launch. No concept validation phase; the commercial relationship depends on reliability from day one.

**Resource Requirements:** Small self-operated team with Linux infrastructure, GHA pipeline, and Go/Rust development capability for the forwardAuth service.

**Meridian publish cadence:** TBD — manual `workflow_dispatch` trigger is the MVP approach; automation on release tag can be added in Phase 2 if cadence demands it.

### MVP Feature Set (Phase 1)

**Core User Journeys Supported:**
- Alex (first install via dnf/apt/docker pull)
- Morgan (automated Ansible deployment)
- Jordan (key provisioning via mock admin API)
- Sam (support troubleshooting via admin API inspection)
- Riley (Kubernetes OCI pull secret + cosign verify)

**Must-Have Capabilities:**
- `pkg.mdn.opennms.com` serving RPM, DEB, OCI for Core, Minion, Sentinel
- HTTP Basic Auth + component-scoped forwardAuth service
- Admin API v1 (create/revoke/list/inspect keys) — mock implementation
- Year-versioned Meridian repos (coexisting LTS releases)
- RPM: RHEL 8/9/10, CentOS 10, x86_64
- DEB: Debian 12/13, Ubuntu 22.04/24.04
- OCI: x86_64 + ARM64 multi-arch image index
- GPG-signed RPM/DEB (one Meridian key) + key-based cosign OCI
- GHA promotion pipeline with RustFS staging
- createrepo_c serialisation lock
- Aptly snapshot retention policy
- Public `/gpg/meridian.asc` endpoint

### Post-MVP Features

**Phase 2 — Growth:**
- Real subscription management software integration (replaces mock admin API)
- Bootstrap script (one-command customer onboarding)
- `/status` health and version endpoint
- Immutable path enforcement at Traefik level
- Test subscription key for toolchain verification

**Phase 3 — Expansion:**
- Usage analytics (per-key request counts, component popularity)
- Automated key expiry and renewal
- Webhook notifications on anomalous key usage
- CDN layer (if subscriber scale demands it)
- Multi-region deployment

### Risk Mitigation Strategy

**Technical Risks:**
- createrepo_c concurrency → serialisation lock in GHA promotion (must be Phase 1, pre-launch)
- Admin API public exposure → separate Traefik internal entrypoint for `/api/v1/` (must be Phase 1)

**Market Risks:** Minimal — platform serves an existing paying customer base; risk is operational failure, not market fit.

**Resource Risks:** If constrained, forwardAuth service can start as a minimal Go binary with SQLite backing — no external database dependency for MVP.

## Functional Requirements

### Package Access & Delivery

- **FR1:** Subscribers can download RPM packages for their licensed component using standard `dnf`/`yum` tooling
- **FR2:** Subscribers can download DEB packages for their licensed component using standard `apt` tooling
- **FR3:** Subscribers can pull OCI container images for their licensed component using standard Docker/containerd tooling
- **FR4:** Subscribers can verify OCI image signatures offline using `cosign` without any outbound internet dependency
- **FR5:** Subscribers can download the Meridian GPG public key without authentication
- **FR6:** Package managers automatically verify GPG signatures on RPM and DEB packages without manual trust configuration beyond initial key import
- **FR7:** OCI clients retrieve the architecture-appropriate image (x86_64 or ARM64) using a single image tag without specifying architecture explicitly

### Subscription Key Authentication

- **FR8:** The platform authenticates subscribers via HTTP Basic Auth on all package serving endpoints
- **FR9:** The platform enforces component scope — a key scoped to Core cannot access Minion or Sentinel package paths
- **FR10:** The platform returns HTTP 401 on requests made with an invalid, revoked, or out-of-scope key
- **FR11:** Key revocation takes effect on the next request with no server restart or configuration reload required
- **FR12:** Subscribers can embed credentials in standard OS package manager configuration files for persistent automated access
- **FR13:** Subscribers can embed credentials in Kubernetes image pull secrets for OCI access

### Key Lifecycle Management

- **FR14:** Operations staff can create a subscription key scoped to a specific component (Core, Minion, or Sentinel)
- **FR15:** Operations staff can assign a human-readable label to a subscription key at creation time
- **FR16:** Operations staff can revoke a subscription key by ID
- **FR17:** Operations staff can list all subscription keys with their active status and usage counts
- **FR18:** Operations staff can inspect a specific key's details — component scope, active status, label, creation date, and usage count
- **FR19:** Operations staff can filter the key list by component
- **FR20:** The admin API returns structured error responses with a machine-readable code and a human-readable message containing diagnostic context

### Package Publishing

- **FR21:** Build systems can stage unsigned package artefacts to the platform's object storage
- **FR22:** Release engineers can trigger promotion of staged artefacts to a specific Meridian release repository
- **FR23:** The promotion pipeline GPG-signs RPM and DEB packages during promotion
- **FR24:** The promotion pipeline cosign-signs OCI images during promotion
- **FR25:** The promotion pipeline rebuilds RPM repository metadata in a way that prevents corruption from concurrent publish operations
- **FR26:** The promotion pipeline creates an immutable point-in-time DEB repository snapshot for each Meridian release
- **FR27:** Multiple Meridian release years can be published and served simultaneously without URL conflicts

### Repository Structure & Organisation

- **FR28:** Subscribers access packages via year-versioned repository URLs that remain permanently stable after publication
- **FR29:** RPM packages are accessible via distinct repository paths for each supported OS target (RHEL 8, RHEL 9, RHEL 10, CentOS 10)
- **FR30:** DEB packages are accessible via distinct APT sources for each supported distribution (Debian 12, Debian 13, Ubuntu 22.04, Ubuntu 24.04)
- **FR31:** Repository metadata (RPM repodata, APT Release/Packages, OCI manifests) is always consistent with the available packages at any point in time

### Platform Operations

- **FR32:** Operations staff can deploy the complete platform stack on a single VM using container-based tooling
- **FR33:** The platform operates on standard Linux VMs across Azure, AWS, and KVM without cloud-provider-specific dependencies
- **FR34:** The admin API is accessible exclusively from internal/operations networks, not via the public package serving endpoints

## Non-Functional Requirements

### Performance

- **NFR1:** The forwardAuth service validates a subscription key and returns a response within 100ms — exceeding this risks package manager connection timeouts on metadata fetches
- **NFR2:** Repository metadata endpoints (`repomd.xml`, `Release`, `InRelease`, OCI manifests) deliver first byte within 2 seconds under normal load
- **NFR3:** Package download throughput is bounded only by VM network capacity — no application-layer throttling

### Security

- **NFR4:** All endpoints are served exclusively over TLS — no HTTP fallback or redirect accepted
- **NFR5:** Subscription key values are never written to application logs, access logs, or error reports in plaintext
- **NFR6:** GPG signing keys and cosign private keys are never written to disk on any server — all signing operations are ephemeral within the GHA promotion workflow only
- **NFR7:** TLS certificates use standard ACME-compatible issuance — no certificate pinning — to remain compatible with enterprise TLS inspection proxies
- **NFR8:** The admin API is unreachable via the public package serving network entrypoint — network-level isolation, not application-level filtering

### Reliability

- **NFR9:** `pkg.mdn.opennms.com` achieves 99.9% monthly availability (measured at the package serving endpoints)
- **NFR10:** Repository metadata is always in a consistent state — no partial repodata rebuild is visible to package managers at any point
- **NFR11:** forwardAuth service failure results in fail-closed behaviour (HTTP 503 to package manager) — never fail-open (HTTP 200 granting unauthenticated access)
- **NFR12:** The platform recovers to full operation after a VM restart without manual intervention beyond the container orchestration restart policy

### Scalability

- **NFR13:** The platform serves 500 concurrent authenticated subscribers without measurable degradation in forwardAuth response time or metadata delivery
- **NFR14:** Aptly snapshot and createrepo_c metadata storage grows predictably and is bounded by a defined retention policy — unbounded growth is not acceptable

### Integration

- **NFR15:** All package serving endpoints comply with their respective standards — OCI Distribution Spec v1, createrepo_c-compatible repomd.xml, standard Debian archive format (Release, Packages.gz, InRelease)
- **NFR16:** HTTP Basic Auth is the sole authentication mechanism on serving endpoints — no custom headers, query parameters, or cookies — ensuring compatibility with all standard package manager and container runtime clients
- **NFR17:** The platform operates correctly behind enterprise HTTP proxies and TLS inspection appliances without requiring custom certificate trust configuration
