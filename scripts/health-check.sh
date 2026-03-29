#!/usr/bin/env bash
# health-check.sh — verify packyard stack is operational
# Exit 0 on success, non-zero on failure.
# Extended by later stories (Story 1.2 adds /gpg/meridian.asc check).
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

if [[ $FAILED -ne 0 ]]; then
    echo ""
    echo "HEALTH CHECK FAILED — one or more services are not healthy"
    exit 1
fi

echo ""
echo "All services healthy"
