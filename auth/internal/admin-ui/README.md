# Admin SPA

React + TypeScript single-page app embedded into the auth service via
`go:embed` (see `auth/internal/adminui/`). Served from the public admin
host at `/admin/*`.

## Build

From the repo root:

```sh
make admin-ui          # build the SPA bundle into the Go embed dir
make build             # build the auth binary with the SPA bundled
```

## Local development

`make admin-ui-dev` starts the Vite dev server with hot reload. **It is
intended for SPA UI iteration only.**

### Why OAuth doesn't work via `npm run dev`

The Vite dev server runs on `http://localhost:5173`. The auth service
sets session cookies with the `Secure` attribute (per spec D15), which
browsers refuse to store on plain-HTTP origins. Even if you proxy
`/api/v1/*` to a running auth backend, the post-login redirect cannot
complete because the session cookie is dropped.

To test the full OAuth login flow, run the auth service through Traefik
with TLS via the compose dev override:

```sh
docker compose -f compose.yml -f compose.override.dev.yml up
```

Then visit `https://admin.${ADMIN_DOMAIN}/admin/login` (the dev override
provisions a self-signed cert for the admin host).

## Build artefacts

`npm run build` writes content-hashed assets to `dist/` here, and the
top-level `make admin-ui` target then copies them into
`auth/internal/adminui/dist/` so the Go `go:embed` directive picks them
up. `auth/internal/adminui/dist/.gitkeep` is the placeholder; everything
else in that directory is build-derived and `.gitignore`d.
