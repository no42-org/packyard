---
stepsCompleted: [1, 2, 3, 4]
inputDocuments: []
session_topic: 'Designing a multi-format artifact hosting platform for OpenNMS (RPM, Debian, OCI)'
session_goals: 'Identify architecture, tooling, and design decisions for hosting 18 repository channels across 3 components, 2 distributions, and 3 formats with dual-tier access control'
selected_approach: 'ai-recommended'
techniques_used: ['Morphological Analysis', 'First Principles Thinking', 'Six Thinking Hats']
ideas_generated: 22
session_active: false
workflow_completed: true
context_file: ''
---

# Brainstorming Session: OpenNMS Package Repository Platform

**Facilitator:** Indigo
**Date:** 2026-03-27

---

## Session Overview

**Topic:** Designing a multi-format artifact hosting platform for OpenNMS (RPM, Debian, OCI)

**Goals:** Identify architecture, tooling, and design decisions for hosting 18 repository channels across 3 components (Core, Minion, Sentinel), 2 distributions (Horizon public / Meridian authenticated LTS), and 3 formats (RPM, DEB, OCI) with dual-tier access control.

### Components

| Component | Description | Formats |
|---|---|---|
| **Core** | Main server + web application | RPM, DEB, OCI |
| **Minion** | Edge network agent for remote device access | RPM, DEB, OCI |
| **Sentinel** | Elasticsearch flow persistence for data center | RPM, DEB, OCI |

### Distributions

| Distribution | Access | Model |
|---|---|---|
| **Horizon** | Public, no auth | Rolling/bleeding-edge releases (version numbers: 35, 36...) |
| **Meridian** | Subscription key required | Stabilised LTS (year-based: 2024, 2025, 2026...) |

### Platform Facts

- **RPM targets:** RHEL 8/9/10, CentOS 10 (`el8`, `el9`, `el10`)
- **DEB targets:** Debian 12 (bookworm), Debian 13 (trixie), Ubuntu 22.04 (jammy), Ubuntu 24.04 (noble)
- **Architecture:** RPM/DEB x86_64 only; OCI multi-arch (x86_64 + ARM64)
- **Infrastructure:** Self-operated VMs on Azure, AWS, or KVM
- **Meridian subscribers at launch:** ~500
- **Package count per full release:** 36 packages (12 RPM + 12 DEB) + 9 OCI image indexes

---

## Technique Selection

**Approach:** AI-Recommended Techniques

| Phase | Technique | Category | Purpose |
|---|---|---|---|
| 1 | Morphological Analysis | deep | Map all decision axes and options systematically |
| 2 | First Principles Thinking | creative | Challenge assumptions; rebuild from what users actually need |
| 3 | Six Thinking Hats | structured | Stress-test the full design from six perspectives |

---

## Technique Execution

### Phase 1: Morphological Analysis

Systematically explored 6 decision parameters across the full solution space.

---

#### Parameter 1: Hosting Platform & Tooling

**Decision:** Traefik as ingress + Aptly (DEB) + createrepo_c (RPM) + Zot (OCI)

**Rationale:**
- Aptly's snapshot model maps directly onto Meridian's LTS release pattern — a Meridian 2025 snapshot is taken once and never changes
- createrepo_c is the de facto RPM metadata standard — no viable alternative
- Zot is CNCF-native, OCI-spec compliant, ~15MB binary, supports auth and garbage collection
- Traefik provides uniform TLS, path routing, and auth middleware across all three backends

**Alternatives considered:**
- Pulp (Red Hat) — more feature-complete but significantly heavier operationally
- Reprepro — simpler than Aptly but no snapshot/promotion model; poorly suited for Meridian LTS
- Harbor — too heavy; designed for enterprise Kubernetes environments
- SaaS (Cloudsmith, Packagecloud) — eliminated in favour of self-hosted

---

#### Parameter 2: Authentication Mechanism

