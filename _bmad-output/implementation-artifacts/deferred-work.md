# Deferred Work

## Deferred from: code review of 1-1-docker-compose-platform-scaffold-with-traefik-tls (2026-03-29)

- No Docker network segmentation — all services on default bridge, bypassing forwardAuth at L3; network isolation introduced with forwardAuth wiring in Stories 2.x
- `traefik-certs` volume has no backup mechanism — ACME `acme.json` loss triggers Let's Encrypt rate-limited re-issuance; address in production hardening (Story 5.4)
- `aptly.conf` `enableChecksumDownload: false` — upstream mirror downloads not checksum-validated; address in Story 4.3 aptly configuration
- `rustfs/rustfs:latest` mutable image tag — silently replaced on `docker compose pull`; pin to explicit digest in production hardening pass
