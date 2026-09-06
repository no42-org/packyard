# Packyard End-to-End Tests

End-to-end tests that validate the full subscriber experience against a live packyard stack.
These tests require real infrastructure — they cannot be run with mocks.

---

## Infrastructure Requirements

Before running any e2e test, the following must be in place:

### 1. Running packyard stack

```bash
docker compose up -d
```

All services must be healthy: Traefik, auth, nginx (rpm, deb), Zot, Aptly, RustFS, static.

### 2. A valid subscription key in the auth database

```bash
# Insert a test key into the auth service SQLite database:
sqlite3 /path/to/auth-db/auth.db \
  "INSERT INTO subscription_key (id, component, label, active, created_at)
   VALUES ('YOUR_TEST_KEY', 'core', 'e2e-test', 1, datetime('now'));"
```

Or use the admin API if available:
```bash
curl -X POST https://pkg.example.org/api/v1/keys \
  -H 'Content-Type: application/json' \
  -d '{"component": "core", "label": "e2e-test"}'
```

### 3. Signed packages published to the stack

At least one signed package per format under test must exist in the serving tree.

**RPM** — must have a signed RPM under `rpm-data` and `repodata/` generated:
```bash
# Option A: Use the promotion workflow (recommended)
# 1. Upload a test RPM to RustFS staging:
RUSTFS_ACCESS_KEY=... RUSTFS_SECRET_KEY=... \
  bash scripts/stage-artifact.sh /path/to/lts-core.rpm core rpm 2025 el9-x86_64
# 2. Trigger the promote-rpm GHA workflow for component=core, series=2025, os=el9-x86_64

# Option B: Manual (for local dev only)
docker compose exec rpm cp /path/to/lts-core.rpm /usr/share/nginx/html/core/2025/el9-x86_64/
docker compose exec rpm createrepo_c --update /usr/share/nginx/html/core/2025/el9-x86_64/
```

**DEB** — must have signed DEBs and an Aptly published snapshot:
```bash
# Option A: Use the promotion workflow (recommended)
# 1. Upload a test DEB to RustFS staging:
RUSTFS_ACCESS_KEY=... RUSTFS_SECRET_KEY=... \
  bash scripts/stage-artifact.sh /path/to/lts-core_2025.1.0_amd64.deb core deb 2025 bookworm
# 2. Trigger the promote-deb GHA workflow for component=core, series=2025, distro=bookworm

# Option B: Manual (for local dev only — requires aptly container access)
docker compose exec aptly /scripts/create-snapshot.sh core 2025 bookworm
docker compose exec aptly /scripts/publish-snapshot.sh core 2025 bookworm
```

**OCI** — must have cosign-signed multi-arch image index in Zot:
```bash
# Use the promote-oci GHA workflow for component=core, series=2025
# The workflow pushes x86_64 and arm64 images, creates a multi-arch index,
# and signs all three with cosign (stored in Zot alongside the images).

# Option B: Manual (for local dev only — requires SSH tunnel to Zot on port 5000)
ssh -fN -L 5000:localhost:5000 deploy@HOST
crane push /tmp/test-amd64.tar localhost:5000/lts-core:2025-x86_64 --insecure
crane push /tmp/test-arm64.tar localhost:5000/lts-core:2025-arm64 --insecure
```

---

## Required Environment Variables

| Variable   | Required | Description                                              |
|------------|----------|----------------------------------------------------------|
| `BASE_URL` | Yes      | Packyard base URL (e.g. `https://pkg.example.org`)  |
| `VALID_KEY`| Yes      | A valid active subscription key in the auth database     |

## Optional Environment Variables (per test)

| Variable    | Default       | Description                        |
|-------------|---------------|------------------------------------|
| `COMPONENT` | `core`        | LTS component                 |
| `SERIES`    | `2025`        | LTS release series            |
| `OS_ARCH`   | `el9-x86_64`  | RPM OS/arch path segment           |
| `DISTRO`    | `bookworm`    | DEB distro name                    |
| `PACKAGE`   | `lts-core` | Package name to install          |

---

## Running the Tests

### RPM subscriber test (Story 5.1)

**Prerequisites:** `dnf`, `rpm`, `python3` installed on the test host (requires RHEL/Rocky/CentOS or a compatible container).

```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
bash tests/e2e/rpm-subscriber.sh
```

### DEB subscriber test (Story 5.2)

**Prerequisites:** `apt-get`, `dpkg`, `python3`, `curl`, `gpg` installed (requires Debian/Ubuntu host or container).

```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
bash tests/e2e/deb-subscriber.sh
```

Optional overrides:
```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
COMPONENT=core \
SERIES=2025 \
DISTRO=bookworm \
PACKAGE=lts-core \
bash tests/e2e/deb-subscriber.sh
```