**Decision:** Traefik `forwardAuth` middleware → custom subscription key validation service

**Rationale:**
- Auth boundary enforced at the DNS/domain level — `pkg.mdn.opennms.com` globally authenticated, `pkg.hzn.opennms.com` globally public
- Three subscription keys: one per component (Core, Minion, Sentinel)
- Each key grants access to all formats (RPM, DEB, OCI) for that component only
- A Core key cannot pull Minion or Sentinel packages — component-scoped enforcement
- ForwardAuth decouples auth logic from all backends — change auth without touching Aptly, createrepo_c, or Zot

**Alternatives considered:**
- Authelia / Authentik — designed for user sessions, not machine-to-machine CI/CD credentials
- Per-backend auth — diverges over time, three configs to maintain
- Traefik native basicAuth — insufficient for component-scope enforcement logic

---

#### Parameter 3: Repository URL Structure

**Decision:** Two domains, format-as-path-prefix

```
# Horizon — public, no auth
pkg.hzn.opennms.com/rpm/{core,minion,sentinel}/{version}/el{8,9,10}/x86_64/
pkg.hzn.opennms.com/deb/{core,minion,sentinel}/{version}/     ← APT codename in sources.list
pkg.hzn.opennms.com/oci/{core,minion,sentinel}:{version}

# Meridian — subscription key required (HTTP Basic Auth)
pkg.mdn.opennms.com/rpm/{core,minion,sentinel}/{year}/el{8,9,10}/x86_64/
pkg.mdn.opennms.com/deb/{core,minion,sentinel}/{year}/
pkg.mdn.opennms.com/oci/{core,minion,sentinel}:{year}
```

**Example customer configs:**

```ini
# /etc/yum.repos.d/meridian-core.repo
[meridian-core]
baseurl=https://subscriber:KEY@pkg.mdn.opennms.com/rpm/core/2025/el9/x86_64/
gpgcheck=1
```

```
# /etc/apt/auth.conf
machine pkg.mdn.opennms.com login subscriber password KEY

# /etc/apt/sources.list.d/meridian-core.list
deb [signed-by=/etc/apt/keyrings/meridian.gpg] https://pkg.mdn.opennms.com/deb/core/2025/ bookworm main
```

**OCI notes:**
- Traefik PathStrip middleware removes `/oci` prefix before forwarding to Zot
- Zot image names: `core`, `minion`, `sentinel` — version expressed as tag
- Multi-arch image index per tag — single tag resolves to x86_64 or ARM64 automatically
- `pkg.hzn.opennms.com/oci/core:35` pulls correctly on both architectures

**Rationale for design choices:**
- `hzn`/`mdn` subdomains create a hard auth boundary at DNS — no per-path auth rules, no misconfiguration risk
- Format-first path (`/rpm/`, `/deb/`, `/oci/`) enables Traefik backend routing by first path segment
- Component-second path maps directly to subscription key scope validation
- Meridian year-in-path allows multiple LTS releases to coexist (`/2025/` and `/2026/` simultaneously)
- Single unified domain per distribution reduces cert and DNS management overhead

---

#### Parameter 4: CDN / Delivery Strategy

**Decision:** Origin-only, CDN-deferred

**Rationale:** ~500 Meridian subscribers at launch; traffic volume does not justify CDN complexity. The two-domain structure creates a clean CDN insertion point — `pkg.hzn.opennms.com` can be proxied through Cloudflare at any time by a DNS change alone, with no changes to Traefik, backends, or customer configs.

**When to revisit:** When Horizon traffic causes measurable origin load or when geographic latency becomes a support issue.

---

#### Parameter 5: CI/CD & Promotion Pipeline

**Decision:** RustFS staging bucket → GHA promotion workflow

