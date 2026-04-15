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

## Components

Components are provisioned via the admin API and stored in the SQLite database. The auth service loads the component list at startup.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/components` | Provision a new component |
| `GET` | `/api/v1/components` | List all components |
| `GET` | `/api/v1/components/{name}` | Get a single component |
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

Returns `201 Created` with the component record. The RPM directory tree is initialised on disk automatically. DEB (aptly) and OCI (Zot) provision lazily on first publish.

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

Returns `200 OK` with `{"keys_revoked": 4}`. RPM directory content is **not** removed — the operator is responsible for archiving packages before deleting the directory.

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

`component_visibility` reflects the current visibility of the key's component as stored in the database. It is computed at response time — not stored with the key. If the component has been removed since the key was created, this field defaults to `"private"`.

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
