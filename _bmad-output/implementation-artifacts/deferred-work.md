# Deferred Work

## Deferred from: code review of 1-1-docker-compose-platform-scaffold-with-traefik-tls (2026-03-29)

- No Docker network segmentation — all services on default bridge, bypassing forwardAuth at L3; network isolation introduced with forwardAuth wiring in Stories 2.x
- `traefik-certs` volume has no backup mechanism — ACME `acme.json` loss triggers Let's Encrypt rate-limited re-issuance; address in production hardening (Story 5.4)
- `aptly.conf` `enableChecksumDownload: false` — upstream mirror downloads not checksum-validated; address in Story 4.3 aptly configuration
- `rustfs/rustfs:latest` mutable image tag — silently replaced on `docker compose pull`; pin to explicit digest in production hardening pass

## Deferred from: code review of 1-2-gpg-public-key-endpoint (2026-03-30)

- `docker compose ps` field-name case (`.Name`/`.State`) varies across Compose versions — portability fix for health-check.sh in hardening pass
- `PathPrefix('/gpg/')` routes all `/gpg/*` publicly — intentional today; review if additional files added under `static/gpg/`
- `curl -k` unconditional in health-check — masks TLS cert expiry in production; make conditional in Story 5.4 hardening
- Dev GPG key (`meridian-dev@opennms.com`, fingerprint `6DB8ACBC99143F98B02CDBCBDDF27FC6812E6214`) must be replaced with real Meridian signing key before production
- `static` container has no Docker healthcheck — always reports `running`, never `healthy`; add healthcheck in production hardening
