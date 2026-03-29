---
stepsCompleted: [1, 2, 3, 4, 5, 6]
inputDocuments: []
workflowType: 'research'
lastStep: 6
research_type: 'technical'
research_topic: 'Traefik v3 forwardAuth middleware integration patterns'
research_goals: 'Understand router/middleware attachment, selective application, chaining, stripPrefix ordering, file provider dynamic config, and healthcheck interaction for Traefik v3'
user_name: 'Indigo'
date: '2026-03-28'
web_research_enabled: true
source_verification: true
---

# Research Report: Traefik v3 forwardAuth Middleware Integration Patterns

**Date:** 2026-03-28
**Author:** Indigo
**Research Type:** Technical

---

## Research Overview

This report answers six targeted questions about Traefik v3's forwardAuth middleware. All findings are sourced from the official Traefik v3 documentation, Traefik GitHub source code, and verified community resources. The research is implementation-ready — YAML examples are exact, key names are verified against the current canonical docs at `doc.traefik.io`.

**Primary sources consulted:**
- `doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/`
- `doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/chain/`
- `doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/stripprefix/`
- `doc.traefik.io/traefik/v3.3/routing/routers/` (router rules and priority)
- `doc.traefik.io/traefik/v3.4/reference/routing-configuration/http/router/rules-and-priority/`
- `doc.traefik.io/traefik/reference/install-configuration/providers/others/file/`
- `github.com/traefik/traefik` — `pkg/middlewares/auth/forward.go` (source-code verified)

---

## Technical Research Scope Confirmation

**Research Topic:** Traefik v3 forwardAuth middleware integration patterns
**Research Goals:** Verified, current integration patterns for production use — specifically router attachment, selective application, chaining, path ordering with stripPrefix, file provider best practices, and healthcheck behaviour.

**Scope Confirmed:** 2026-03-28

---

## Section 1: Router → Middleware Attachment (Dynamic Config, YAML File Provider)

### How forwardAuth is defined and attached

In Traefik v3 dynamic configuration (file provider), middlewares and routers are defined as sibling keys under the `http:` block. A router references middleware by name in its `middlewares` array. The middleware definition itself lives under `http.middlewares`.

**Complete pattern — router + forwardAuth middleware binding:**

```yaml
http:
  routers:
    rpm-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/rpm/`)"
      middlewares:
        - package-auth
      service: rpm-backend

  middlewares:
    package-auth:
      forwardAuth:
        address: "http://auth-service:8080/verify"
        authResponseHeaders:
          - "X-Auth-User"
          - "X-Auth-Scope"

  services:
    rpm-backend:
      loadBalancer:
        servers:
          - url: "http://rpm-service:8080"
```

**Key rules:**
- The `@` character is forbidden in middleware names (reserved for cross-provider references).
- The `middlewares` array on a router is a list of strings — the names of middlewares defined in the same provider namespace.
- Middleware only activates when the router's rule matches; it does not apply globally.
- The `service:` field is required on every router.

**Sources:**
- https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/
- https://doc.traefik.io/traefik/v3.3/routing/routers/

---

## Section 2: Selective Middleware Application — Auth on Some Routes, Bypass on Others

### The pattern

Traefik has no concept of "global middleware with exceptions". Selectivity is achieved by defining **separate routers** per route group, each with its own `middlewares` array (or none). The route-matching system handles dispatch. Routes that should bypass forwardAuth simply do not reference it in their router definition.

### Route priority

Routers on the same entrypoint are prioritised by **rule length (descending)** by default. Longer rules match first. You can override this with an explicit `priority:` integer on any router. The longer (more specific) rule wins ties.

### Example: /rpm/, /deb/, /oci/ require auth — /gpg/ is public — admin API on 127.0.0.1:8443 has no auth

```yaml
http:
  routers:
    # --- Routes requiring forwardAuth ---
    rpm-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/rpm/`)"
      middlewares:
        - package-auth
      service: package-backend

    deb-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/deb/`)"
      middlewares:
        - package-auth
      service: package-backend

    oci-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/oci/`)"
      middlewares:
        - package-auth
      service: package-backend

    # --- Public route — no forwardAuth ---
    gpg-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/gpg/`)"
      # No middlewares array — no auth applied
      service: gpg-backend

    # --- Admin API on loopback entrypoint — no forwardAuth ---
    admin-router:
      entryPoints:
        - admin           # entrypoint bound to 127.0.0.1:8443 in static config
      rule: "PathPrefix(`/`)"
      service: admin-backend

  middlewares:
    package-auth:
      forwardAuth:
        address: "http://auth-service:8080/verify"

  services:
    package-backend:
      loadBalancer:
        servers:
          - url: "http://package-svc:8080"
    gpg-backend:
      loadBalancer:
        servers:
          - url: "http://gpg-svc:8080"
    admin-backend:
      loadBalancer:
        servers:
          - url: "http://admin-svc:8443"
