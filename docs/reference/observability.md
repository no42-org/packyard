# Observability

## Prometheus Metrics

Metrics are exposed by the auth service at `http://auth:9090/metrics` (Docker-internal only).

| Metric | Type | Description |
|--------|------|-------------|
| `packyard_auth_requests_total{status="allowed\|denied\|error"}` | Counter | forwardAuth request outcomes |
| `packyard_auth_duration_seconds` | Histogram | forwardAuth latency |

Traefik exposes its own metrics on an internal `metrics` entrypoint at
`http://traefik:8082/metrics` — Docker-internal only, not published to the
host. Both endpoints are reachable from any container on the `proxy`
network.

## Accessing metrics locally

In the dev stack, exec into the auth or Traefik container directly:

```bash
# Auth service metrics
docker compose exec auth wget -qO- http://localhost:9090/metrics | grep packyard_auth

# Traefik's own metrics
docker compose exec traefik wget -qO- http://localhost:8082/metrics | head
```

## Accessing metrics in production

The auth service exposes `:9090` on the `proxy` Docker network — any
container on that network can scrape `http://auth:9090/metrics` by DNS.

Traefik's own `metrics` entrypoint binds **loopback inside the container**
(`127.0.0.1:8082`), so other containers on the `proxy` network cannot
reach it. To scrape Traefik, run Prometheus in the same network namespace
as the Traefik container:

```yaml
# compose.override.metrics.yml
services:
  prometheus:
    image: prom/prometheus:latest
    network_mode: service:traefik   # shares Traefik's network namespace
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
    # Scrape targets in prometheus.yml:
    #   - http://localhost:8082/metrics  (Traefik — same netns, so loopback works)
    # Auth metrics are reachable via auth:9090 from the proxy network even
    # without netns sharing — run a second Prometheus on the proxy network
    # if you want everything in one container.
```

For ad-hoc inspection without deploying Prometheus, SSH into the VM and
exec into the relevant container as shown in the "locally" section above.

## Monitoring checklist

| Check | Method | Target |
|-------|--------|--------|
| Endpoint availability | HTTP GET `https://pkg.example.org/gpg/lts.asc` | 99.9% monthly |
| TLS cert expiry | Alert at ≤ 30 days remaining | — |
| Auth service health | Traefik forwardAuth health check | Fail-closed |
