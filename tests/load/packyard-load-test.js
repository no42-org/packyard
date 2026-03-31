/**
 * packyard-load-test.js — k6 load test for packyard NFR validation (Story 5.5)
 *
 * INFRASTRUCTURE DEPENDENCY:
 *   Requires a live packyard stack (docker compose up -d) with:
 *   - A valid subscription key in the auth database
 *   - At least one signed RPM under rpm/core/2025/el9-x86_64/ with repodata/
 *   - Stories 5.1–5.3 passing (confirms full delivery chain before load testing)
 *
 * REQUIRED ENV VARS:
 *   BASE_URL  — packyard base URL (e.g. https://pkg.mdn.opennms.com)
 *   KEY       — a valid active subscription key
 *
 * OPTIONAL ENV VARS:
 *   VUS           — number of virtual users for the baseline scenario (default: 50)
 *   DOWNLOAD_VUS  — VUs for the download scenario (default: min(5, VUS/10), capped to avoid bandwidth saturation)
 *   COMPONENT     — Meridian component (default: core)
 *   YEAR          — Meridian release year (default: 2025)
 *   OS_ARCH       — RPM OS/arch segment (default: el9-x86_64)
 *   RPM_FILE      — RPM filename for download scenario (must be set to an existing RPM; default: meridian-core.rpm)
 *
 * USAGE (50 VU baseline + download):
 *   k6 run --env BASE_URL=https://pkg.mdn.opennms.com --env KEY=abc123 tests/load/packyard-load-test.js
 *
 * USAGE (500 VU scale):
 *   k6 run --env VUS=500 --env BASE_URL=https://pkg.mdn.opennms.com --env KEY=abc123 tests/load/packyard-load-test.js
 */

import encoding from 'k6/encoding';
import http from 'k6/http';
import { check, sleep } from 'k6';

// ─── Configuration ────────────────────────────────────────────────────────────

const BASE_URL  = (__ENV.BASE_URL  || 'https://pkg.mdn.opennms.com').replace(/\/$/, '');
const KEY       = __ENV.KEY;
const VUS       = parseInt(__ENV.VUS)  || 50;
const COMPONENT = __ENV.COMPONENT || 'core';
const YEAR      = __ENV.YEAR      || '2025';
const OS_ARCH   = __ENV.OS_ARCH   || 'el9-x86_64';
const RPM_FILE  = __ENV.RPM_FILE  || 'meridian-core.rpm';

// Download scenario uses fewer VUs to avoid saturating bandwidth during the latency baseline.
const DOWNLOAD_VUS = parseInt(__ENV.DOWNLOAD_VUS) || Math.min(5, Math.max(1, Math.floor(VUS / 10)));

if (!KEY) {
  throw new Error('KEY env var is required (a valid subscription key)');
}

// HTTP Basic credentials: encoding.b64encode is k6's built-in base64 encoder
// (btoa is a browser/Node API and is not available in k6's Goja runtime).
const AUTH_HEADER = `Basic ${encoding.b64encode('subscriber:' + KEY)}`;

// Commonly referenced URL segments.
const RPM_BASE     = `${BASE_URL}/rpm/${COMPONENT}/${YEAR}/${OS_ARCH}`;
const METADATA_URL = `${RPM_BASE}/repodata/repomd.xml`;
const DOWNLOAD_URL = `${RPM_BASE}/${RPM_FILE}`;

// ─── k6 Options ───────────────────────────────────────────────────────────────

export const options = {
  /**
   * Named scenarios allow `baselineScenario` and `downloadScenario` to run
   * concurrently with independent VU pools. The `exec` field references the
   * exported function name.
   */
  scenarios: {
    baseline: {
      executor: 'constant-vus',
      vus:      VUS,
      duration: '60s',
      exec:     'baselineScenario',
    },
    download: {
      executor: 'constant-vus',
      vus:      DOWNLOAD_VUS,
      duration: '60s',
      exec:     'downloadScenario',
    },
  },

  /**
   * NFR thresholds — k6 exits non-zero if any threshold is breached (AC5).
   *
   * NFR1: forwardAuth p95 ≤ 100ms  (tagged requests within baselineScenario)
   * NFR2: repository metadata TTFB p95 ≤ 2s
   * NFR11: fail-closed — every check passes (zero unauthenticated 200s; no rate-limit headers)
   *
   * Note: {scenario:forwardAuth} and {scenario:metadata} filter by the user-defined
   * custom tag set on each request, NOT the k6 built-in scenario name.
   */
  thresholds: {
    'http_req_duration{scenario:forwardAuth}': ['p(95)<100'],   // NFR1
    'http_req_duration{scenario:metadata}':    ['p(95)<2000'],  // NFR2
    'checks':                                  ['rate>=1.0'],   // NFR11 + NFR3
  },
};

// ─── Baseline scenario (NFR1, NFR2, NFR11) ───────────────────────────────────

export function baselineScenario() {

  // ── forwardAuth sub-request (NFR1) ───────────────────────────────────────
  // Measures the full round-trip: subscriber → Traefik (forwardAuth) → nginx.
  // The /auth endpoint is Traefik-internal; end-to-end latency is the best proxy.
  const authRes = http.get(METADATA_URL, {
    headers: { 'Authorization': AUTH_HEADER },
    tags:    { scenario: 'forwardAuth' },
  });
  check(authRes, {
    'forwardAuth: status 200': (r) => r.status === 200,
  });

  sleep(0.05);

  // ── metadata sub-request (NFR2) ──────────────────────────────────────────
  // Separate tagged request for TTFB measurement of repository metadata.
  const metaRes = http.get(METADATA_URL, {
    headers: { 'Authorization': AUTH_HEADER },
    tags:    { scenario: 'metadata' },
  });
  check(metaRes, {
    'metadata: status 200':       (r) => r.status === 200,
    'metadata: has content-type': (r) => r.headers['content-type'] !== undefined,
  });

  sleep(0.05);

  // ── noauth sub-request (NFR11 — fail-closed) ─────────────────────────────
  // Unauthenticated request MUST return 401, NEVER 200.
  // Any check failure here causes 'checks' threshold to breach → non-zero exit.
  const noAuthRes = http.get(METADATA_URL, {
    tags: { scenario: 'noauth' },
  });
  check(noAuthRes, {
    'noauth: returns 401 (fail-closed)': (r) => r.status === 401,
    'noauth: never returns 200':         (r) => r.status !== 200,
  });

  sleep(0.1);
}

// ─── Download scenario (NFR3 — no application-layer throttling) ───────────────
// Runs concurrently with baselineScenario via options.scenarios.
// Uses fewer VUs (DOWNLOAD_VUS) to avoid saturating bandwidth during latency testing.

export function downloadScenario() {
  const res = http.get(DOWNLOAD_URL, {
    headers:      { 'Authorization': AUTH_HEADER },
    tags:         { scenario: 'download' },
    responseType: 'none',  // discard body — header verification only
    timeout:      '30s',
  });

  check(res, {
    // NFR3: no artificial rate limiting — these headers must be absent.
    'download: no Retry-After header':    (r) => r.headers['retry-after']        === undefined,
    'download: no X-RateLimit-Limit':     (r) => r.headers['x-ratelimit-limit']  === undefined,
    'download: no X-RateLimit-Remaining': (r) => r.headers['x-ratelimit-remaining'] === undefined,
    'download: status 200 or 206':        (r) => r.status === 200 || r.status === 206,
  });
}