```

**Static config excerpt** (traefik.yml) for the admin loopback entrypoint:

```yaml
entryPoints:
  web:
    address: ":80"
  admin:
    address: "127.0.0.1:8443"
```

### Key points

- Each router independently declares its own middleware chain (or omits it entirely).
- Routes on a different entrypoint (`admin`) are completely isolated from routes on `web`; they never compete for the same requests.
- For path-based selectivity on the *same* entrypoint, longer/more-specific `PathPrefix` rules win automatically. If ambiguity is a concern, set explicit `priority:` values.
- The `gpg-router` example above works because `/gpg/` does not overlap with `/rpm/`, `/deb/`, or `/oci/` — no priority tuning needed.
- If you need to bypass auth for a sub-path of an authenticated prefix (e.g., `/rpm/public/` inside `/rpm/`), create a separate router for the sub-path with a higher explicit `priority:` and no forwardAuth, alongside the parent router that has forwardAuth.

**Sources:**
- https://doc.traefik.io/traefik/v3.4/reference/routing-configuration/http/router/rules-and-priority/
- https://doc.traefik.io/traefik/v3.3/routing/routers/
- https://community.traefik.io/t/bypass-authentik-forward-auth-for-local-addresses/24807

---

## Section 3: Middleware Chaining — Multiple Middlewares on a Single Router

### Two approaches

**Approach A — Inline list on the router (simplest)**

List all middleware names directly in the router's `middlewares` array. They execute in declaration order (first listed = first executed).

```yaml
http:
  routers:
    rpm-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/rpm/`)"
      middlewares:
        - package-auth      # runs first
        - strip-rpm-prefix  # runs second
      service: rpm-backend

  middlewares:
    package-auth:
      forwardAuth:
        address: "http://auth-service:8080/verify"

    strip-rpm-prefix:
      stripPrefix:
        prefixes:
          - "/rpm"
```

**Approach B — Named chain middleware (reusable group)**

Use the `chain` middleware type to define a reusable bundle. This is useful when the same combination is needed on many routers.

```yaml
http:
  routers:
    rpm-router:
      entryPoints:
        - web
      rule: "PathPrefix(`/rpm/`)"
      middlewares:
        - auth-and-strip
      service: rpm-backend

  middlewares:
    auth-and-strip:
      chain:
        middlewares:
          - package-auth
          - strip-rpm-prefix

    package-auth:
      forwardAuth:
        address: "http://auth-service:8080/verify"

    strip-rpm-prefix:
      stripPrefix:
        prefixes:
          - "/rpm"
```

### Execution order rule (verified from official docs)

> "Middlewares are applied in the same order as their declaration in router."

This applies to both the inline list and within a `chain` block. The first middleware in the list is the first to process the request.

**Sources:**
- https://doc.traefik.io/traefik/v3.3/routing/routers/ (explicit quote)
- https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/chain/

---

## Section 4: stripPrefix + forwardAuth — Which Runs First, What Does the Auth Service See?

### The critical finding (source-code verified)

The forwardAuth middleware reads `X-Forwarded-Uri` from `req.URL.RequestURI()` at the moment it executes. From `pkg/middlewares/auth/forward.go`:

```go
xfURI := req.Header.Get(xForwardedURI)
switch {
case xfURI != "" && trustForwardHeader:
    forwardReq.Header.Set(xForwardedURI, xfURI)
case req.URL.RequestURI() != "":
    forwardReq.Header.Set(xForwardedURI, req.URL.RequestURI())
```

`req.URL` is the **in-flight request object**, which is mutated by each middleware in sequence. This means:

- If **forwardAuth runs before stripPrefix**: `X-Forwarded-Uri` contains the **original path** (e.g., `/rpm/centos/packages/foo.rpm`). The auth service can inspect the full prefix.
- If **stripPrefix runs before forwardAuth**: `X-Forwarded-Uri` contains the **already-stripped path** (e.g., `/centos/packages/foo.rpm`). The auth service loses the prefix context.

Additionally, when stripPrefix runs it adds `X-Forwarded-Prefix` (e.g., `/rpm`) to the request. If forwardAuth runs after stripPrefix, the auth service receives `X-Forwarded-Prefix` but not the original prefix in `X-Forwarded-Uri`.

