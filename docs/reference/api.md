# Admin API

The admin API is available at `/api/v1/` via Traefik's loopback admin entrypoint (`:8088`). It is not reachable from the internet — access via SSH tunnel:

```bash
ssh -L 8088:127.0.0.1:8088 deploy@pkg.example.org -N &
```

## Endpoints

### Keys

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/keys` | Create a subscription key |
| `GET` | `/api/v1/keys` | List all keys |
| `GET` | `/api/v1/keys?component=core` | List keys filtered by component |
| `GET` | `/api/v1/keys/{id}` | Inspect a key |
| `DELETE` | `/api/v1/keys/{id}` | Revoke a key |

The health endpoint `GET /health` returns 200 when the service is up.

**Response codes for `GET /api/v1/keys`:**

| Code | Condition |
|------|-----------|
| `200 OK` | Returns a JSON array of key objects (empty array when no keys exist or none match the filter) |
| `400 Bad Request` | `?component=` names a component not in the startup-loaded map (`INVALID_COMPONENT`) |
| `500 Internal Server Error` | Unexpected store error |

**Response codes for `GET /api/v1/keys/{id}`:**

| Code | Condition |
|------|-----------|
| `200 OK` | Key found; returns the key object |
| `404 Not Found` | No key with the given ID exists (`KEY_NOT_FOUND`) |
| `500 Internal Server Error` | Unexpected store error |

> **Note:** The `?component=` filter and the `component_visibility` field in key responses are validated against a component map loaded at auth service startup. Newly provisioned components return `400 INVALID_COMPONENT` until the auth service is restarted; `component_visibility` likewise reflects the startup snapshot, not the live database value. Forward-auth access control uses the live database and does not require a restart — only the `?component=` filter parameter and `component_visibility` field are affected by the startup caveat. See also the [Components](#components) section preamble.

## Components

Components are provisioned via the admin API and stored in the SQLite database. Forward-auth resolves component visibility via a live database lookup on every request; the `GET /api/v1/keys?component=` filter and `component_visibility` field in key responses use a snapshot loaded at startup (restart required to pick up new components in those paths).

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/components` | Provision a new component |
| `GET` | `/api/v1/components` | List all components |
| `GET` | `/api/v1/components/{name}` | Get a single component |
| `PATCH` | `/api/v1/components/{name}` | Update component visibility |
| `DELETE` | `/api/v1/components/{name}` | Deprovision a component (safe-lock) |

A key scoped to `core` grants access only to `/rpm/core/`, `/deb/core/`, and `lts-core` OCI paths. Cross-component access is denied — a `core` key cannot access `/rpm/minion/`. The component name in the key must match the path segment exactly.

### Component visibility

Each component has a `visibility` setting:

| Value | Behaviour |
|-------|-----------|
| `private` (default) | Credentials are required and scope-checked |
| `public` | Requests are allowed without credentials — credentials, if present, are ignored |

Public components are useful for freely distributable software. The auth service returns `200` for any request to a public component path, regardless of whether credentials are present or valid.

### Provisioning a component

```bash
curl -s -X POST http://127.0.0.1:8088/api/v1/components \
  -H 'Content-Type: application/json' \
  -d '{
    "name": "minion",
    "visibility": "private",
    "rpm_series": ["2025"],
    "rpm_os_families": ["el9"],
    "rpm_architectures": ["x86_64", "aarch64"]
  }' | jq .
```

**Validation rules:**

- `name` is required and must not contain `/`, `\`, or `..`
- `visibility` must be `"public"` or `"private"` (default: `"private"` when omitted)
- `rpm_series`, `rpm_os_families`, `rpm_architectures` values must not contain `/`, `\`, or `..`

**Response codes:**

| Code | Condition |
|------|-----------|
| `201 Created` | Component created; RPM directory tree initialised on disk |
| `400 Bad Request` | Validation failure (`INVALID_REQUEST` or `INVALID_VISIBILITY`) |
| `409 Conflict` | A component with this name already exists (`COMPONENT_EXISTS`) |
| `500 Internal Server Error` | RPM directory initialisation failed (`RPM_INIT_FAILED`) |

Example `201 Created` response:

```json
{
  "name": "minion",
  "visibility": "private",
  "rpm_series": ["2025"],
  "rpm_os_families": ["el9"],
  "rpm_architectures": ["x86_64", "aarch64"],
  "created_at": "2025-01-01T00:00:00Z"
}
```

Note: when `visibility` is omitted from the request, the response body shows `"visibility": "private"` (the default).

DEB (aptly) and OCI (Zot) provision lazily on first publish.

### Listing and inspecting components

```bash
# List all components
curl -s http://127.0.0.1:8088/api/v1/components | jq .