**Note on GPG:** The script uses `[signed-by=...]` in the sources.list (modern approach — does not use deprecated `apt-key add`). The LTS GPG key is fetched from `${BASE_URL}/gpg/lts.asc` and dearmored to a temp file at runtime.

### OCI subscriber test (Story 5.3)

**Prerequisites:** `docker`, `crane`, `cosign`, `curl`, `jq` installed.

```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
bash tests/e2e/oci-subscriber.sh
```

Optional overrides:
```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
COMPONENT=core \
SERIES=2025 \
bash tests/e2e/oci-subscriber.sh
```

**Note on cosign verification:** Cosign signatures are stored in Zot alongside the image as OCI objects (co-located at a digest-based tag). Images are signed keylessly by `promote-oci.yml`, so AC4 verifies against that workflow's identity (`--certificate-identity-regexp`, override with `COSIGN_CERT_IDENTITY_REGEXP` on forks) and needs outbound HTTPS to the Sigstore transparency log. There is no public key to fetch.

**Note on multi-arch:** `docker pull` automatically selects the correct architecture from the OCI image index. Both `amd64` and `arm64` manifests must be present in the index (verified by `crane manifest` in AC2).

### Observability test (Story 5.4)

**Prerequisites:** `curl`, `docker`, `jq` installed. The auth service metrics endpoint must be reachable at `http://localhost:9090/metrics` (or override via `METRICS_URL`).

```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
bash tests/e2e/observability.sh
```

Optional overrides:
```bash
BASE_URL=https://pkg.example.org \
VALID_KEY=your-subscription-key \
METRICS_URL=http://auth:9090/metrics \
bash tests/e2e/observability.sh
```

**What this verifies:**
- AC1: `packyard_auth_requests_total` and `packyard_auth_duration_seconds` appear in `/metrics`
- AC2: subscription key values do not appear in Traefik or auth logs (NFR5); `Authorization` headers are redacted and `ClientUsername` is dropped from Traefik access logs (C3)
- AC3: `scripts/backup-keystore.sh` produces a valid, integrity-checked SQLite backup in the `auth-backup` volume
- AC4: restore procedure is documented in `docs/ops/restore-keystore.md` (manual — requires volume manipulation)

---

## CI Requirements

These tests are integration tests, not unit tests. They require:

- Docker Compose v2 (`docker compose`)
- A reachable packyard stack (cannot run without live infrastructure)
- Format-specific clients (`dnf` for RPM, `apt-get` for DEB, `docker`/`podman` for OCI)
- Network access to `BASE_URL`

**CI approach (`.github/workflows/integration.yml`):** the workflow drives the stack through Makefile targets.
`make ci-stack-up` builds auth, rpm, static, backup and aptly from the working tree (labelled with the commit via `CI_REVISION`), starts the CI compose stack and waits for routing and health.
`make ci-verify-env` proves the forwarded `PACKYARD_*` variables reach the auth container and that compose rejects a missing `ADMIN_DOMAIN`.
It also asserts every built service runs an image whose revision label is the commit under test (`-dirty` if the tree had uncommitted changes), so a stale or pulled image does not pass unnoticed, and that the backup and aptly tags in `compose.yml` match their Dockerfile ARGs.
`make ci-seed` gives CI an operator session without OAuth: `scripts/ci/seed-integration.sh` inserts one session row for the bootstrap operator into the auth database (via a one-off `sqlite3` sidecar on the `auth-db` volume), verifies it with `GET /api/v1/auth/whoami`, then provisions components, a per-run subscriber account and a key through the real API with the session cookie and an `Origin` header derived from `ADMIN_DOMAIN`.
`make e2e-observability` runs this suite with the seeded key.
To reproduce locally: export `COMPOSE_PROJECT_NAME=packyard-ci` and `COMPOSE_FILE=compose.yml:compose.override.ci.yml` (add `:compose.override.arm64.yml` on Apple silicon), point `COMPOSE_ENV_FILES` at a copy of the workflow's `.env`, and run the same targets.
The `ci-*` targets refuse to run without those two exports, so they cannot touch a developer's real stack.
Built images are tagged with the names from `compose.yml`, so on a shared daemon a CI run temporarily replaces the published tags; `make ci-stack-down` removes them again.

---

## Fixtures

| File | Purpose |
|------|---------|
| `fixtures/lts-test.repo.tmpl` | RPM `.repo` file template; `{{BASE_URL}}`, `{{COMPONENT}}`, `{{SERIES}}`, `{{OS_ARCH}}` substituted at runtime |
| `fixtures/lts-test.list.tmpl` | DEB `sources.list` template; `{{KEY}}`, `{{BASE_URL_HOST}}`, `{{COMPONENT}}`, `{{SERIES}}`, `{{DISTRO}}` substituted at runtime |
| `fixtures/docker-daemon.json.tmpl` | Docker auth template; documents `docker login` as the recommended credential approach for OCI pull |
