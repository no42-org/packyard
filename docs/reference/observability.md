# Observability

## Prometheus Metrics

Metrics are exposed by the auth service at `http://auth:9090/metrics` (Docker-internal only).

| Metric | Type | Description |
|--------|------|-------------|
| `packyard_auth_requests_total{status="allowed\|denied\|error"}` | Counter | forwardAuth request outcomes |
| `packyard_auth_duration_seconds` | Histogram | forwardAuth latency |
| `packyard_auth_component_cache_requests_total` | Counter | Component lookups through the public-component cache (forward-auth and admin reads) |
| `packyard_auth_component_cache_hits_total` | Counter | Lookups answered from the cache without a store call; hit ratio is hits / requests |
| `packyard_auth_component_cache_entries` | Gauge | Components currently cached as public; should track the number of public components |

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
reach it. To scrape Traefik, attach Prometheus to the `proxy` network
and bind Traefik's metrics entrypoint to all interfaces inside the
container so it is reachable by service name:

```yaml
# compose.override.metrics.yml
services:
  traefik:
    environment:
      # Override the static-config bind so metrics are reachable from
      # other containers on the proxy network. The host port mapping is
      # NOT added — metrics stay internal to the compose project.
      - TRAEFIK_METRICS_PROMETHEUS_ENTRYPOINT=metrics
      - TRAEFIK_ENTRYPOINTS_METRICS_ADDRESS=:8082
  prometheus:
    image: prom/prometheus:latest
    volumes:
      - ./prometheus.yml:/etc/prometheus/prometheus.yml:ro
    networks:
      - proxy
    # Scrape targets in prometheus.yml:
    #   - http://traefik:8082/metrics  (reachable on the proxy network)
    #   - http://auth:9090/metrics     (also on the proxy network)
```

`network_mode: service:traefik` was previously documented here but does
not work alongside the `networks:` aliases Traefik already declares —
compose v2 rejects the mix. The pattern above keeps each service on its
own network alias and lets Prometheus discover both by name.

For ad-hoc inspection without deploying Prometheus, SSH into the VM and
exec into the relevant container as shown in the "locally" section above.

## Monitoring checklist

| Check | Method | Target |
|-------|--------|--------|
| Endpoint availability | HTTP GET `https://pkg.example.org/gpg/lts.asc` | 99.9% monthly |
| TLS cert expiry | Alert at ≤ 30 days remaining | — |
| Auth service health | Traefik forwardAuth health check | Fail-closed |
