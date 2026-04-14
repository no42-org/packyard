# Observability

## Prometheus Metrics

Metrics are exposed by the auth service at `http://auth:9090/metrics` (Docker-internal only).

| Metric | Type | Description |
|--------|------|-------------|
| `packyard_auth_requests_total{status="allowed\|denied\|error"}` | Counter | forwardAuth request outcomes |
| `packyard_auth_duration_seconds` | Histogram | forwardAuth latency |

Traefik metrics are available at `http://localhost:8443/metrics` (loopback only).

## Accessing metrics locally

```bash
# Auth service metrics (local stack)
curl -s http://localhost:9090/metrics | grep packyard_auth

# Traefik metrics (via loopback admin entrypoint)
curl -s http://localhost:8443/metrics
```

## Accessing metrics in production

Metrics endpoints are Docker-internal. Expose to an external monitoring stack via SSH tunnel:

```bash
ssh -L 9090:auth:9090 deploy@pkg.example.com -N &
curl -s http://localhost:9090/metrics
```

## Monitoring checklist

| Check | Method | Target |
|-------|--------|--------|
| Endpoint availability | HTTP GET `https://pkg.example.com/gpg/meridian.asc` | 99.9% monthly |
| TLS cert expiry | Alert at ≤ 30 days remaining | — |
| Auth service health | Traefik forwardAuth health check | Fail-closed |
