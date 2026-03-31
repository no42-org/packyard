#!/usr/bin/env bash
# health-check.sh — verify packyard stack is operational
# Exit 0 on success, non-zero on failure.
# Extended by later stories (Story 1.2 adds /gpg/meridian.asc check,
# Story 1.3 adds RPM routing and RustFS checks,
# Story 2.3 adds auth service health check and updates RPM routing assertion).
set -euo pipefail

FAILED=0

echo "==> Checking container health..."
while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    name=$(echo "$line" | jq -r '.Name // empty' 2>/dev/null) || { echo "WARN: unparseable line from docker compose ps"; FAILED=1; continue; }
    state=$(echo "$line" | jq -r '.State // empty' 2>/dev/null) || { echo "WARN: unparseable line from docker compose ps"; FAILED=1; continue; }
    if [[ "$state" != "running" && "$state" != "healthy" ]]; then
        echo "FAIL: $name is $state"
        FAILED=1
    else
        echo "OK:   $name ($state)"
    fi
done < <(docker compose ps --format json | jq -c 'if type=="array" then .[] else . end')

# GPG endpoint availability check (Story 1.2)
GPG_DOMAIN="${DOMAIN:-localhost}"
echo ""
echo "==> Checking GPG endpoint availability..."
HTTP_STATUS=$(curl -s -k --max-time 10 --write-out '%{http_code}' --output /dev/null \
    "https://${GPG_DOMAIN}/gpg/meridian.asc" 2>/dev/null || echo "000")
if [[ "${HTTP_STATUS}" != "200" ]]; then
    echo "FAIL: /gpg/meridian.asc returned HTTP ${HTTP_STATUS} (expected 200)"
    FAILED=1
else
    echo "OK:   /gpg/meridian.asc returned HTTP 200"
fi

# Auth service health check via admin API entrypoint (Story 2.3)
# Hits GET /api/v1/keys on the admin entrypoint (127.0.0.1:8443) — the only path routed
# on that entrypoint. Returns 200 + empty array when auth is up, proving both that the
# auth service is reachable and that the admin API route is live.
# Note: /health is NOT routed on the admin entrypoint (only PathPrefix(/api/v1/) is).
echo ""
echo "==> Checking auth service (admin API reachable on loopback 8443)..."
AUTH_STATUS=$(curl -s --max-time 10 --write-out '%{http_code}' --output /dev/null \
    "http://127.0.0.1:8443/api/v1/keys" 2>/dev/null || echo "000")
if [[ "${AUTH_STATUS}" == "200" ]]; then
    echo "OK:   auth /api/v1/keys returned HTTP 200 via admin entrypoint"
else
    echo "FAIL: auth /api/v1/keys returned HTTP ${AUTH_STATUS} (expected 200 via 127.0.0.1:8443)"
    FAILED=1
fi

# Admin API isolation check — must NOT be routable via the public websecure entrypoint (port 443)
# A 404 from Traefik means no websecure router matched, which is the correct behaviour (NFR8, FR34).
echo ""
echo "==> Checking admin API isolation (must not be routable on port 443)..."
ADMIN_PUBLIC_STATUS=$(curl -s -k --max-time 10 --write-out '%{http_code}' --output /dev/null \
    "https://${GPG_DOMAIN}/api/v1/keys" 2>/dev/null || echo "000")
if [[ "${ADMIN_PUBLIC_STATUS}" == "404" ]]; then
    echo "OK:   /api/v1/keys on port 443 returned HTTP 404 (not routed via websecure — correctly isolated)"
elif [[ "${ADMIN_PUBLIC_STATUS}" == "000" ]]; then
    echo "FAIL: /api/v1/keys on port 443 unreachable (connection refused or timeout)"
    FAILED=1
else
    echo "FAIL: /api/v1/keys on port 443 returned HTTP ${ADMIN_PUBLIC_STATUS} (expected 404 — admin API may be publicly exposed)"
    FAILED=1
fi

# RPM routing check — unauthenticated request must return 401 (real forwardAuth active since Story 2.3)
# The busybox auth stub from Story 1.3 has been replaced by the real auth service.
echo ""
echo "==> Checking RPM routing (Traefik → forwardAuth → rpm backend, expect 401 for no credentials)..."
RPM_STATUS=$(curl -s -k --max-time 10 --write-out '%{http_code}' --output /dev/null \
    "https://${GPG_DOMAIN}/rpm/" 2>/dev/null || echo "000")
if [[ "${RPM_STATUS}" == "000" ]]; then
    echo "FAIL: /rpm/ unreachable (connection refused or timeout)"
    FAILED=1
elif [[ "${RPM_STATUS}" == "401" ]]; then
    echo "OK:   /rpm/ returned HTTP 401 (forwardAuth rejecting unauthenticated request as expected)"
else
    echo "WARN: /rpm/ returned HTTP ${RPM_STATUS} (expected 401 — forwardAuth may not be wired)"
    FAILED=1
fi

# Network isolation check — traefik must NOT be able to reach rustfs (backend-only network)
# If the connection is refused or times out, isolation is working correctly (AC7).
echo ""
echo "==> Checking network isolation (traefik must not reach rustfs:9000)..."
TRAEFIK_CONTAINER=$(docker compose ps --format json | jq -r 'if type=="array" then .[] else . end | select(.Service=="traefik") | .Name // empty' 2>/dev/null | head -1)
if [[ -z "${TRAEFIK_CONTAINER}" ]]; then
    echo "FAIL: traefik container not found"
    FAILED=1
else
    # wget exits non-zero on connection failure; we expect failure here — isolation is working if it fails
    if docker exec "${TRAEFIK_CONTAINER}" wget -qO- --timeout=5 http://rustfs:9000/health > /dev/null 2>&1; then
        echo "FAIL: traefik can reach rustfs:9000 — network isolation is NOT enforced"
        FAILED=1
    else
        echo "OK:   traefik cannot reach rustfs:9000 (backend-network isolation confirmed)"
    fi
fi

# RustFS health check — query health endpoint from within the rustfs container
# Uses docker exec to avoid exposing port 9000 on the host (RustFS is internal-only)
echo ""
echo "==> Checking RustFS health..."
RUSTFS_CONTAINER=$(docker compose ps --format json | jq -r 'if type=="array" then .[] else . end | select(.Service=="rustfs") | .Name // empty' 2>/dev/null | head -1)
if [[ -z "${RUSTFS_CONTAINER}" ]]; then
    echo "FAIL: rustfs container not found"
    FAILED=1
else
    RUSTFS_HEALTH=$(docker exec "${RUSTFS_CONTAINER}" \
        wget -qO- --server-response http://localhost:9000/health 2>&1 | grep "HTTP/" | awk '{print $2}' || echo "000")
    if [[ "${RUSTFS_HEALTH}" == "200" ]]; then
        echo "OK:   RustFS health endpoint returned HTTP 200"
    else
        echo "FAIL: RustFS health endpoint returned HTTP ${RUSTFS_HEALTH} (expected 200)"
        FAILED=1
    fi
fi

if [[ $FAILED -ne 0 ]]; then
    echo ""
    echo "HEALTH CHECK FAILED — one or more services are not healthy"
    exit 1
fi

echo ""
echo "All services healthy"