```
CircleCI builds  ──┐
                   ├──► RustFS staging bucket (unsigned artefacts)
GHA builds       ──┘           │
                               ▼
                    GHA promotion workflow
                    ├── pull from RustFS
                    ├── GPG sign RPM/DEB (ephemeral key)
                    ├── cosign sign OCI (ephemeral key)
                    ├── push to Aptly / createrepo_c / Zot
                    └── rebuild metadata

                    Horizon: automatic on release tag
                    Meridian: manual workflow_dispatch (component + year inputs)
```

**Key properties:**
- Neither CircleCI nor GHA holds repo credentials or signing keys — only the promotion workflow does
- Horizon and Meridian promotions are separate pipeline runs with the same staged artefacts as input
- RustFS runs as a single container in the stack; S3-compatible endpoint swappable by environment variable
- The Meridian promotion gate is a deliberate human approval step

**Alternatives considered:**
- Direct CI push to repo backends — eliminated due to signing key exposure in two CI systems and metadata rebuild race conditions
- GitHub Releases as source of truth — adds latency; polling/webhook complexity outweighs benefit

---

#### Parameter 6: Package Signing & Trust

**Decision:** Two GPG keys (Horizon / Meridian) + key-based cosign for OCI

| Surface | Method | Key Storage |
|---|---|---|
| RPM packages | GPG (embedded signature + `repomd.xml`) | GHA encrypted secret |
| DEB packages | GPG (`Release` file via Aptly) | GHA encrypted secret |
| OCI images | cosign key-based | GHA encrypted secret |

**Rationale:**
- Two GPG keys (one per distribution): a Horizon key compromise does not affect Meridian package trust
- Key-based cosign (not keyless/Sigstore): works fully offline — no outbound Sigstore dependency; critical for enterprise/data-centre deployments with restricted outbound firewall rules
- All keys imported ephemerally during promotion workflow, never written to disk on any server
- Cosign signatures stored in Zot alongside images — `cosign verify pkg.mdn.opennms.com/oci/core:2025` works against own registry

**GPG key rotation:** Customer-impacting event requiring re-import of public key. Needs documented migration path and advance notice period for 500 Meridian subscribers.

---

### Phase 2: First Principles Thinking

Challenged the assumption that auth required a custom protocol. Derived from what package managers actually speak.

**Bedrock truth stress-tested:** *"Meridian customers need a credential they can embed in a config file that survives reboots and automation."*

**Finding:** All three package managers (dnf, apt, OCI clients) speak HTTP Basic Auth natively. No custom headers, no bearer tokens, no query parameters needed.

**Design implications:**
- Subscription key = HTTP Basic Auth **password**
- Username = fixed string `subscriber` (consistent across all formats)
- ForwardAuth service receives standard `Authorization: Basic` header — no custom parsing
- Credential is a static string: storable in HashiCorp Vault, Ansible Vault, AWS Secrets Manager; templatable in Ansible/Terraform/Puppet without special handling

**Auth service internal data model (prototype-ready, extensible):**

```
Key {
  id:          string        // the subscription key value
  component:   enum          // core | minion | sentinel
  active:      bool          // revocation flag
  created_at:  timestamp
  expires_at:  timestamp?    // null = no expiry (prototype default)
  usage_count: int           // incremented per validated request
  label:       string        // e.g. "Acme Corp - Core"
}
```

**Mock admin API (prototype interface — replaced by real subscription management software):**

```
POST   /admin/keys         # create key, assign component scope
DELETE /admin/keys/{id}    # revoke key (sets active: false)
GET    /admin/keys         # list all keys with usage counts
GET    /admin/keys/{id}    # get key detail

GET    /auth               # forwardAuth endpoint (called by Traefik)
                           # reads Authorization header + X-Forwarded-Uri
                           # returns 200 (allow) or 401 (deny)
```

**Integration contract:** The real subscription management software replaces the mock by calling the same admin API. The repo platform has no knowledge of billing, customer accounts, or subscription state — it only knows keys.

---

### Phase 3: Six Thinking Hats

Full stress-test of the confirmed design.

#### White Hat — Facts

Confirmed platform matrix:

