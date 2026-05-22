# Admin API

The admin API is reached at `https://admin.pkg.example.org/api/v1/` (or
whatever value you set for `ADMIN_DOMAIN` in `.env`). Every endpoint other
than `/api/v1/auth/login/{provider}` and `/api/v1/auth/callback/{provider}`
requires an authenticated operator session.

Operators sign in via OAuth (GitHub or Microsoft) and receive an HttpOnly,
SameSite=Strict session cookie. Sessions are server-side and revocable; there
is no API token or break-glass path. See [Operator
onboarding](../ops/operator-onboarding.md) for the human workflow.

## Authentication and session model

### OAuth login

| Method | Path                                  | Description                                                    |
|--------|---------------------------------------|----------------------------------------------------------------|
| `GET`  | `/api/v1/auth/login/github`           | Initiate the GitHub OAuth flow; sets a transient handle cookie |
| `GET`  | `/api/v1/auth/login/microsoft`        | Initiate the Microsoft Entra OAuth flow                        |
| `GET`  | `/api/v1/auth/callback/{provider}`    | OAuth callback — the IdP redirects the browser here            |
| `POST` | `/api/v1/auth/logout`                 | Destroy the current session and clear the cookie               |
| `GET`  | `/api/v1/auth/whoami`                 | Return the current operator's `{id, email, role, status, …}`   |

Both login endpoints use the **Authorization Code + PKCE (S256)** flow. The
`state` parameter and PKCE `code_verifier` are stored server-side and keyed
by an opaque handle held in a short-lived cookie so the browser doesn't see
the verifier. The callback validates the state, exchanges the code, resolves
the operator by canonical email against the allowlist (`operators` table),
and creates a session.

### Session cookie

Set on successful callback as `Set-Cookie: packyard_session=…`:

| Attribute        | Value           | Why                                                           |
|------------------|-----------------|---------------------------------------------------------------|
| `HttpOnly`       | yes             | JavaScript cannot read the cookie — bounds XSS impact         |
| `Secure`         | yes             | Only sent over TLS                                            |
| `SameSite`       | `Strict`        | Cross-site requests do not carry the cookie (CSRF defence)    |
| `Path`           | `/`             | Both `/admin/*` (SPA) and `/api/v1/*` (API) need the cookie   |
| `Max-Age`        | 24 h (absolute) | Absolute session lifetime — re-login required after that      |

In addition to `Max-Age`, the server enforces an **8-hour idle timeout**.
Each authenticated request bumps `last_seen_at`; if that field is older than
8 hours when the next request arrives, the session is rejected with
`SESSION_EXPIRED`.

### CSRF defence

Every mutating request (`POST`/`PUT`/`PATCH`/`DELETE`) must carry an
`Origin` header (or a `Referer` header, when `Origin` is absent) whose
scheme + host + port match `PACKYARD_ADMIN_HOST`. Failure returns `403
CSRF_DENIED`. Same-origin requests from the SPA always pass; this layer
exists in addition to `SameSite=Strict` so a misconfigured proxy or custom
client still has to carry a matching origin header.

### Rate limit

The OAuth surface (`/api/v1/auth/login/{provider}` and
`/api/v1/auth/callback/{provider}`) is rate-limited per source IP via a
token bucket — **capacity 10, refill 1 request every 6 seconds**. Exceeding
the bucket returns `429 RATE_LIMITED` and writes an `auth.rate_limited`
audit row (coalesced per source IP within a 60-second window so a sustained
attack does not flood the audit log). No other admin endpoints are
rate-limited — they sit behind the session check, which already bounds
unauthenticated abuse.

### Pagination

All list endpoints (`/accounts`, `/accounts/{id}/keys`, `/keys`, `/operators`,
`/audit`) accept:

| Param     | Default | Max | Behaviour                                               |
|-----------|---------|-----|---------------------------------------------------------|
| `offset`  | `0`     | —   | Zero-based row offset                                   |
| `limit`   | `50`    | 500 | Number of rows; `limit > 500` returns `400 LIMIT_TOO_LARGE` |

Responses include an RFC 5988 `Link` header with `rel="prev"` and
`rel="next"` URLs preserving any filter parameters. The SPA uses these
directly; CLI users can parse them with `curl -i` and `awk`/`jq`. The
response body itself is a plain JSON array — no envelope object, no
`total_count`.