# Inspect a single component
curl -s http://127.0.0.1:8088/api/v1/components/minion | jq .
```

**Response codes for GET `/api/v1/components`:** `200 OK` (returns empty array when none exist), `500 Internal Server Error` (store error).

**Response codes for GET `/api/v1/components/{name}`:** `200 OK` (found), `404 Not Found` (`COMPONENT_NOT_FOUND`).

### Deprovisioning a component

Without `?confirm`, the API returns `409` with an impact preview:

```bash
curl -s -X DELETE http://127.0.0.1:8088/api/v1/components/minion | jq .
```

```json
{
  "code": "CONFIRM_REQUIRED",
  "message": "Deleting \"minion\" will remove the component and revoke all associated keys. Pass ?confirm=minion to proceed.",
  "impact": { "keys_revoked": 4, "rpm_series_removed": ["2025"] }
}
```

With the correct `?confirm={name}`:

```bash
curl -s -X DELETE http://127.0.0.1:8088/api/v1/components/minion?confirm=minion | jq .
```

**Response codes — without `?confirm` (impact preview):**

| Code | Condition |
|------|-----------|
| `409 Conflict` | `?confirm` absent or value does not match the component name exactly (`CONFIRM_REQUIRED`) |
| `404 Not Found` | Component does not exist (`COMPONENT_NOT_FOUND`) |

**Response codes — with `?confirm={name}` (confirmed delete):**

| Code | Condition |
|------|-----------|
| `200 OK` | Component deleted; keys revoked atomically — returns `{"keys_revoked": N}` |
| `404 Not Found` | Component does not exist (`COMPONENT_NOT_FOUND`) |
| `500 Internal Server Error` | Unexpected store error during atomic delete |

RPM directory content is **not** removed — the operator is responsible for archiving packages before deleting the directory.

### Updating a component

`PATCH /api/v1/components/{name}` updates mutable component fields. Currently only `visibility` may be changed. The `visibility` field is **required** — omitting it or sending an empty body returns `400 INVALID_VISIBILITY`. The update is persisted immediately and takes effect on the next subscriber request — no service restart is required.

```bash
curl -s -X PATCH http://127.0.0.1:8088/api/v1/components/minion \
  -H 'Content-Type: application/json' \
  -d '{"visibility": "public"}' | jq .
```

Returns the updated component object on success.

**Response codes:**

| Code | Condition |
|------|-----------|
| `200 OK` | Visibility updated; returns updated component record |
| `400 Bad Request` | `visibility` is not `"public"` or `"private"` (`INVALID_VISIBILITY`); or body is not valid JSON (`INVALID_REQUEST`) |
| `404 Not Found` | Component does not exist (`COMPONENT_NOT_FOUND`) |
| `500 Internal Server Error` | Unexpected store error |

### Component API error codes

| Code | Returned by | Description |
|------|-------------|-------------|
| `INVALID_REQUEST` | POST, PATCH | `name` is missing, empty, or contains path-unsafe characters; or an array field contains a path-unsafe value; or body is not valid JSON |
| `INVALID_VISIBILITY` | POST, PATCH | `visibility` is not `"public"` or `"private"` |
| `COMPONENT_EXISTS` | POST | A component with the given name already exists |
| `RPM_INIT_FAILED` | POST | RPM directory tree creation failed; the component record was rolled back |
| `COMPONENT_NOT_FOUND` | GET `/{name}`, PATCH, DELETE | No component with the given name exists |
| `CONFIRM_REQUIRED` | DELETE (no confirm) | `?confirm={name}` was absent or did not match; response includes impact preview |

## Examples

The following examples assume the SSH tunnel is active (see above).

**Create a key:**

```bash
curl -s -X POST http://127.0.0.1:8088/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "Acme Corp"}' | jq .
```

```json
{
  "id": "abc123...",
  "component": "core",
  "label": "Acme Corp",
  "active": true,
  "created_at": "2025-01-01T00:00:00Z",
  "component_visibility": "private"
}
```

`component_visibility` reflects the component visibility from the startup-loaded map, not the live database value. It is computed at response time — not stored with the key. If the component was provisioned after the service started, or its visibility was changed via `PATCH /api/v1/components/{name}` since the last restart, this field may show a stale value. If the component has been removed since the key was created, it defaults to `"private"`.

**List keys:**

```bash
curl -s http://127.0.0.1:8088/api/v1/keys | jq .
```

**Revoke a key:**

```bash
curl -s -X DELETE http://127.0.0.1:8088/api/v1/keys/abc123...
```

## Error responses

The API returns structured errors for all failure cases:

```json
{
  "code": "KEY_SCOPE_MISMATCH",
  "message": "Key 'abc123' is scoped to 'core' but requested path is '/minion/'",
  "component_requested": "minion",
  "key_scope": "core"
}
```

Package serving endpoints (`/rpm/`, `/deb/`, `/oci/`) return bare `HTTP 401` on auth failure — `dnf`/`apt`/`docker` do not parse response bodies on auth failure.