| | RPM | DEB | OCI |
|---|---|---|---|
| **Horizon** | `pkg.hzn.opennms.com/rpm/{component}/35/el{8,9,10}/x86_64/` | `pkg.hzn.opennms.com/deb/{component}/35/` | `pkg.hzn.opennms.com/oci/{component}:35` |
| **Meridian** | `pkg.mdn.opennms.com/rpm/{component}/2025/el{8,9,10}/x86_64/` | `pkg.mdn.opennms.com/deb/{component}/2025/` | `pkg.mdn.opennms.com/oci/{component}:2025` |

Package count per full release: 12 RPM + 12 DEB + 9 OCI manifests (3 components × 2 arches + 1 image index each).

#### Red Hat — Intuition & Concerns

- Stack has meaningful operational surface area for a self-operated team — mitigated by composability and independent replaceability of components
- Aptly snapshot database grows over time without a retention policy — must be addressed from day one
- RustFS is least battle-tested component — acceptable risk as staging-only; GHA re-runs are cheap and Git tag is the source of truth
- GHA as single path to production is a risk — acceptable for open source release cadence
- 500 Meridian subscribers requires a functional key issuance interface from day one — addressed by mock admin API

#### Yellow Hat — Benefits

- Every component independently replaceable without customer-facing URL changes
- Two-domain auth boundary is structurally enforced — cannot accidentally expose Meridian content
- Customer-facing simplicity despite 18-channel backend complexity — one URL, one credential, one GPG key per customer
- GHA promotion pipeline is auditable by default — workflow run ID, commit SHA, timestamp, triggered-by on every published package
- Multi-arch OCI with single tag — zero friction for customers on ARM64
- Aptly snapshots mean Meridian 2025 customers are permanently stable regardless of future releases

#### Black Hat — Risks

| Risk | Severity | Mitigation |
|---|---|---|
| createrepo_c has no file locking | High | GHA promotion must serialise RPM publish jobs per component per OS target |
| Aptly snapshot proliferation | Medium | Retention policy from day one — archive and prune old Meridian snapshots |
| GPG key rotation is customer-impacting | Medium | Documented migration path + advance notice process for 500 subscribers |
| RustFS data loss in staging | Low | Staging is not source of truth — Git tag + re-run promotion workflow |
| 72 packages per full release (CI cost) | Low | Monitor GHA minutes; cache build dependencies aggressively |

#### Green Hat — Creative Enhancements

*Not in prototype scope — high value for GA:*

- **Bootstrap script:** OS detection → write correct repo file → import GPG key → prompt for subscription key. One command customer onboarding.
- **`/status` endpoint:** JSON page listing published component versions, last-updated timestamps, backend health. Customer debugging + operational monitoring.
- **GHCR mirror for Horizon OCI:** `ghcr.io/opennms/core:35` as zero-config fallback — many users already have GHCR configured.
- **Test subscription key:** Publicly documented, read-only, dummy packages. Lets customers verify toolchain without a real subscription.
- **Immutable path enforcement:** Traefik blocks `PUT`/`DELETE` on published package paths — packages are append-only in production.

#### Blue Hat — Execution Sequence

```
Phase 1 — Core stack
  Components: Traefik + Zot + Aptly + createrepo_c file server
  Goal: Two domains, path routing, no auth
  Validate: dnf/apt can install a test package from both domains

Phase 2 — Auth layer
  Components: ForwardAuth service + mock admin API
  Goal: Apply to pkg.mdn.opennms.com; enforce component key scope
  Validate: Core key rejected on Minion path; Meridian path returns 401 without key

Phase 3 — CI/CD pipeline
  Components: RustFS + GHA promotion workflow
  Goal: Full pipeline from build to installable package
  Validate: CircleCI → RustFS → GHA promotion → pkg.hzn.opennms.com → dnf install

Phase 4 — Signing
  Components: GPG key pair + cosign key pair in GHA secrets
  Goal: Signed packages served from both domains
  Validate: dnf/apt gpgcheck passes; cosign verify passes offline against Zot

Phase 5 — Hardening
  Items: createrepo_c serialisation lock, Aptly snapshot retention policy,
         /status endpoint, immutable path enforcement in Traefik
  Goal: Production-ready behaviour
  Validate: Concurrent push test does not corrupt repomd.xml
```