## Accounts

Accounts are subscriber identities that own subscription keys. Every key
must belong to exactly one account; deleting an account revokes its keys.

| Method   | Path                                  | Description                                        |
|----------|---------------------------------------|----------------------------------------------------|
| `POST`   | `/api/v1/accounts`                    | Create a new subscriber account                    |
| `GET`    | `/api/v1/accounts`                    | List accounts (filter via `?status=`)              |
| `GET`    | `/api/v1/accounts/{id}`               | Get a single account                               |
| `PATCH`  | `/api/v1/accounts/{id}`               | Update email / org / status                        |
| `DELETE` | `/api/v1/accounts/{id}?confirm={id}`  | Delete the account and revoke every active key     |
| `GET`    | `/api/v1/accounts/{id}/keys`          | List keys owned by the account                     |
| `POST`   | `/api/v1/accounts/{id}/keys`          | Issue a new key for the account                    |

The `status` field is one of `active`, `suspended`, `deleted`. Deleted
accounts are not returned by list endpoints; only an exact-id lookup
surfaces them.

### Create an account

```bash
curl -s -X POST https://admin.pkg.example.org/api/v1/accounts \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"email":"ops@acme.test","org_name":"Acme Corp"}' | jq .
```

```json
{
  "id": "01J4Y…",
  "email": "ops@acme.test",
  "org_name": "Acme Corp",
  "status": "active",
  "created_at": "2026-05-22T12:00:00Z",
  "created_by_operator_id": "9f…"
}
```

### Issue a key for an account

```bash
curl -s -X POST "https://admin.pkg.example.org/api/v1/accounts/${ACCOUNT_ID}/keys" \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"component":"core","label":"prod"}' | jq .
```

```json
{
  "id": "abc123…",
  "component": "core",
  "active": true,
  "label": "prod",
  "created_at": "2026-05-22T12:00:00Z",
  "expires_at": null,
  "usage_count": 0,
  "account_id": "01J4Y…",
  "component_visibility": "private"
}
```

The `id` field **is** the subscription key — there is no separate secret.
It is the HTTP Basic password the subscriber pastes into their package
manager configuration verbatim. Capture it at creation; subsequent list /
get responses include the same `id` alongside metadata, so it is never
secret-by-obscurity, but rotation requires revoking the key and issuing a
fresh one.

### Delete an account (with safe-lock)

Bare `DELETE` returns `409 CONFIRM_REQUIRED` with an impact preview:

```bash
curl -s -X DELETE "https://admin.pkg.example.org/api/v1/accounts/${ACCOUNT_ID}" \
  --cookie "$COOKIE" | jq .
```

```json
{
  "code": "CONFIRM_REQUIRED",
  "message": "Confirmation required",
  "impact": { "active_keys": 3 }
}
```

Re-issue with `?confirm={id}` to proceed:

```bash
curl -s -X DELETE "https://admin.pkg.example.org/api/v1/accounts/${ACCOUNT_ID}?confirm=${ACCOUNT_ID}" \
  --cookie "$COOKIE" | jq .
# { "revoked_keys": 3 }
```

## Subscription keys

Keys are scoped to a single component and a single account. The bulk
endpoints below remain useful for queries; routine issuance goes through
`/accounts/{id}/keys` so the account binding is explicit.

| Method   | Path                              | Description                                           |
|----------|-----------------------------------|-------------------------------------------------------|
| `POST`   | `/api/v1/keys`                    | Create a key (`account_id` required in body)          |
| `GET`    | `/api/v1/keys`                    | List keys (filter via `?component=` and/or `?account=`) |
| `GET`    | `/api/v1/keys/{id}`               | Inspect a single key                                  |
| `DELETE` | `/api/v1/keys/{id}`               | Revoke a key                                          |

## Components

Components are provisioned via the admin API and stored in the SQLite
database. Forward-auth resolves component visibility via a live database
lookup on every request; the `GET /api/v1/keys?component=` filter and
`component_visibility` field in key responses use a snapshot loaded at
startup (restart required to pick up new components in those paths).

