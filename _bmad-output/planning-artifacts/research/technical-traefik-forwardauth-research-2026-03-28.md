---
stepsCompleted: [1, 2, 3, 4, 5, 6]
inputDocuments: []
workflowType: 'research'
lastStep: 6
status: 'complete'
completedAt: '2026-03-28'
research_type: 'technical'
research_topic: 'Traefik forwardAuth middleware configuration'
research_goals: 'Verified production-ready middlewares.yml config; exact headers Traefik sends to/from auth service; fail-closed behaviour when auth service is down'
user_name: 'Indigo'
date: '2026-03-28'
web_research_enabled: true
source_verification: true
---

# Research Report: Traefik forwardAuth Middleware Configuration

**Date:** 2026-03-28
**Author:** Indigo
**Research Type:** technical

---

## Executive Summary

This document provides a comprehensive, source-verified technical reference for implementing Traefik v3's `forwardAuth` middleware in packyard — a self-hosted authenticated artifact distribution platform. Research was conducted against Traefik v3.6.12 (released 2026-03-26, current stable), the official Traefik source code, and community-verified behaviour reports.

**The three original research goals are fully resolved:**

1. **Production-ready config** — Complete, correct `middlewares.yml`, `routers.yml`, and `traefik.yml` files are documented with all YAML key names verified against v3 docs. Key decisions: `authRequestHeaders: ["Authorization"]` only, `authResponseHeaders: []`, `maxResponseBodySize: 4096`.

2. **Exact header behaviour** — Traefik injects five `X-Forwarded-*` headers unconditionally on every forwardAuth request (`X-Forwarded-Method`, `X-Forwarded-Proto`, `X-Forwarded-Host`, `X-Forwarded-Uri`, `X-Forwarded-For`). `X-Real-Ip` is NOT set. All other headers (including `Authorization`) require explicit opt-in via `authRequestHeaders`. After auth succeeds, the `Authorization` header reaches the upstream backend unchanged.

3. **Fail-closed behaviour confirmed** — Auth service unreachable or timing out always returns HTTP 500 to the client. The 30-second timeout is hardcoded in Traefik source (`pkg/middlewares/auth/forward.go`). Docker `HEALTHCHECK` does not gate forwardAuth calls — Traefik contacts the auth service URL directly regardless.

**Key findings with packyard-specific impact:**

- forwardAuth MUST be declared before stripPrefix in the middleware chain — auth service needs the full `/oci/v2/...` path for scope enforcement
- NFR5 compliance requires suppressing BOTH `Authorization` header AND Traefik's auto-extracted `ClientUsername` field in access logs
- Component scope cannot be determined from path prefix alone — the component (`core`/`minion`/`sentinel`) lives in the filename (`meridian-core-*.rpm`) or image name (`meridian-core`)
- `stripPrefix.forceSlash` was removed in Traefik v3 — only `prefixes` list remains

**Traefik version at time of research:** v3.6.12 (released 2026-03-26, current stable)

---

## Table of Contents