---

## Idea Organization and Prioritisation

### Complete Idea Inventory

**Infrastructure & Stack (confirmed)**
- Traefik + Aptly + createrepo_c + Zot + RustFS as the core stack
- Origin-only delivery, CDN insertion point deferred to DNS layer

**Access Control & Authentication (confirmed)**
- Component-scoped subscription keys (Core / Minion / Sentinel)
- HTTP Basic Auth as the client-facing protocol
- Custom forwardAuth service with extensible key data model
- Mock admin API as clean integration contract for subscription management software

**URL & Repository Structure (confirmed)**
- Two domains: `pkg.hzn.opennms.com` (public) and `pkg.mdn.opennms.com` (authenticated)
- Format-as-path-prefix routing to three backends
- Horizon: release version numbers; Meridian: year-based versioning with coexisting LTS releases
- OCI: version as tag, PathStrip middleware, multi-arch image index

**CI/CD & Promotion Pipeline (confirmed)**
- RustFS staging → GHA promotion workflow
- Horizon: automatic on release tag; Meridian: manual workflow_dispatch
- Signing keys ephemeral in GHA secrets only

**Trust & Security (confirmed)**
- Two GPG keys (Horizon / Meridian split)
- Key-based cosign for OCI — fully offline verification
- createrepo_c serialisation required to prevent metadata corruption

**Operational Enhancements (backlog)**
- Bootstrap script for one-command customer onboarding
- `/status` health and version endpoint
- GHCR mirror for Horizon OCI
- Test subscription key for toolchain verification
- Immutable package path enforcement in Traefik
- Aptly snapshot retention policy

### Prioritisation

| Priority | Item | Rationale |
|---|---|---|
| **P1** | Full stack prototype (Phases 1–4) | Validates entire pipeline end-to-end |
| **P2** | createrepo_c serialisation lock | Silent data corruption risk before first real publish |
| **P3** | Mock admin API + test subscription key | Required to verify Meridian auth without real subscription software |
| **P4** | `/status` endpoint | Low effort, high value for operations and customer support |
| **P5** | Bootstrap script | Significant onboarding friction reduction at 500 subscribers |
| **P6** | GHCR mirror + Aptly retention policy | Polish — before GA, not before prototype |

---

## Session Summary

**Key architectural decisions made:**

1. Self-hosted stack: Traefik + Aptly + createrepo_c + Zot + RustFS
2. Two-domain model with auth boundary at DNS level (`hzn` / `mdn`)
3. Component-scoped subscription keys over HTTP Basic Auth
4. Custom forwardAuth service with clean API contract for subscription management integration
5. RustFS staging + GHA promotion pipeline as the single path to production
6. Two GPG keys (per distribution) + key-based cosign (fully offline)
7. Meridian year-versioning in URL path enabling coexisting LTS releases
8. Origin-only delivery, CDN-deferred

**Critical implementation notes:**
- createrepo_c **must** have serialisation enforced in the promotion pipeline — concurrent metadata rebuilds corrupt `repomd.xml`
- Aptly snapshot retention policy needed from day one
- GPG key rotation requires a customer communication and migration process

**Design principles that emerged:**
- Every component independently replaceable without customer-facing impact
- External protocol simplicity (vanilla HTTP Basic Auth) with internal extensibility
- Auth enforced structurally (DNS level), not procedurally (per-path rules)
- Staging as isolation layer — neither CI system holds production credentials

---

*Session document generated: 2026-03-27*
*Output path: `_bmad-output/brainstorming/brainstorming-session-2026-03-27-1630.md`*
