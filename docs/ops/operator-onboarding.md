# Operator onboarding

This guide walks through bringing a new operator from "human with an
identity provider" to "able to administer the Packyard service."

The model is **email-joined OAuth**:

1. An existing admin allowlists the new operator's email address.
2. The operator signs in via GitHub or Microsoft.
3. The auth service looks up the operator row by canonical email and binds
   the OAuth identity to it on first login.

There is no API token, no shared password, and no break-glass path. Every
admin action is attributed to a specific operator and audited.

---

## 1. Register the IdP apps (one-time, per deployment)

The auth service supports **GitHub** and **Microsoft Entra (Azure AD)**
providers. You can configure either or both. Each provider requires the
**complete** environment-variable set listed below — a partial configuration
fails the auth service at startup rather than registering a half-broken
provider.

### 1.1 GitHub OAuth app

1. In the **GitHub organisation** that gates access (the
   `PACKYARD_GITHUB_ORG`), navigate to **Settings → Developer settings →
   OAuth Apps → New OAuth App**.
2. Set:
   - **Application name**: `Packyard Admin (production)` or similar.
   - **Homepage URL**: `https://admin.pkg.example.org`.
   - **Authorization callback URL**:
     `https://admin.pkg.example.org/api/v1/auth/callback/github`.
3. Click **Register application**. Note the **Client ID**.
4. Click **Generate a new client secret** and copy the value — it is shown
   only once.
5. **Org membership scope** — the OAuth app must have access to query the
   org's member list. By default this requires the `read:org` scope, which
   GitHub may surface as needing org-owner approval depending on your org's
   third-party app policy. Approve it.

Set these in the auth service's `.env`:

```dotenv
PACKYARD_GITHUB_CLIENT_ID=<client id>
PACKYARD_GITHUB_CLIENT_SECRET=<client secret>
PACKYARD_GITHUB_REDIRECT_URI=https://admin.pkg.example.org/api/v1/auth/callback/github
PACKYARD_GITHUB_ORG=<org-login>
```

The org-membership check is the **second lock** in our two-lock model: a
GitHub user whose email is on our allowlist but who is not a member of
`PACKYARD_GITHUB_ORG` is rejected with `403 ORG_MEMBERSHIP_REQUIRED`.

### 1.2 Microsoft Entra app

1. In the Azure portal, navigate to **Entra ID → App registrations → New
   registration**.
2. Set:
   - **Name**: `Packyard Admin (production)`.
   - **Supported account types**: **Accounts in this organisational
     directory only (single tenant)** — the tenant id below is verified on
     every login, so cross-tenant tokens are rejected.
   - **Redirect URI (Web)**:
     `https://admin.pkg.example.org/api/v1/auth/callback/microsoft`.
3. After creation, copy the **Application (client) ID** and the **Directory
   (tenant) ID**.
4. Under **Certificates & secrets → New client secret**, generate a secret
   and copy the value.
5. Under **API permissions → Add a permission → Microsoft Graph → Delegated
   permissions**, add `openid`, `profile`, `email`. Grant admin consent.

Set these in `.env`:

```dotenv
PACKYARD_MICROSOFT_CLIENT_ID=<client id>
PACKYARD_MICROSOFT_CLIENT_SECRET=<client secret>
PACKYARD_MICROSOFT_REDIRECT_URI=https://admin.pkg.example.org/api/v1/auth/callback/microsoft
PACKYARD_MICROSOFT_TENANT_ID=<tenant id>
```

The tenant id is compared case-insensitively against the `tid` claim in
the ID token; tokens from any other tenant are rejected.

---

## 2. Bootstrap the first operator

The auth service has a chicken-and-egg problem: only admins can allowlist
operators, but on a fresh deployment there are no admins. Resolve it by
setting the bootstrap env var **once**:

```dotenv
PACKYARD_BOOTSTRAP_OPERATOR_EMAIL=ops@example.org
```

On startup, if the `operators` table is empty AND
`PACKYARD_BOOTSTRAP_OPERATOR_EMAIL` is set, the auth service inserts that
email as an `active` `admin`. The bootstrap is idempotent — once any
operator exists, the variable is ignored. You can leave it in `.env` or
remove it; both behaviours are safe.

The bootstrap operator then signs in via OAuth in the normal way and uses
the SPA / API to allowlist their peers.

---

## 3. Allowlist a new operator

Two paths — the admin SPA or the API directly.

### Via the SPA (recommended)

Sign in at `https://admin.pkg.example.org/admin/`, navigate to **Operators
→ + Allowlist operator**, type the operator's email, pick the role, and
submit.

### Via the API

```bash
curl -s -X POST https://admin.pkg.example.org/api/v1/operators \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"email":"new.operator@example.org","role":"readonly"}' | jq .
```

`role` defaults to `admin` when omitted. Use `readonly` for operators who
should be able to view but not mutate (they can still read the audit log).

The email you allowlist **must** match the verified email returned by the
operator's IdP (case-insensitive, whitespace-trimmed). The audit log
records the allowlisting in an `operator.add` row attributed to the
acting admin.

---

## 4. First login

The newly-allowlisted operator goes to
`https://admin.pkg.example.org/admin/login` and clicks one of the provider
buttons. After successful OAuth:

1. The auth service exchanges the code for an ID token.
2. It extracts the verified email from the token, canonicalises it
   (lowercase + trim), and looks up the matching `operators` row.
