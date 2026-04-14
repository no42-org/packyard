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

Valid values for `component`: `core`, `minion`, `sentinel`.

A key scoped to `core` grants access to `/rpm/core/`, `/deb/core/`, and `/oci/` paths for core images. Cross-component access is denied — a `core` key cannot access `/rpm/minion/`.

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
  "created_at": "2025-01-01T00:00:00Z"
}
```

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