### Recommended ordering

**For package registries and similar use cases, declare forwardAuth BEFORE stripPrefix:**

```yaml
middlewares:
  - package-auth      # 1st — auth service sees /rpm/centos/packages/foo.rpm
  - strip-rpm-prefix  # 2nd — backend sees /centos/packages/foo.rpm
```

This ensures the auth service receives the full path context (including the `/rpm/` prefix) and can make scope-aware decisions (e.g., "is this key authorised for the rpm namespace?"). The backend still receives the stripped path because stripPrefix runs second, before the request is forwarded to the service.

### What the auth service receives (forwardAuth first, stripPrefix second)

| Header | Value | Notes |
|--------|-------|-------|
| `X-Forwarded-Uri` | `/rpm/centos/packages/foo.rpm` | Full original path |
| `X-Forwarded-Method` | `GET` | Original HTTP method |
| `X-Forwarded-Host` | `packages.example.com` | Original Host header |
| `X-Forwarded-Proto` | `https` | Protocol |
| `X-Forwarded-For` | `203.0.113.45` | Client IP |

### Security note

Traefik does **not** normalise URL-encoded sequences (e.g., `%2F`) before middleware execution. This is intentional and prevents path-traversal bypass attacks where an attacker might try to route around forwardAuth by encoding path separators. Verified against security research at xvnpw.github.io.

**Sources:**
- https://github.com/traefik/traefik/blob/master/pkg/middlewares/auth/forward.go (source code)
- https://doc.traefik.io/traefik/v3.3/routing/routers/ (middleware declaration order)
- https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/stripprefix/
- https://xvnpw.github.io/posts/path_traversal_in_authorization_context_in_traefik_and_haproxy/

---

## Section 5: File Provider Dynamic Config — Organisation and Hot-Reload

### Static config options (traefik.yml)

Two mutually exclusive options:

```yaml
# Option A: single file
providers:
  file:
    filename: "/etc/traefik/dynamic.yml"
    watch: true

# Option B: directory (preferred)
providers:
  file:
    directory: "/etc/traefik/dynamic/"
    watch: true
```

