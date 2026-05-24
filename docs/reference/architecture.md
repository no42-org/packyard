# Architecture

## System Overview

```
   Subscribers                              Operators (humans)
       │                                            │
       ▼                                            ▼
   pkg.example.org                       admin.pkg.example.org
   (TLS 443)                             (TLS 443)
       │                                            │
       └──────────► Traefik ◄───────────────────────┘
                      │
                      ├── /rpm/   ──► nginx (RPM repos)        ─┐
                      ├── /deb/   ──► nginx → Aptly             │  forwardAuth ──► auth /auth
                      ├── /oci/   ──► Zot (OCI registry)        │
                      ├── /gpg/   ──► nginx (public, no auth)  ─┘
                      │
                      ├── (admin host) /admin/* ──► auth (embedded SPA)
                      └── (admin host) /api/v1/* ──► auth (admin API)
                                                       │
                                                       └── SQLite (operators, accounts, keys,
                                                                   components, sessions,
                                                                   audit_log)

Promotion pipeline (GitHub Actions):
    RustFS (staging) → sign → publish → rpm / deb / zot
```

The admin surface (`/admin/*` + `/api/v1/*`) and the subscriber surface
(`/rpm/`, `/deb/`, `/oci/`, `/gpg/`) share the auth service but route
through Traefik on different hostnames. Subscriber requests are
authenticated by HTTP Basic with a subscription key; operator requests are
authenticated by an OAuth-derived session cookie.

## Trust model

Packyard has three distinct trust populations. Each has its own
authentication and authorisation surface.

| Population   | Identity                                   | Auth                                                       | Surface                          |
|--------------|--------------------------------------------|------------------------------------------------------------|----------------------------------|
| Subscribers  | Subscription key (random secret)           | HTTP Basic on every request; checked via Traefik forwardAuth | `/rpm/`, `/deb/`, `/oci/` paths on `pkg.example.org` |
| Operators    | Email + GitHub or Microsoft Entra identity | OAuth Authorization Code + PKCE; session cookie thereafter | `/admin/*`, `/api/v1/*` on `admin.pkg.example.org` |
| Promotion CI | SSH key + GitHub Actions secrets           | SSH into the VM; runs `docker exec` to invoke admin tools  | Outside the trust boundary of this diagram |

### Subscriber trust

A subscription key is a 64-character hex secret bound to a single
`account_id` + `component`. The auth service:

- validates the key on every request via Traefik forwardAuth;
- enforces the key's component against the request path
  (`/rpm/{component}/…` etc.) — a `core` key cannot fetch `minion`;
- fails closed: if the auth service is unreachable, Traefik returns 503
  rather than admitting the request.

Forward-auth queries the database live on every request — component
visibility, key revocation, and account suspension take effect on the next
subscriber request with no service restart needed.

### Operator trust

Operators are humans who administer the deployment. Their identity is
their **email address**; the IdP (GitHub or Microsoft Entra) is the source
of truth for whether that email is verified and currently associated with
them. The auth service holds an allowlist (`operators` table) that maps
emails to roles. The two locks must both clear:

1. The IdP says "yes, this is verified email X".
2. Email X exists in `operators` with `status='active'`.

GitHub additionally requires that the user is an active member of the
configured org — a third lock (`ORG_MEMBERSHIP_REQUIRED`). Microsoft Entra
restricts to a single tenant — token's `tid` claim must match
`PACKYARD_MICROSOFT_TENANT_ID`.

Sessions are server-side: 32-byte random IDs in an HttpOnly + SameSite=Strict
cookie, with 8-hour idle / 24-hour absolute expiry. An admin disabling
another operator deletes that operator's session rows immediately. There is
no API token, no shared secret, and no break-glass path. See [Operator
onboarding](../ops/operator-onboarding.md) for the full flow.

Two operator roles:

- **admin** — full CRUD on accounts, keys, components, operators; can
  read the audit log.
- **readonly** — can read everything (including the audit log) but cannot
  mutate. Useful for auditors and on-call engineers who need visibility
  without write access.

A self-lockout guard refuses any operator PATCH that would leave zero
active admins. The guard is global (not just self-mutation): admin A
cannot demote/disable the last admin B either.

### Audit trail

Every mutating operator action and every authentication outcome writes a
row to the append-only `audit_log` table. Rows are immutable via the API —
the only write path is INSERT; no UPDATE/DELETE handler exists. Both
roles (admin and readonly) can query `/api/v1/audit` for forensic
investigation.

## Services

| Service | Image | Role |
|---------|-------|------|
| `traefik` | `traefik:3.6.12` | TLS termination, ACME, routing, forwardAuth middleware on subscriber paths |
| `auth` | built from `./auth` | Subscription key validation, admin API, admin SPA (embedded), Prometheus metrics |
| `rpm` | built from `./rpm` | nginx serving signed RPM repos |
| `deb` | `nginx:alpine` | nginx serving Aptly-published DEB repos |
| `zot` | `ghcr.io/project-zot/zot-linux-amd64:v2.1.2` | OCI registry with cosign signatures |
| `aptly` | `ghcr.io/no42-org/packyard-aptly:1.6.2` | DEB repo management and signing (multi-arch: amd64 + arm64) |
| `rustfs` | `rustfs/rustfs:latest` | S3-compatible staging storage for promotion pipeline |
| `static` | `nginx:alpine` | Public GPG/cosign key hosting |
| `backup` | `ghcr.io/no42-org/packyard-backup:3.48.0` | Daily SQLite backup of the key store |

The auth service binary embeds the React + TypeScript SPA via `go:embed`,
so the admin UI ships in the same single artifact as the admin API and the
subscriber forward-auth handler. One container, one port, one TLS cert per
hostname.

## Repository Layout

```
auth/               Go service — subscription key auth + admin API + embedded SPA
  cmd/server/         main()
  internal/admin-ui/  React + TypeScript SPA (Vite source tree)
  internal/adminui/   Go //go:embed wrapper + /admin/* handler
  internal/handler/   /api/v1/* HTTP handlers
  internal/middleware/  session, role, CSRF, rate-limit, CSP middleware
  internal/auth/      OAuth providers (github, microsoft) + state store
  internal/store/     SQLite-backed stores (operators, accounts, keys, sessions, audit)
aptly/              Aptly configuration and DEB repo scripts
deb/                nginx configuration for DEB serving
rpm/                nginx + createrepo_c for RPM serving
zot/                Zot OCI registry configuration
traefik/            Traefik static and dynamic configuration
rustfs/             RustFS staging storage configuration
static/             Public static files (GPG/cosign keys)
scripts/            Operator scripts (backup, stage-artifact, health-check)
docs/ops/           Operational runbooks
docs/reference/     Architecture, API, configuration, observability
tests/e2e/          End-to-end subscriber tests (RPM, DEB, OCI, observability)
tests/load/         k6 load tests for NFR validation
.github/workflows/  Promotion pipeline (RPM, DEB, OCI)
```