3. If the row exists and `status = 'active'`, a session is created and the
   browser is redirected to `/admin/`.
4. **First login per provider** populates one-time forensic columns on the
   operator row:
   - `last_login_at` (always updated)
   - `github_username` (set on first GitHub login; never overwritten)
   - `microsoft_upn` (set on first Microsoft login; never overwritten)
   - `first_seen_provider` (set to whichever provider was used first)

A `login.success` audit row is written. The session is good for 24 hours
absolute / 8 hours idle.

If something goes wrong, the operator is bounced back to
`/admin/login?error=CODE` — see [Troubleshooting →
operator login fails](troubleshooting.md#operator-login-fails).

---

## 5. Change roles

Admin only. Either through the SPA's Operators page (inline role select) or
via the API:

```bash
curl -s -X PATCH "https://admin.pkg.example.org/api/v1/operators/${OP_ID}" \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"role":"readonly"}' | jq .
```

Demoting `admin → readonly` deletes the target's active sessions so the new
role takes effect on the next request. Promoting `readonly → admin` does
**not** force-logout — the existing session keeps working.

The PATCH wraps the count guard + role + status change in one serializable
transaction. A mutation that would leave zero active admins anywhere in
the system returns `403 OPERATOR_SELF_LOCKOUT` — not just self-mutation,
but any change that would remove the last admin. Add another admin first.

---

## 6. Disable / re-enable an operator

Disabling is the operator equivalent of "delete" — the row stays around
for audit attribution but the operator can no longer sign in:

```bash
curl -s -X PATCH "https://admin.pkg.example.org/api/v1/operators/${OP_ID}" \
  -H 'Content-Type: application/json' \
  --cookie "$COOKIE" \
  -d '{"status":"disabled"}' | jq .
```

Effects:

- The target's active sessions are deleted immediately (force-logout).
- The next OAuth callback for this email returns `403 OPERATOR_DISABLED`.
- An `operator.disable` audit row is written.

Re-enable with `{"status":"active"}`. The operator can sign in again on
their next visit; existing sessions were destroyed at disable time so they
must re-authenticate.

There is no DELETE endpoint by design — preserving the operator row keeps
audit-log attribution stable forever. If the operator leaves the company,
disable them.

---

## 7. Failure modes and operator expectations

### 7.1 IdP email change is destructive

The operator's identity in our system is their **email address**. If a
GitHub user changes their primary email, or an Entra account's UPN /
preferred username is updated to a new address, **the next OAuth callback
will fail with `OPERATOR_NOT_ALLOWED`** — the auth service looks up the
operator by canonical email and finds nothing.

There is no automatic remap. The mitigation is procedural:

- Operators must **contact an existing admin** before changing their
  primary email at the IdP.
- The admin then runs:
  - `POST /api/v1/operators` to allowlist the new email as a fresh row, OR
  - SQL surgery (last resort, with a logged justification) to update the
    `email` column on the existing row.
- The first approach is the documented path. The second preserves the
  `github_username` / `microsoft_upn` linkage but bypasses the usual
  audit trail. Prefer the first.

### 7.2 Session lifetime expectations

| Setting           | Value | What it means                                                  |
|-------------------|-------|----------------------------------------------------------------|
| Idle timeout      | 8 h   | No request for 8 h → next request returns `SESSION_EXPIRED`    |
| Absolute lifetime | 24 h  | Even with continuous activity, re-login required after 24 h    |
| Cookie scope      | `/`   | Same cookie covers both the SPA (`/admin/*`) and the API       |

Sessions are server-side and revocable. An admin disabling another
operator destroys the target's sessions immediately. An operator logging
out destroys their own session. Closing the browser does **not** destroy
the session — it just drops the cookie locally; the server-side row stays
until idle expiry. This is intentional so a transient network blip does
not log everyone out.

### 7.3 Org membership (GitHub only)

A GitHub user whose email is allowlisted but who is **not** an active
member of `PACKYARD_GITHUB_ORG` is rejected with `ORG_MEMBERSHIP_REQUIRED`.
If a previously-active operator leaves the GitHub org, their next OAuth
login fails — disable them in the allowlist as well to make the audit log
consistent.

### 7.4 Both providers configured? The operator picks at login time

If both GitHub and Microsoft are configured, the `/admin/login` chooser
shows both. The operator can use either; the per-provider identity
columns (`github_username` / `microsoft_upn`) accumulate independently.
`first_seen_provider` records whichever was used first and is never
overwritten.

---

## 8. Auditing operator activity

Every operator-related state change writes an audit row. Query them at
`GET /api/v1/audit?action=operator.<…>` or via the **Audit** page in the
SPA:

| Action                  | Written when                                                     |
|-------------------------|------------------------------------------------------------------|
| `operator.add`          | A new operator is allowlisted                                    |
| `operator.role_change`  | Role flips between `admin` and `readonly` (details: `from`/`to`) |
| `operator.disable`      | Status flips to `disabled`                                       |
| `operator.enable`       | Status flips back to `active`                                    |
| `login.success`         | OAuth callback creates a session                                 |
| `login.failure`         | OAuth callback fails (reason in `details`)                       |
| `logout`                | Operator hits `POST /api/v1/auth/logout`                         |
| `auth.role_denied`      | Authenticated operator hits an endpoint their role can't use     |
| `auth.rate_limited`     | Source IP exceeds the OAuth rate-limit bucket                    |

Rows are append-only. There is no UI or API to mutate or delete them.