| Method   | Path                              | Description                              |
|----------|-----------------------------------|------------------------------------------|
| `POST`   | `/api/v1/components`              | Provision a new component                |
| `GET`    | `/api/v1/components`              | List all components                      |
| `GET`    | `/api/v1/components/{name}`       | Get a single component                   |
| `PATCH`  | `/api/v1/components/{name}`       | Update component visibility              |
| `DELETE` | `/api/v1/components/{name}`       | Deprovision a component (safe-lock)      |

A key scoped to `core` grants access only to `/rpm/core/`, `/deb/core/`,
and `lts-core` OCI paths. Cross-component access is denied — a `core` key
cannot access `/rpm/minion/`. The component name in the key must match the
path segment exactly.

### Component visibility

| Value     | Behaviour                                                              |
|-----------|------------------------------------------------------------------------|
| `private` | Credentials are required and scope-checked (default)                   |
| `public`  | Requests are allowed without credentials — credentials, if present, are ignored |

Public components are useful for freely distributable software. The auth
service returns `200` for any request to a public component path,
regardless of whether credentials are present or valid.

### Provisioning a component

```bash
curl -s -X POST https://admin.pkg.example.org/api/v1/components \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{
    "name": "minion",
    "visibility": "private",
    "rpm_series": ["2025"],
    "rpm_os_families": ["el9"],
    "rpm_architectures": ["x86_64", "aarch64"]
  }' | jq .
```

Validation rules: `name` must not contain `/`, `\`, or `..`; `visibility`
must be `public` or `private`; series/families/architectures values are
also rejected if they contain path-unsafe characters.

### Deprovisioning a component

Same two-step safe-lock as accounts:

```bash
curl -s -X DELETE https://admin.pkg.example.org/api/v1/components/minion --cookie "$COOKIE" | jq .
# 409 CONFIRM_REQUIRED with impact preview