`watch` defaults to `true`. When enabled, Traefik uses [fsnotify](https://github.com/fsnotify/fsnotify) to listen for filesystem events and hot-reloads the dynamic config with **no request interruption or connection loss**.

A `providersThrottleDuration` (default: 2 seconds) prevents excessive reloads when many files change rapidly.

### Single file vs. directory

| Approach | Pros | Cons |
|----------|------|------|
| Single file | Simple, one place to look | Can become unwieldy; harder to manage with config management tools |
| Directory | Files can be split by concern (routers, middlewares, services); config management can write individual files | Slightly more mental overhead |

**Recommended directory layout for a service like packyard:**

```
/etc/traefik/dynamic/
├── middlewares.yml     # all middleware definitions
├── routers.yml         # all router definitions
└── services.yml        # all service definitions
```

All YAML files in the directory are merged. Keys must be globally unique across all files (duplicate router/middleware names across files will cause a conflict — last write wins, or Traefik may log a warning).

Alternatively, organise by service:

```
/etc/traefik/dynamic/
├── package-registry.yml    # routers + services + middlewares for /rpm/, /deb/, /oci/
├── gpg.yml                 # public /gpg/ route
└── admin.yml               # admin API on loopback
```

### Docker volume mount warning

If mounting config files via Docker bind mounts, watch out for symlink-based volume links (e.g., Kubernetes ConfigMaps, some Docker volume drivers). When the source file is replaced (not edited in place), the symlink can break and fsnotify stops receiving events.

**Best practice:** Mount the **parent directory**, not the file itself. Configure Traefik's `directory:` to point at that mounted directory. This ensures fsnotify watches the directory rather than a specific inode.

```yaml
# Good (directory mount)
providers:
  file:
    directory: "/etc/traefik/dynamic"

# Risky with Docker volumes
providers:
  file:
    filename: "/etc/traefik/dynamic/config.yml"
```

**Sources:**
- https://doc.traefik.io/traefik/reference/install-configuration/providers/others/file/
- https://doc.traefik.io/traefik/v3.3/providers/file/

---

## Section 6: Health Check and forwardAuth Interaction

### Two independent health check systems

There are two separate health check mechanisms involved; they do not interact with each other.

**1. Docker HEALTHCHECK (container health)**

Defined in `docker-compose.yml` or a Dockerfile. Docker itself monitors the container and marks it `healthy` / `unhealthy`. This label is visible to operators and orchestrators but Traefik does **not** use Docker container health status to gate routing decisions unless the Docker provider's `exposedByDefault: false` combined with labels is used to control visibility. Container health does not prevent Traefik from attempting to contact the auth service.

**2. Traefik load-balancer health check (backend server health)**

Configured on a Traefik `service` resource. It probes backend URLs on an interval and removes unhealthy servers from load-balancer rotation:

```yaml
http:
  services:
    auth-service:
      loadBalancer:
        healthCheck:
          path: "/health"
          interval: "10s"
          timeout: "3s"
        servers:
          - url: "http://auth-svc:8080"
```

This only applies to servers listed under a Traefik `service.loadBalancer.servers`. It does **not** apply to the `address:` field of a `forwardAuth` middleware.

### How forwardAuth resolves its target

The `address:` field in `forwardAuth` is a plain HTTP/HTTPS URL that Traefik calls directly at request time using its internal HTTP client. This connection is **not** managed by a Traefik load-balancer service and therefore **does not benefit from Traefik's LB health-check probing or server rotation**.

Traefik makes a raw HTTP request to that address on every authenticated request. There is no prior health gate.

### What happens when the auth service is unreachable

When Traefik's forwardAuth middleware cannot reach the `address:` (connection refused, timeout, DNS failure):

- Traefik logs an error from the forward middleware (e.g., `"Error calling http://auth-service:8080/verify. Cause: dial tcp: connect: connection refused"`)
- Traefik returns an **HTTP 500 Internal Server Error** to the client
- Access is **denied** (fail-closed behaviour) — Traefik does NOT bypass authentication when the auth service is unavailable
- The response is the error from Traefik's middleware layer, not a 502 from the backend

This is the correct security posture: an unavailable auth service results in a hard failure, not a pass-through.

### If you want the auth service itself to benefit from load-balancer health checks

You can point `forwardAuth.address` at a Traefik-managed service URL (i.e., route auth requests through Traefik's own load balancer for the auth service), but this adds complexity. The simpler and more common pattern is to run the auth service with a Docker health check for operational monitoring and rely on container restart policies to recover it quickly. Traefik's fail-closed behaviour ensures no requests pass unauthenticated during any auth service downtime.

**Sources:**
- https://doc.traefik.io/traefik/reference/routing-configuration/http/load-balancing/service/
- https://doc.traefik.io/traefik/reference/install-configuration/observability/healthcheck/
- https://community.traefik.io/t/error-calling-http-oauth2-proxy-4180/20025

---

## Complete Working Example (All Patterns Combined)

This example integrates all six findings into a single coherent dynamic config file.

### `/etc/traefik/traefik.yml` (static config excerpt)

```yaml
entryPoints:
  web:
    address: ":80"
  websecure:
    address: ":443"
  admin:
    address: "127.0.0.1:8443"

providers:
  file:
    directory: "/etc/traefik/dynamic"
    watch: true
```

### `/etc/traefik/dynamic/package-registry.yml`

```yaml
http:
  routers:
    # --- Authenticated package routes ---
    rpm-router:
      entryPoints:
        - websecure
      rule: "PathPrefix(`/rpm/`)"
      middlewares:
        - package-auth        # 1st: auth sees /rpm/... path
        - strip-rpm-prefix    # 2nd: backend sees /... path
      service: package-backend
      tls: {}

    deb-router:
      entryPoints:
        - websecure
      rule: "PathPrefix(`/deb/`)"
      middlewares:
        - package-auth
        - strip-deb-prefix
      service: package-backend
      tls: {}

    oci-router:
      entryPoints:
        - websecure
      rule: "PathPrefix(`/oci/`)"
      middlewares:
        - package-auth
        - strip-oci-prefix
      service: oci-backend
      tls: {}

    # --- Public route (no auth) ---
    gpg-router:
      entryPoints:
        - websecure
      rule: "PathPrefix(`/gpg/`)"
      # No middlewares — intentionally public
      service: gpg-backend
      tls: {}

    # --- Admin API (loopback only, no auth) ---
    admin-router:
      entryPoints:
        - admin
      rule: "PathPrefix(`/`)"
      service: admin-backend

  middlewares:
    package-auth:
      forwardAuth:
        address: "http://auth-service:8080/verify"
        authResponseHeaders:
          - "X-Auth-User"
          - "X-Auth-Scope"
        authRequestHeaders:
          - "Authorization"
        maxBodySize: 0        # Do not forward request body to auth service

    strip-rpm-prefix:
      stripPrefix:
        prefixes:
          - "/rpm"

    strip-deb-prefix:
      stripPrefix:
        prefixes:
          - "/deb"

    strip-oci-prefix:
      stripPrefix:
        prefixes:
          - "/oci"

  services:
    package-backend:
      loadBalancer:
        healthCheck:
          path: "/health"
          interval: "10s"
          timeout: "3s"
        servers:
          - url: "http://package-svc:8080"

    oci-backend:
      loadBalancer:
        servers:
          - url: "http://oci-registry:5000"

    gpg-backend:
      loadBalancer:
        servers:
          - url: "http://gpg-svc:8080"

    admin-backend:
      loadBalancer:
        servers:
          - url: "http://admin-svc:8443"
```

---

## Key Findings Summary

| Question | Answer |
|----------|--------|
| Router → middleware attachment | `middlewares:` array on router references named middleware by string key; defined under `http.middlewares` |
| Selective application | Create separate routers per route; omit forwardAuth from routers that should not use it; use entrypoint isolation for fully separate APIs |
| Middleware chaining | List middleware names in order in `middlewares:` array, or use a `chain:` middleware; order is declaration order |
| forwardAuth + stripPrefix order | Declare forwardAuth FIRST; `X-Forwarded-Uri` reflects whatever `req.URL` is at execution time — if stripPrefix runs first, auth sees stripped path |
| File provider | Use `directory:` (preferred over `filename:`); `watch: true` by default; mount parent dir in Docker to avoid fsnotify symlink issues |
| Healthcheck + forwardAuth | Traefik LB healthchecks do NOT apply to `forwardAuth.address`; auth service is contacted directly at request time; unavailability → HTTP 500, fail-closed |

---

## Confidence Levels

| Finding | Confidence | Basis |
|---------|-----------|-------|
| Router middlewares array and declaration order | High | Official docs, explicit quote |
| Selective application via separate routers | High | Official docs + community pattern |
| forwardAuth config options and headers table | High | Official v3 docs |
| X-Forwarded-Uri captures current req.URL | High | Source code (forward.go) |
| File provider directory vs. filename | High | Official docs |
| fsnotify + Docker volume mount caveat | High | Official docs |
| forwardAuth fail-closed on service unavailable | Medium-High | Community forum logs + documented behaviour; no explicit "returns 500" statement in official docs, but consistent with observed behaviour and documented flow |
| Traefik LB healthcheck does not gate forwardAuth.address | High | Architecture-level: forwardAuth uses independent HTTP client, not a LB service |

---

## Sources

- [Traefik ForwardAuth Documentation (current)](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/forwardauth/)
- [Traefik ForwardAuth Documentation v3.4](https://doc.traefik.io/traefik/v3.4/middlewares/http/forwardauth/)
- [Traefik Chain Middleware Documentation](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/chain/)
- [Traefik StripPrefix Documentation](https://doc.traefik.io/traefik/reference/routing-configuration/http/middlewares/stripprefix/)
- [Traefik HTTP Router Documentation v3.3](https://doc.traefik.io/traefik/v3.3/routing/routers/)
- [Traefik Router Rules and Priority v3.4](https://doc.traefik.io/traefik/v3.4/reference/routing-configuration/http/router/rules-and-priority/)
- [Traefik File Provider Documentation](https://doc.traefik.io/traefik/reference/install-configuration/providers/others/file/)
- [Traefik HTTP Services / Load Balancer Health Check](https://doc.traefik.io/traefik/reference/routing-configuration/http/load-balancing/service/)
- [Traefik Health Check CLI Documentation](https://doc.traefik.io/traefik/reference/install-configuration/observability/healthcheck/)
- [traefik/traefik — pkg/middlewares/auth/forward.go (GitHub)](https://github.com/traefik/traefik/blob/master/pkg/middlewares/auth/forward.go)
- [Path traversal in authorization context in Traefik and HAProxy — xvnpw](https://xvnpw.github.io/posts/path_traversal_in_authorization_context_in_traefik_and_haproxy/)
- [Traefik Community: Middleware order struggle in v3](https://community.traefik.io/t/anyone-else-struggle-with-middleware-order-in-traefik-v3/27173)
- [Traefik Community: Bypass forwardAuth for local addresses](https://community.traefik.io/t/bypass-authentik-forward-auth-for-local-addresses/24807)
- [Traefik Community: Error calling forwardAuth / oauth2-proxy](https://community.traefik.io/t/error-calling-http-oauth2-proxy-4180/20025)
