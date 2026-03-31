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
curl -X POST https://pkg.mdn.opennms.com/api/v1/keys \
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
  bash scripts/stage-artifact.sh /path/to/meridian-core.rpm core rpm 2025 el9-x86_64
# 2. Trigger the promote-rpm GHA workflow for component=core, year=2025, os=el9-x86_64

# Option B: Manual (for local dev only)
docker compose exec rpm cp /path/to/meridian-core.rpm /usr/share/nginx/html/core/2025/el9-x86_64/
docker compose exec rpm createrepo_c --update /usr/share/nginx/html/core/2025/el9-x86_64/
```

**DEB** — must have signed DEBs and an Aptly published snapshot:
```bash
# Option A: Use the promotion workflow (recommended)
# 1. Upload a test DEB to RustFS staging:
RUSTFS_ACCESS_KEY=... RUSTFS_SECRET_KEY=... \
  bash scripts/stage-artifact.sh /path/to/meridian-core_2025.1.0_amd64.deb core deb 2025 bookworm
# 2. Trigger the promote-deb GHA workflow for component=core, year=2025, distro=bookworm

# Option B: Manual (for local dev only — requires aptly container access)
docker compose exec aptly /scripts/create-snapshot.sh core 2025 bookworm
docker compose exec aptly /scripts/publish-snapshot.sh core 2025 bookworm
```

**OCI** — must have cosign-signed multi-arch image index in Zot:
```bash
# Use the promote-oci GHA workflow for component=core, year=2025
# The workflow pushes x86_64 and arm64 images, creates a multi-arch index,
# and signs all three with cosign (stored in Zot alongside the images).

# Option B: Manual (for local dev only — requires SSH tunnel to Zot on port 5000)
ssh -fN -L 5000:localhost:5000 deploy@HOST
crane push /tmp/test-amd64.tar localhost:5000/meridian-core:2025-x86_64 --insecure
crane push /tmp/test-arm64.tar localhost:5000/meridian-core:2025-arm64 --insecure
```

---

## Required Environment Variables

| Variable   | Required | Description                                              |
|------------|----------|----------------------------------------------------------|
| `BASE_URL` | Yes      | Packyard base URL (e.g. `https://pkg.mdn.opennms.com`)  |
| `VALID_KEY`| Yes      | A valid active subscription key in the auth database     |

## Optional Environment Variables (per test)

| Variable    | Default       | Description                        |
|-------------|---------------|------------------------------------|
| `COMPONENT` | `core`        | Meridian component                 |
| `YEAR`      | `2025`        | Meridian release year              |
| `OS_ARCH`   | `el9-x86_64`  | RPM OS/arch path segment           |
| `DISTRO`    | `bookworm`    | DEB distro name                    |
| `PACKAGE`   | `meridian-core` | Package name to install          |

---

## Running the Tests

### RPM subscriber test (Story 5.1)

**Prerequisites:** `dnf`, `rpm`, `python3` installed on the test host (requires RHEL/Rocky/CentOS or a compatible container).

```bash
BASE_URL=https://pkg.mdn.opennms.com \
VALID_KEY=your-subscription-key \
bash tests/e2e/rpm-subscriber.sh
```

### DEB subscriber test (Story 5.2)

**Prerequisites:** `apt-get`, `dpkg`, `python3`, `curl`, `gpg` installed (requires Debian/Ubuntu host or container).

```bash
BASE_URL=https://pkg.mdn.opennms.com \
VALID_KEY=your-subscription-key \
bash tests/e2e/deb-subscriber.sh
```

Optional overrides:
```bash
BASE_URL=https://pkg.mdn.opennms.com \
VALID_KEY=your-subscription-key \
COMPONENT=core \
YEAR=2025 \
DISTRO=bookworm \
PACKAGE=meridian-core \
bash tests/e2e/deb-subscriber.sh
```

**Note on GPG:** The script uses `[signed-by=...]` in the sources.list (modern approach — does not use deprecated `apt-key add`). The Meridian GPG key is fetched from `${BASE_URL}/gpg/meridian.asc` and dearmored to a temp file at runtime.

### OCI subscriber test (Story 5.3)

**Prerequisites:** `docker`, `crane`, `cosign`, `curl`, `jq` installed.

```bash
BASE_URL=https://pkg.mdn.opennms.com \
VALID_KEY=your-subscription-key \
bash tests/e2e/oci-subscriber.sh
```

Optional overrides:
```bash
BASE_URL=https://pkg.mdn.opennms.com \
VALID_KEY=your-subscription-key \
COMPONENT=core \
YEAR=2025 \
bash tests/e2e/oci-subscriber.sh
```

**Note on offline cosign verification:** Cosign signatures are stored in Zot alongside the image as OCI objects (co-located at a digest-based tag). Verification uses `--insecure-ignore-tlog` to skip the Sigstore transparency log — no internet access is needed once the image and signature are pulled. The cosign public key is fetched once from `${BASE_URL}/gpg/cosign.pub` (subscriber onboarding step).

**Note on multi-arch:** `docker pull` automatically selects the correct architecture from the OCI image index. Both `amd64` and `arm64` manifests must be present in the index (verified by `crane manifest` in AC2).

### Observability test (Story 5.4)

**Prerequisites:** `curl`, `docker`, `jq` installed. The auth service metrics endpoint must be reachable at `http://localhost:9090/metrics` (or override via `METRICS_URL`).

```bash
BASE_URL=https://pkg.mdn.opennms.com \
VALID_KEY=your-subscription-key \
bash tests/e2e/observability.sh
```

Optional overrides:
```bash
BASE_URL=https://pkg.mdn.opennms.com \
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

**Recommended CI approach:** Run in a dedicated integration test job that spins up the full stack via `docker compose up -d`, waits for health checks, seeds test data, then runs the e2e scripts.

---

## Fixtures

| File | Purpose |
|------|---------|
| `fixtures/meridian-test.repo.tmpl` | RPM `.repo` file template; `{{BASE_URL}}`, `{{COMPONENT}}`, `{{YEAR}}`, `{{OS_ARCH}}` substituted at runtime |
| `fixtures/meridian-test.list.tmpl` | DEB `sources.list` template; `{{KEY}}`, `{{BASE_URL_HOST}}`, `{{COMPONENT}}`, `{{YEAR}}`, `{{DISTRO}}` substituted at runtime |
| `fixtures/docker-daemon.json.tmpl` | Docker auth template; documents `docker login` as the recommended credential approach for OCI pull |