curl -s -X DELETE 'https://admin.pkg.example.org/api/v1/components/minion?confirm=minion' --cookie "$COOKIE" | jq .
# 200 OK with { "keys_revoked": N }
```

RPM directory content is **not** removed automatically — archive packages
before deleting the directory.

### Updating a component

`PATCH /api/v1/components/{name}` updates mutable fields. Currently only
`visibility` may be changed. The update is persisted immediately and takes
effect on the next subscriber request — no restart required.

```bash
curl -s -X PATCH https://admin.pkg.example.org/api/v1/components/minion \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"visibility":"public"}' | jq .
```

## Operators

| Method  | Path                            | Description                                                    |
|---------|---------------------------------|----------------------------------------------------------------|
| `GET`   | `/api/v1/operators`             | List operators (paginated)                                     |
| `POST`  | `/api/v1/operators`             | Allowlist a new operator (admin-only)                          |
| `PATCH` | `/api/v1/operators/{id}`        | Change `role` and/or `status` (admin-only, self-lockout-guarded) |

`POST /api/v1/operators` accepts `{"email": "...", "role": "admin"|"readonly"}`.
Role defaults to `admin` when omitted. Email is canonicalised (lowercase +
trim).

`PATCH /api/v1/operators/{id}` accepts `{"role": "...", "status": "..."}`.
At least one of the two must be set. The PATCH wraps the count guard +
role + status update in a single serializable transaction; a mutation that
would leave zero active admins returns `403 OPERATOR_SELF_LOCKOUT`. A
demote (`admin → readonly`) or disable (`active → disabled`) deletes the
target's active sessions so the change takes effect on the next request.

There is no `DELETE` for operators by design — disabling preserves the
audit-log attribution. See the [operator onboarding
guide](../ops/operator-onboarding.md) for the full flow.

## Audit log

| Method | Path             | Description                                       |
|--------|------------------|---------------------------------------------------|
| `GET`  | `/api/v1/audit`  | List audit rows (readable by admin and readonly)  |

Filters (all optional, AND-combined):

| Param          | Description                                                                |
|----------------|----------------------------------------------------------------------------|
| `operator`     | Restrict to actions by this operator id                                    |
| `action`       | Exact action name (e.g. `account.create`, `operator.role_change`)          |
| `target_type`  | Restrict to a target type (e.g. `account`, `operator`, `key`, `session`)   |
| `target_id`    | Restrict to a target id (combine with `target_type` for unambiguous match) |
| `since`        | RFC 3339 timestamp — inclusive lower bound                                 |
| `until`        | RFC 3339 timestamp — exclusive upper bound                                 |
| `offset`,`limit` | Pagination per [§ Pagination](#pagination)                               |

Audit rows are append-only — no `PUT`/`PATCH`/`DELETE` exists for them.

Action vocabulary (non-exhaustive — see the operator-authn spec for the
authoritative list): `login.success`, `login.failure`, `logout`,
`auth.role_denied`, `auth.rate_limited`, `account.create`, `account.update`,
`account.suspend`, `account.reactivate`, `account.delete`, `key.issue`,
`key.revoke`, `operator.add`, `operator.disable`, `operator.enable`,
`operator.role_change`.

## Error responses

Every JSON error has the same envelope:

```json
{
  "code": "OPERATOR_SELF_LOCKOUT",
  "message": "refusing to leave zero active admins; ask another admin to make this change"
}
```

Additional context fields may be present (e.g. `impact` on
`CONFIRM_REQUIRED`, `component_requested` / `key_scope` on forward-auth
rejection).

### Authentication / authorisation codes

| Code                       | HTTP | When                                                       |
|----------------------------|------|------------------------------------------------------------|
| `UNAUTHORIZED`             | 401  | No valid operator session                                  |
| `SESSION_EXPIRED`          | 401  | Session exceeded idle or absolute timeout                  |
| `ROLE_DENIED`              | 403  | Role does not permit the requested operation               |
| `OPERATOR_NOT_ALLOWED`     | 403  | Email is not in the operators allowlist                    |
| `OPERATOR_DISABLED`        | 403  | Operator exists but `status = 'disabled'`                  |
| `ORG_MEMBERSHIP_REQUIRED`  | 403  | OAuth user is not a member of the configured GitHub org    |
| `EMAIL_NOT_VERIFIED`       | 403  | OAuth provider returned an unverified email                |
| `INVALID_OAUTH_STATE`      | 400  | OAuth `state` missing, mismatched, or replayed             |
| `RATE_LIMITED`             | 429  | Token-bucket capacity exceeded on `/api/v1/auth/*`         |
| `CSRF_DENIED`              | 403  | Origin / Referer does not match the configured admin host  |
| `UNKNOWN_PROVIDER`         | 404  | OAuth path named an unconfigured provider                  |
| `OAUTH_EXCHANGE_FAILED`    | 401  | OAuth code-for-token exchange failed                       |
| `LOGIN_INIT_FAILED`        | 500  | Internal entropy or store failure starting an OAuth flow   |
| `OPERATOR_LOOKUP_FAILED`   | 500  | Backend store error resolving an operator                  |
| `SESSION_CREATE_FAILED`    | 500  | Backend store error creating a session                     |
| `SESSION_LOOKUP_FAILED`    | 500  | Transient backend error during session middleware lookup   |

### Resource / validation codes

| Code                           | HTTP | When                                                       |
|--------------------------------|------|------------------------------------------------------------|
| `ACCOUNT_NOT_FOUND`            | 404  | No account with the requested id                           |
| `ACCOUNT_EMAIL_EXISTS`         | 409  | Account email collides with an existing record             |
| `MISSING_ACCOUNT_ID`           | 400  | `POST /api/v1/keys` called without `account_id`            |
| `INVALID_STATUS_TRANSITION`    | 400  | Attempted PATCH to/from `deleted`                          |
| `CONFIRM_REQUIRED`             | 409  | Destructive endpoint called without matching `?confirm=`   |
| `LIMIT_TOO_LARGE`              | 400  | List endpoint `?limit` exceeds 500                         |
| `OPERATOR_EMAIL_EXISTS`        | 409  | Operator email already in allowlist                        |
| `OPERATOR_NOT_FOUND`           | 404  | No operator with the requested id                          |
| `OPERATOR_SELF_LOCKOUT`        | 403  | PATCH would leave zero active admins                       |
| `COMPONENT_EXISTS`             | 409  | A component with the given name already exists             |
| `COMPONENT_NOT_FOUND`          | 404  | No component with the given name exists                    |
| `INVALID_VISIBILITY`           | 400  | `visibility` is not `public` or `private`                  |
| `INVALID_REQUEST`              | 400  | Body is not JSON, or a field is missing / malformed        |

Package serving endpoints (`/rpm/`, `/deb/`, `/oci/`) return a bare `HTTP
401` on auth failure — `dnf`/`apt`/`docker` do not parse response bodies
on auth failure.
