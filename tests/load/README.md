# Packyard Load Tests

k6-based performance validation against a live packyard stack, targeting the NFR thresholds defined in the product requirements.

---

## Infrastructure Requirements

**This test requires real infrastructure — it cannot be run with mocks.**

1. **Running packyard stack**
   ```bash
   docker compose up -d
   ```
   All services must be healthy: Traefik, auth, nginx (rpm, deb), Zot, Aptly, RustFS, static.

2. **A valid subscription key**
   ```bash
   curl -X POST https://pkg.mdn.opennms.com/api/v1/keys \
     -H 'Content-Type: application/json' \
     -d '{"component": "core", "label": "load-test"}'
   ```

3. **Signed RPM packages published** — at least one signed RPM under `rpm/core/2025/el9-x86_64/` with `repodata/repomd.xml` present. Run Stories 5.1–5.3 e2e tests first to confirm the full delivery chain works before load testing.

4. **k6 installed** — https://k6.io/docs/get-started/installation/
   ```bash
   brew install k6            # macOS
   sudo snap install k6       # Ubuntu/Debian
   ```

---

## Required Environment Variables

| Variable   | Required | Description                                              |
|------------|----------|----------------------------------------------------------|
| `BASE_URL` | Yes      | Packyard base URL (e.g. `https://pkg.mdn.opennms.com`)  |
| `KEY`      | Yes      | A valid active subscription key in the auth database     |

## Optional Environment Variables

| Variable        | Default                   | Description                                                   |
|-----------------|---------------------------|---------------------------------------------------------------|
| `VUS`           | `50`                      | Virtual users for the baseline scenario                       |
| `DOWNLOAD_VUS`  | `min(5, VUS/10)`          | Virtual users for the download scenario (capped to avoid bandwidth saturation) |
| `COMPONENT`     | `core`                    | Meridian component                                            |
| `YEAR`          | `2025`                    | Meridian release year                                         |
| `OS_ARCH`       | `el9-x86_64`              | RPM OS/arch path segment                                      |
| `RPM_FILE`      | `meridian-core.rpm`       | RPM filename for download scenario — **must be set to an existing versioned filename** |

---

## Running the Tests

### Baseline — 50 VUs (NFR1, NFR2, NFR11)

```bash
k6 run \
  --env BASE_URL=https://pkg.mdn.opennms.com \
  --env KEY=your-subscription-key \
  tests/load/packyard-load-test.js
```

Runs for 60 seconds at 50 concurrent virtual users.

### Scale test — 500 VUs (NFR13)

```bash
k6 run \
  --env VUS=500 \
  --env BASE_URL=https://pkg.mdn.opennms.com \
  --env KEY=your-subscription-key \
  tests/load/packyard-load-test.js
```

### Download / NFR3 throughput scenario

The download scenario runs concurrently with the baseline via `options.scenarios` — no separate invocation needed. It uses a smaller VU pool (default: `min(5, VUS/10)`) to avoid saturating bandwidth during the latency baseline.

To override the download VU count:

```bash
k6 run \
  --env BASE_URL=https://pkg.mdn.opennms.com \
  --env KEY=your-subscription-key \
  --env RPM_FILE=meridian-core-2025.1.0.x86_64.rpm \
  --env DOWNLOAD_VUS=3 \
  tests/load/packyard-load-test.js
```

**Note:** `RPM_FILE` must be set to an existing versioned filename in the repo (e.g. `meridian-core-2025.1.0.x86_64.rpm`). The default `meridian-core.rpm` is a placeholder — a 404 will fail the download checks.

### Save results for CI artifact storage

```bash
k6 run \
  --env BASE_URL=https://pkg.mdn.opennms.com \
  --env KEY=your-subscription-key \
  --summary-export=tests/load/results.json \
  tests/load/packyard-load-test.js
```

---

## Expected Results

### NFR Thresholds

| NFR   | Target                        | k6 Threshold                                           |
|-------|-------------------------------|--------------------------------------------------------|
| NFR1  | forwardAuth p95 ≤ 100ms       | `http_req_duration{scenario:forwardAuth} p(95)<100`    |
| NFR2  | Metadata TTFB p95 ≤ 2s        | `http_req_duration{scenario:metadata} p(95)<2000`      |
| NFR3  | No application-layer throttling | No `Retry-After` or `X-RateLimit-*` response headers |
| NFR11 | Fail-closed (no unauth 200)   | `checks rate==1.0`                                     |
| NFR13 | 500 concurrent subscribers    | Run with `--env VUS=500`                               |

### Interpreting the k6 exit code

| Exit Code | Meaning                                                        |
|-----------|----------------------------------------------------------------|
| `0`       | All NFR thresholds passed                                      |
| `non-zero` | One or more thresholds breached — check k6 summary output    |

A non-zero exit code in CI indicates an NFR regression. Check the k6 summary for which threshold failed:

```
✗ http_req_duration{scenario:forwardAuth}............: p(95)=143ms  ✗ (threshold: p(95)<100)
```

### What each scenario measures

- **forwardAuth** — full round-trip: subscriber → Traefik (forwardAuth call to auth) → nginx → subscriber. The `/auth` endpoint is Traefik-internal, so this measures the combined path. forwardAuth is the primary latency contributor.
- **metadata** — repository metadata (`repomd.xml`) fetch with auth. Measures TTFB including auth and nginx.
- **noauth** — unauthenticated requests. Verifies NFR11 fail-closed: every request must return 401, never 200.

---

## Notes

- The load test does NOT spin up infrastructure — it targets an already-running stack.
- Run Stories 5.1–5.3 e2e tests before load testing to confirm the delivery chain is functional.
- The 500 VU test (NFR13) requires adequate VM resources; run on the production host or a representative staging host.
- Subscription key values are embedded in `Authorization` headers during the test. Use a dedicated load-test key (not a production subscriber key) and revoke it after testing.