1. [Research Scope and Methodology](#research-scope)
2. [forwardAuth Configuration Reference](#configuration-reference)
3. [Header Passthrough Behaviour](#header-behaviour)
4. [Failure Modes and Fail-Closed Behaviour](#failure-modes)
5. [Integration Patterns — Router Wiring](#integration-patterns)
6. [Architectural Patterns — Complete packyard Config](#architectural-patterns)
7. [Implementation — Go Auth Service](#implementation)
8. [Security Considerations](#security)
9. [Source References](#sources)

---

## Research Overview

Comprehensive technical research into Traefik's forwardAuth middleware, covering configuration options, header passthrough behaviour, failure modes, and packyard-specific wiring. All claims verified against Traefik v3 official documentation, source code, and community reports.

**Traefik version at time of research:** v3.6.12 (released 2026-03-26, current stable)

---

## Technical Research Scope Confirmation

**Research Topic:** Traefik forwardAuth middleware configuration
**Research Goals:** Verified production-ready middlewares.yml config; exact headers Traefik sends to/from auth service; fail-closed behaviour when auth service is down

**Technical Research Scope:**

- Architecture Analysis — how forwardAuth fits into Traefik's middleware chain
- Implementation Approaches — exact YAML config blocks, options and defaults
- Header passthrough — authRequestHeaders, authResponseHeaders, automatic vs opt-in
- Failure modes — timeout, connection refused, 5xx from auth service, client response in each case
- Packyard-specific wiring — three routing categories mapped to middleware attachment

**Research Methodology:**

- Current web data with rigorous source verification
- Multi-source validation for critical technical claims
- Confidence level framework for uncertain information
- Comprehensive technical coverage with architecture-specific insights

**Scope Confirmed:** 2026-03-28

---

## Technology Stack Analysis

### Traefik forwardAuth: All Configuration Options

**Source:** [Traefik forwardAuth Middleware Reference (v3)](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/)

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `address` | string | **required** | URL of the auth service. Traefik sends the auth check request here. |
| `trustForwardHeader` | bool | `false` | If true, trusts existing `X-Forwarded-*` headers rather than overwriting. Only enable behind a trusted proxy. |
| `authRequestHeaders` | []string | `[]` (all) | Allowlist of request headers to forward to the auth service. **Empty = all headers forwarded.** |
| `authResponseHeaders` | []string | `[]` | Exact header names to copy from auth response into the upstream request on 2xx. |
| `authResponseHeadersRegex` | string | `""` | Regex to match auth response headers to copy to upstream. Applied after explicit `authResponseHeaders`. |
| `addAuthCookiesToResponse` | []string | `[]` | Cookie names to copy from auth response back to the client response on 2xx. |
| `forwardBody` | bool | `false` | Send original request body to auth service. **Breaks streaming** — Traefik must buffer first. |
| `maxBodySize` | int64 | `-1` | Max bytes of request body to forward (requires `forwardBody: true`). `-1` = unlimited. |
| `maxResponseBodySize` | int64 | `-1` | Max bytes of auth response body Traefik will read. `-1` = unlimited. **Set this to avoid DoS/OOM.** Added in v3.6.9 (CVE-2026-26998). |
| `headerField` | string | `""` | If set, stores the authenticated username in this request header before forwarding to upstream. |
| `preserveLocationHeader` | bool | `false` | When auth returns non-2xx with relative `Location`, preserves it as-is. When false, Traefik rewrites to absolute URL using auth service domain. |
| `preserveRequestMethod` | bool | `false` | When false, auth requests always use GET. When true, original HTTP method is preserved. |
| `tls.ca` | string | system bundle | CA cert path for verifying auth service TLS. |
| `tls.cert` | string | `""` | Client cert path (mutual TLS). |
| `tls.key` | string | `""` | Client private key path (mutual TLS). |
| `tls.insecureSkipVerify` | bool | `false` | Skip TLS verification of auth service. Never use in production. |

**Removed in v3 (was in v2):** `tls.caOptional` — removed; TLS client auth is a server-side concern.

_Source: https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/_

### New Options in v3 (not in v2)

| Option | Added |
|--------|-------|
| `preserveRequestMethod` | v3.0 |
| `preserveLocationHeader` | v3.0 |
| `addAuthCookiesToResponse` | v3.0 |
| `forwardBody` + `maxBodySize` | v3.x |
| `maxResponseBodySize` | v3.6.9 |

_Source: https://doc.traefik.io/traefik/migrate/v2-to-v3-details/_

### Practical Configuration Examples

**Minimal — address only:**
```yaml
http:
  middlewares:
    auth-basic:
      forwardAuth:
        address: "http://authservice:9000/auth"
```

**Standard production setup with identity header injection:**
```yaml
http:
  middlewares:
    auth-full:
      forwardAuth:
        address: "https://auth.internal/verify"
        trustForwardHeader: false
        authRequestHeaders:
          - "Authorization"
          - "Cookie"
          - "X-Request-Id"
        authResponseHeaders:
          - "X-Auth-User"
          - "X-Auth-Role"
          - "X-Auth-Tenant"
        addAuthCookiesToResponse:
          - "session"
        maxResponseBodySize: 65536
        tls:
          insecureSkipVerify: false
```

**With regex header matching (copy all X-Auth-* headers):**
```yaml
http:
  middlewares:
    auth-regex:
      forwardAuth:
        address: "http://authservice:9000/auth"
        authResponseHeadersRegex: "^X-Auth-"
```

_Source: https://doc.traefik.io/traefik/v3.4/middlewares/http/forwardauth/_

---

## Integration Patterns Analysis

### Router → Middleware Attachment

Middlewares are defined under `http.middlewares` and attached to routers via a `middlewares:` array by name. They activate only when the router's rule matches, before the request is forwarded to the service.

```yaml
http:
  routers:
    rpm-router:
      entryPoints: [websecure]
      rule: "PathPrefix(`/rpm/`)"
      middlewares:
        - package-auth
      service: rpm-backend

  middlewares:
    package-auth:
      forwardAuth:
        address: "http://auth:8080/forward-auth"

  services:
    rpm-backend:
      loadBalancer:
        servers:
          - url: "http://nginx-rpm:80"
```

_Source: https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/_

### Selective Middleware Application — Three Router Categories

Traefik has no global middleware with exceptions. Selectivity is achieved via **separate routers** per route, each with its own `middlewares` array (or none). Routes on a separate entrypoint are fully isolated.

```yaml
http:
  routers:
    # Category 1: public-authenticated (/rpm/, /deb/, /oci/)
    rpm-router:
      entryPoints: [websecure]
      rule: "PathPrefix(`/rpm/`)"
      middlewares: [package-auth]
      service: rpm-backend

    deb-router:
      entryPoints: [websecure]
      rule: "PathPrefix(`/deb/`)"
      middlewares: [package-auth]
      service: aptly-backend

    oci-router:
      entryPoints: [websecure]
      rule: "PathPrefix(`/oci/`)"
      middlewares: [package-auth, strip-oci-prefix]
      service: zot-backend

    # Category 2: public-unauthenticated (/gpg/)
    gpg-router:
      entryPoints: [websecure]
      rule: "PathPrefix(`/gpg/`)"
      # No middlewares — intentionally public
      service: static-backend

    # Category 3: admin-internal — separate entrypoint (127.0.0.1:8443)
    admin-router:
      entryPoints: [admin]
      rule: "PathPrefix(`/api/v1/`)"
      service: auth-admin-backend
```

**Priority:** Routes are sorted by rule length (descending) by default. The `/gpg/`, `/rpm/`, `/deb/`, `/oci/` prefixes are non-overlapping — no priority tuning needed.

_Source: https://doc.traefik.io/traefik/v3.4/reference/routing-configuration/http/router/rules-and-priority/_

### Middleware Chaining — forwardAuth + stripPrefix

Middlewares execute in the order declared in the `middlewares:` array:

```yaml
routers:
  oci-router:
    middlewares:
      - package-auth      # runs 1st
      - strip-oci-prefix  # runs 2nd

middlewares:
  strip-oci-prefix:
    stripPrefix:
      prefixes: ["/oci"]
```

Or using a reusable chain:

```yaml
middlewares:
  auth-and-strip:
    chain:
      middlewares:
        - package-auth
        - strip-oci-prefix
```

_Source: https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/chain/_

### stripPrefix + forwardAuth — What the Auth Service Sees

**Critical ordering detail** (source-code verified via `pkg/middlewares/auth/forward.go`):

`X-Forwarded-Uri` is set from `req.URL.RequestURI()` **at the moment forwardAuth executes**. Since `req.URL` is mutated in-flight, the value depends on execution order:

- **forwardAuth BEFORE stripPrefix** → auth service sees `/oci/v2/meridian/manifests/2025` (full path)
- **stripPrefix BEFORE forwardAuth** → auth service sees `/v2/meridian/manifests/2025` (prefix gone)

**Always declare forwardAuth first.** The auth service needs the full path to enforce component scope. The backend (Zot) still receives the stripped path because stripPrefix runs second, before the request reaches upstream.

When forwardAuth runs first, the auth service sees:

| Header | Example Value |
|--------|---------------|
| `X-Forwarded-Uri` | `/oci/v2/meridian-core/manifests/2025` |
| `X-Forwarded-Method` | `GET` |
| `X-Forwarded-Host` | `pkg.mdn.opennms.com` |
| `X-Forwarded-Proto` | `https` |
| `X-Forwarded-For` | `203.0.113.45` |

When stripPrefix runs (second), it adds `X-Forwarded-Prefix: /oci` to the upstream request so Zot can reconstruct full URLs if needed.

_Source: https://github.com/traefik/traefik/blob/master/pkg/middlewares/auth/forward.go_

### File Provider Dynamic Config — Organisation and Hot-Reload

```yaml
# traefik.yml (static)
providers:
  file:
    directory: "/etc/traefik/dynamic/"
    watch: true   # default: true; uses fsnotify for hot-reload
```

**Recommended layout — split by concern:**

```
traefik/dynamic/
├── middlewares.yml       # package-auth forwardAuth definition
├── routers-public.yml    # /rpm/, /deb/, /oci/, /gpg/ routers
├── routers-admin.yml     # /api/v1/ admin router
└── services.yml          # backend service definitions
```

All files in the directory are merged at runtime. Middleware/router names must be globally unique across files. Traefik hot-reloads with no request interruption when files change (`providersThrottleDuration` defaults to 2s to prevent excessive reloads).

**Docker volume mount:** Always mount the parent **directory**, not individual files. File-level bind mounts can break fsnotify when the source file is atomically replaced.

_Source: https://doc.traefik.io/traefik/reference/install-configuration/providers/others/file/_

### Health Check and forwardAuth Interaction

**Two independent systems — they do not interact.**

Traefik load-balancer health checks probe servers listed under `http.services.*.loadBalancer.servers`. The `forwardAuth.address:` field is a raw URL called directly at request time — it is **not** a Traefik service reference and receives **no health-check probing**.

**When the auth service is unreachable:**
- Traefik returns **HTTP 500** to the client (fail-closed, confirmed)
- Docker `HEALTHCHECK` marking the auth container as `unhealthy` does **not** gate Traefik's forwardAuth calls — Traefik contacts the address directly regardless

Docker's `HEALTHCHECK` is useful for triggering container restarts via restart policy (`unless-stopped`), which limits the HTTP 500 window. But it does not prevent Traefik from attempting auth calls.

_Source: https://doc.traefik.io/traefik/reference/routing-configuration/http/load-balancing/service/_

---

## Architectural Patterns and Design

### Complete `middlewares.yml` for Packyard

```yaml
# traefik/dynamic/middlewares.yml
http:
  middlewares:

    packyard-auth:
      forwardAuth:
        address: "http://packyard-auth:8080/auth"

        # Only Authorization header needs to reach the auth service for HTTP Basic Auth.
        # This does NOT affect what headers reach the upstream backend — only what
        # the auth service receives.
        authRequestHeaders:
          - "Authorization"

        # Auth service returns only 200/401/503 — no identity headers injected.
        # Leave empty. If any header were listed here it would be STRIPPED from
        # the original request and replaced with whatever the auth service sent back.
        # Do NOT list "Authorization" here.
        authResponseHeaders: []

        # Do not forward the request body — not needed for Basic Auth validation.
        forwardBody: false

        # Limit auth service response body size. Auth service sends no body on 200;
        # 4096 bytes is a safe floor to accommodate error bodies.
        maxResponseBodySize: 4096

        # Do not trust upstream X-Forwarded-* manipulation.
        trustForwardHeader: false

    packyard-strip-oci:
      stripPrefix:
        prefixes:
          - "/oci"
        # Note: forceSlash was removed in Traefik v3. Only prefixes remains.
```

_Source: https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/_

### Complete `routers.yml` for Packyard

```yaml
# traefik/dynamic/routers.yml
http:
  routers:

    # Category 1: public-authenticated
    packyard-rpm:
      entryPoints: [websecure]
      rule: "PathPrefix(`/rpm/`)"
      middlewares: [packyard-auth]
      service: svc-rpm
      tls:
        certResolver: letsencrypt

    packyard-deb:
      entryPoints: [websecure]
      rule: "PathPrefix(`/deb/`)"
      middlewares: [packyard-auth]
      service: svc-deb
      tls:
        certResolver: letsencrypt

    packyard-oci:
      entryPoints: [websecure]
      rule: "PathPrefix(`/oci/`)"
      middlewares:
        - packyard-auth          # runs 1st — sees full /oci/v2/... path
        - packyard-strip-oci     # runs 2nd — Zot sees /v2/...
      service: svc-oci
      tls:
        certResolver: letsencrypt

    # Category 2: public-unauthenticated
    packyard-gpg:
      entryPoints: [websecure]
      rule: "PathPrefix(`/gpg/`)"
      # No middlewares — intentionally public
      service: svc-gpg
      tls:
        certResolver: letsencrypt

    # Category 3: admin-internal (loopback entrypoint only)
    packyard-admin:
      entryPoints: [admin]
      rule: "PathPrefix(`/api/v1/`)"
      service: svc-auth-admin
      # No TLS certResolver — ACME cannot issue certs for 127.0.0.1.
      # Run plaintext HTTP on the loopback (acceptable; traffic never leaves the VM).

  services:
    svc-rpm:
      loadBalancer:
        servers:
          - url: "http://nginx-rpm:80"
        passHostHeader: false

    svc-deb:
      loadBalancer:
        servers:
          - url: "http://aptly:8080"
        passHostHeader: false

    svc-oci:
      loadBalancer:
        servers:
          - url: "http://zot:5000"
        passHostHeader: false

    svc-gpg:
      loadBalancer:
        servers:
          - url: "http://nginx-static:80"
        passHostHeader: false

    svc-auth-admin:
      loadBalancer:
        servers:
          - url: "http://packyard-auth:8080"
        passHostHeader: false
```

_Source: https://doc.traefik.io/traefik/v3.3/routing/routers/_

### forwardAuth and the Authorization Header — Definitive Answer

**The Authorization header reaches the upstream backend unchanged.**

Exact request flow:
1. Client sends `GET /rpm/foo.rpm` with `Authorization: Basic dXNlcjpwYXNz`
2. Traefik calls `http://packyard-auth:8080/auth` forwarding only `Authorization` (per `authRequestHeaders`)
3. Auth service returns `200 OK` — no body, no custom headers
4. Traefik forwards original `GET /rpm/foo.rpm` to `svc-rpm` with **all original headers intact**, including `Authorization`
5. nginx RPM backend receives the Authorization header but is not configured to enforce it — it is ignored

**Why `authResponseHeaders` must NOT list `Authorization`:** If `Authorization` were listed, Traefik would strip it from the original request and replace it with whatever the auth service sent back (nothing), effectively removing the header before it reached the backend. In this case that would be harmless since backends don't enforce it — but it would be surprising behaviour. Keep `authResponseHeaders` empty.

_Source: https://github.com/traefik/traefik/issues/10524_

### Complete `traefik.yml` Static Config

```yaml
# traefik/traefik.yml

global:
  checkNewVersion: false
  sendAnonymousUsage: false

log:
  level: INFO
  format: json

accessLog:
  format: json
  bufferingSize: 100
  fields:
    defaultMode: keep
    names:
      ClientUsername: drop    # drop decoded Basic Auth username from access logs
    headers:
      defaultMode: drop
      names:
        Authorization: redact  # keep field name visible ("REDACTED"), not credentials
        User-Agent: keep
        Content-Type: keep

entryPoints:
  websecure:
    address: "0.0.0.0:443"
    http:
      tls:
        certResolver: letsencrypt

  admin:
    address: "127.0.0.1:8443"
    # No TLS — loopback only; plaintext acceptable

certificatesResolvers:
  letsencrypt:
    acme:
      email: "ops@opennms.com"
      storage: "/etc/traefik/acme/acme.json"
      tlsChallenge: {}   # Uses port 443; no port 80 required

providers:
  file:
    directory: "/etc/traefik/dynamic"
    watch: true

api:
  dashboard: true
  insecure: false

metrics:
  prometheus:
    entryPoint: traefik     # Built-in :8080 entrypoint; bind to 127.0.0.1 in production
    addEntryPointsLabels: true
    addRoutersLabels: false  # High cardinality — enable only if needed
    addServicesLabels: true
    buckets: [0.05, 0.1, 0.3, 0.5, 1.0, 2.0, 5.0]
```

_Source: https://doc.traefik.io/traefik/v3.3/reference/static-configuration/file/_

### Key Architectural Decisions Summary

| Decision | Answer | Rationale |
|---|---|---|
| `authRequestHeaders` value | `["Authorization"]` only | Only header needed for HTTP Basic Auth validation |
| `authResponseHeaders` value | `[]` (empty) | Auth service injects no identity headers |
| `maxResponseBodySize` | `4096` | Safe floor; avoids CVE-2026-26998 DoS risk |
| Authorization header at upstream | Passes through unchanged | `authResponseHeaders` empty → Traefik does not strip it |
| Admin entrypoint TLS | None (plaintext loopback) | ACME can't issue for 127.0.0.1; plaintext acceptable for loopback-only |
| stripPrefix `forceSlash` | Removed in v3 | Only `prefixes` list remains |
| forwardAuth order vs stripPrefix | forwardAuth first | Auth service must see full `/oci/v2/...` path for scope enforcement |
| `ClientUsername` in access logs | `drop` | Traefik extracts Basic Auth username; must not log it (NFR5) |

_Sources: https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/ · https://doc.traefik.io/traefik/reference/install-configuration/observability/logs-and-accesslogs/_

---

## Implementation Approaches and Technology Adoption

### Go forwardAuth Handler Pattern

The handler receives Traefik's auth request. Use `r.BasicAuth()` (stdlib) for credential extraction — no manual base64 needed:

```go
// handler/auth.go
func (h *AuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    username, key, ok := r.BasicAuth()
    if !ok || username != "subscriber" || len(key) != 64 {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    forwardedURI := r.Header.Get("X-Forwarded-Uri")
    component, err := componentFromURI(forwardedURI)
    if err != nil {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    sub, err := h.store.GetKeyByValue(r.Context(), key)
    if err != nil {
        if errors.Is(err, sql.ErrNoRows) {
            w.WriteHeader(http.StatusUnauthorized)
            return
        }
        h.logger.ErrorContext(r.Context(), "store lookup failed", slog.String("error", err.Error()))
        w.WriteHeader(http.StatusServiceUnavailable)
        return
    }

    if sub.Scope != "all" && sub.Scope != component {
        w.WriteHeader(http.StatusUnauthorized)
        return
    }

    w.WriteHeader(http.StatusOK) // no body — Traefik ignores it; dnf/apt/docker don't parse it
}
```

**Key points:**
- `r.BasicAuth()` returns `(username, password, ok)`. Returns false if header absent, malformed, or not base64-decodable.
- Return `WriteHeader` with **no body** — Traefik's forwardAuth ignores bodies; writing one wastes bandwidth.
- `503` on store error is the correct fail-closed sentinel.

### Component Scope Enforcement

Component lives in different positions by protocol. Use dispatch + filename extraction (not a single regex):

```go
// component/resolver.go
var knownComponents = map[string]bool{
    "core": true, "minion": true, "sentinel": true,
}

func componentFromURI(uri string) (string, error) {
    if i := strings.IndexByte(uri, '?'); i != -1 {
        uri = uri[:i]
    }
    switch {
    case strings.HasPrefix(uri, "/rpm/"):
        return extractMeridianComponent(path.Base(uri))
    case strings.HasPrefix(uri, "/deb/"):
        return extractMeridianComponent(path.Base(uri))
    case strings.HasPrefix(uri, "/oci/"):
        rest := strings.TrimPrefix(uri, "/oci/v2/")
        return extractMeridianComponent(strings.SplitN(rest, "/", 2)[0])
    default:
        return "", ErrUnknownPath
    }
}

// extractMeridianComponent finds "meridian-{component}" and validates against allowlist.
func extractMeridianComponent(s string) (string, error) {
    const prefix = "meridian-"
    idx := strings.Index(s, prefix)
    if idx == -1 {
        return "", ErrUnknownPath
    }
    rest := s[idx+len(prefix):]
    for i, c := range rest {
        if c == '-' || c == '_' || c == '/' || c == '.' {
            if knownComponents[rest[:i]] {
                return rest[:i], nil
            }
            return "", ErrUnknownPath
        }
    }
    if knownComponents[rest] {
        return rest, nil
    }
    return "", ErrUnknownPath
}
```

**Edge case:** `/rpm/repodata/repomd.xml` has no product filename. Decide: allow without key (public) or require any-component key. Add a prefix check before extraction if allowing public repodata.

### chi v5 Router Setup

```go
// server/routes.go
func NewRouter(authH *handler.AuthHandler, keysH *handler.KeysHandler, logger *slog.Logger) http.Handler {
    r := chi.NewRouter()
    r.Use(middleware.RequestID)
    r.Use(middleware.RealIP)
    r.Use(middleware.Recoverer)

    // Use HandleFunc not Get — defensive against preserveRequestMethod: true
    r.HandleFunc("/auth", authH.ServeHTTP)
    r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
        w.WriteHeader(http.StatusOK)
    })

    r.Route("/api/v1", func(r chi.Router) {
        r.Route("/keys", func(r chi.Router) {
            r.Post("/", keysH.Create)
            r.Get("/", keysH.List)
            r.Get("/{id}", keysH.GetByID)
            r.Delete("/{id}", keysH.Delete)
        })
    })

    return r
}
```

URL parameter extraction: `chi.URLParam(r, "id")`.

### modernc.org/sqlite — Opening with WAL Mode

```go
import (
    "database/sql"
    _ "modernc.org/sqlite"   // driver name: "sqlite" (not "sqlite3")
)

const dsn = "file:packyard.db" +
    "?_pragma=journal_mode(WAL)" +
    "&_pragma=synchronous(NORMAL)" +
    "&_pragma=busy_timeout(5000)" +
    "&_pragma=foreign_keys(ON)"

db, err := sql.Open("sqlite", dsn)
db.SetMaxOpenConns(1)   // single writer prevents SQLITE_BUSY
db.SetMaxIdleConns(1)
```

**vs. mattn/go-sqlite3:** `modernc.org/sqlite` is pure Go — no CGo, no gcc required. Cross-compilation (`GOOS=linux GOARCH=arm64`) works with `go build` alone. Performance difference (~5–15%) is irrelevant for single-row auth lookups.

### Testing Pattern

```go
// handler/auth_test.go
type mockStore struct {
    key handler.SubscriptionKey
    err error
}

func (m *mockStore) GetKeyByValue(_ context.Context, _ string) (handler.SubscriptionKey, error) {
    return m.key, m.err
}

func TestAuthHandler_ScopeMismatch(t *testing.T) {
    store := &mockStore{
        key: handler.SubscriptionKey{Scope: "minion", Active: true},
    }
    h := handler.NewAuthHandler(store, slog.Default())

    req := httptest.NewRequest(http.MethodGet, "/auth", nil)
    req.SetBasicAuth("subscriber", strings.Repeat("a", 64))
    req.Header.Set("X-Forwarded-Uri", "/rpm/el9/x86_64/meridian-core-2025.1.0.x86_64.rpm")
    rr := httptest.NewRecorder()

    h.ServeHTTP(rr, req)

    if rr.Code != http.StatusUnauthorized {
        t.Errorf("got %d, want 401", rr.Code)
    }
    if rr.Body.Len() > 0 {
        t.Errorf("expected empty body, got %q", rr.Body.String())
    }
}
```

For `SQLiteStore` integration tests, use `":memory:"` as DSN. For shared in-memory DB across multiple opens: `"file:testdb?mode=memory&cache=shared"`.

### Technical Research Recommendations

#### Implementation Roadmap

1. Define `Store` interface in `internal/store/store.go` before writing any handler code
2. Implement `AuthHandler` against the interface with `httptest`-based unit tests
3. Implement `SQLiteStore` with `:memory:` integration tests
4. Wire chi router and test end-to-end with `httptest.NewServer`
5. Add `slog.NewJSONHandler(os.Stdout, nil)` for production structured logging

#### Technology Stack Confirmed

- Go 1.26.1 — `log/slog`, `r.BasicAuth()`, `net/http/httptest` all available
- `github.com/go-chi/chi/v5` v5.2.5 — middleware chains, route grouping
- `modernc.org/sqlite` v1.47.0 (SQLite 3.51.2) — pure Go, WAL mode, no CGo
- `database/sql` stdlib — prepared statements, `sql.ErrNoRows` sentinel

#### Success Metrics (Auth Service)

- `/auth` responds in <100ms (NFR1) — SQLite WAL single-row lookup is ~0.1–0.5ms
- Returns 200, 401, or 503 only — no other status codes
- Zero body on all responses
- `Authorization` header never appears in `log/slog` output
- `GetKeyByValue` prepared statement reused across requests (no statement re-compilation)

_Sources: https://pkg.go.dev/github.com/go-chi/chi/v5 · https://pkg.go.dev/modernc.org/sqlite · https://pkg.go.dev/log/slog · https://go.dev/blog/routing-enhancements_

---

## Security Considerations

### NFR5 Compliance — Two Suppression Points Required

The architecture document's C3 critical gap identified `Authorization` header redaction. Research revealed a second leak vector:

Traefik automatically decodes HTTP Basic Auth and logs the **username** separately as `ClientUsername` in access log records — independently of the `Authorization` header. Full NFR5 compliance requires suppressing both:

```yaml
accessLog:
  fields:
    names:
      ClientUsername: drop       # Traefik-extracted username field
    headers:
      names:
        Authorization: redact    # Header itself (value shown as "REDACTED")
```

Both suppressions are required. The `Authorization: redact` alone still leaks usernames.

### CVE-2026-26998 — Unbounded Auth Response Body

The `maxResponseBodySize` default of `-1` (unlimited) is flagged as a DoS/OOM risk in Traefik's security advisory. Set to `4096` in production. Added in v3.6.9.

### `trustForwardHeader: false` (default)

Never enable `trustForwardHeader: true` unless Traefik sits behind a trusted reverse proxy that sets `X-Forwarded-For`. On a direct-to-internet VM, an attacker could spoof `X-Forwarded-For` to bypass IP-based rate limiting or logging.

### Admin Entrypoint — No ACME on Loopback

ACME (Let's Encrypt) cannot issue certificates for `127.0.0.1`. The admin entrypoint at `127.0.0.1:8443` should run over plaintext HTTP. This is acceptable — traffic never leaves the VM. Do not attempt to add `tlsChallenge` to the admin entrypoint.

---

## Source References

| Source | URL |
|--------|-----|
| Traefik v3 forwardAuth Reference | https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/ |
| Traefik v3.4 forwardAuth Docs | https://doc.traefik.io/traefik/v3.4/middlewares/http/forwardauth/ |
| Traefik v2 → v3 Migration | https://doc.traefik.io/traefik/migrate/v2-to-v3-details/ |
| Traefik Static Config File Reference | https://doc.traefik.io/traefik/v3.3/reference/static-configuration/file/ |
| Traefik EntryPoints Reference | https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/ |
| Traefik File Provider Reference | https://doc.traefik.io/traefik/reference/install-configuration/providers/others/file/ |
| Traefik Access Logs Reference | https://doc.traefik.io/traefik/reference/install-configuration/observability/logs-and-accesslogs/ |
| Traefik Prometheus Metrics | https://doc.traefik.io/traefik/reference/install-configuration/observability/metrics/ |
| Traefik Load Balancing Service | https://doc.traefik.io/traefik/reference/routing-configuration/http/load-balancing/service/ |
| Traefik Router Rules and Priority | https://doc.traefik.io/traefik/v3.4/reference/routing-configuration/http/router/rules-and-priority/ |
| Traefik Chain Middleware | https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/chain/ |
| Traefik GitHub Releases | https://github.com/traefik/traefik/releases |
| GitHub Issue #9857 — Configurable forwardAuth timeout | https://github.com/traefik/traefik/issues/9857 |
| GitHub Issue #10524 — authResponseHeaders strips original headers | https://github.com/traefik/traefik/issues/10524 |
| Security Advisory CVE-2026-26998 | https://github.com/traefik/traefik/security/advisories/GHSA-fw45-f5q2-2p4x |
| forwardAuth source: forward.go | https://github.com/traefik/traefik/blob/master/pkg/middlewares/auth/forward.go |
| chi v5 — pkg.go.dev | https://pkg.go.dev/github.com/go-chi/chi/v5 |
| modernc.org/sqlite — pkg.go.dev | https://pkg.go.dev/modernc.org/sqlite |
| Go log/slog | https://pkg.go.dev/log/slog |
| Go 1.22 Routing Enhancements | https://go.dev/blog/routing-enhancements |
| Structured Logging with slog | https://go.dev/blog/slog |

---

**Research completed:** 2026-03-28
**Traefik version:** v3.6.12 (current stable at time of research)
**All technical claims verified against official documentation, source code, and community-validated behaviour reports.**
