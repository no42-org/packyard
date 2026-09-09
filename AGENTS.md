# AGENTS.md

Guidance for coding agents working in this repository.

## What this is

Packyard is a self-hosted, authenticated distribution platform for LTS releases. Traefik terminates TLS and calls the Go `auth` service as forwardAuth for every request. Authenticated subscribers reach RPM (nginx), DEB (nginx in front of Aptly) and OCI (Zot) repositories. Artifacts are promoted from CI into RustFS object storage and signed. The auth service keeps accounts, keys, components, operators and an audit log in SQLite and embeds a React admin UI.

## Commands

Always go through `make`. CI runs the same targets, so they must stay in sync.

| Task | Command |
|------|---------|
| Build the auth binary with the embedded admin UI | `make build` |
| Run the auth unit tests | `make test` |
| Run one test | `cd auth && go test ./internal/handler -run TestName` |
| gofmt check and go vet | `make lint` |
| Lint workflow files | `make lint-workflows` |
| Build the docs site (fails on broken links) | `make docs-build` |
| Serve docs with live reload | `make docs-serve` |
| Bring up the CI compose stack locally | `make ci-stack-up` |

`make help` lists everything. Docs need Node 20 via `.nvmrc`.

## Layout

- `auth/` is the Go module. `cmd/server` is the entry point. `internal/` holds `handler`, `middleware`, `store` (SQLite), `audit`, `metrics`, `logsafe` and `adminui`. The React SPA source lives in `internal/admin-ui` and builds into the Go embed directory.
- `rpm/`, `static/`, `aptly/`, `backup/` each hold one Dockerfile. `auth`, `rpm` and `static` are released by tag; `aptly` and `backup` are versioned by the upstream tool they wrap.
- `docs/` is the Docusaurus site published to https://no42-org.github.io/packyard/. New pages must be added to `sidebars.ts`. The GitHub Wiki is retired.
- `scripts/ci/` holds the compose stack helpers the `ci-*` make targets call.

## Conventions

- Compose files are `compose.yml` and `compose.override.<name>.yml`, never `docker-compose.yml`.
- Every source file starts with the SPDX header `GPL-3.0-or-later`.
- Never commit to `main`. Branch as `<type>/<short-description>`, use Conventional Commit messages and open a PR that references an issue with `Closes #N`. PRs are squash-merged, so the PR title becomes the commit subject.
- Commits carry `Assisted-by: <Agent>:<model>` followed by a human `Signed-off-by` (`git commit -s`). See CONTRIBUTING.md.
- Releases are tag driven. RELEASING.md documents the version bump locations and the tagging scheme. Change it in the same PR as any pipeline change.

## Admin API errors

Return a machine-readable `code` in SCREAMING_SNAKE_CASE, a human-readable `message`, and any context fields that make the error self-contained:

```json
{"code": "KEY_SCOPE_MISMATCH", "message": "Key 'abc123' is scoped to 'core' but requested path is '/minion/'", "component_requested": "minion", "key_scope": "core"}
```

Package-serving endpoints (RPM, DEB, OCI) may answer a bare `401`; `dnf`, `apt` and `docker` do not read bodies.
