# Contributing to Packyard

Thanks for helping.
This page covers the mechanics: how to build, how to commit, and how a change gets in.

## Setup

Requirements: Docker Compose v2, Go (version in `auth/go.mod`), Node 20 (version in `.nvmrc`), `curl`, `jq`.

| Task | Command |
|------|---------|
| Install the docs toolchain | `make docs-install` |
| Build the docs site (fails on broken links) | `make docs-build` |
| Preview the docs with live reload | `make docs-serve` |
| Build the admin SPA into the Go embed directory | `make admin-ui` |
| Build the auth binary with the embedded SPA | `make build` |
| Run the auth unit tests | `cd auth && go test ./...` |
| Bring up the full stack and smoke-test it | `docker compose up -d && bash verify.sh` |

Run `make help` to list every target.

## Workflow

1. Open or find an issue first. Every pull request references an issue with a closing keyword (`Closes #123`).
2. Branch from `main`. Use `<type>/<short-description>`, for example `fix/forward-auth-401`.
3. Keep one logical change per pull request.
4. Pull requests are squash-merged. The PR title becomes the commit subject on `main`, so give it a Conventional Commit title.
5. CI must be green before merge. The required checks are the auth unit tests, the Docker image build and the docs build.

## Commit messages

Use [Conventional Commits](https://www.conventionalcommits.org/): `<type>[scope]: <description>`.
Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `ci`, `build`, `revert`.
A breaking change appends `!` to the type or adds a `BREAKING CHANGE:` footer.

## Developer Certificate of Origin

All commits must be signed off, certifying the [DCO](https://developercertificate.org/):

```bash
git commit -s
```

The `Signed-off-by` trailer must name a human identity.
That person is responsible for the contribution, including its license compliance.
The repository requires signed commits as well, so configure GPG or SSH commit signing.

## AI-assisted contributions

AI assistance is welcome.
Commits produced with an AI agent carry an additional `Assisted-by: <Agent>:<model>` trailer, placed before the `Signed-off-by` line:

```
Assisted-by: ClaudeCode:claude-fable-5-1
Signed-off-by: Jane Doe <jane@example.org>
```

The human signer reviews all AI-generated code and remains responsible for its correctness and license compliance.

## Source file headers

Every new source file starts with an SPDX header matching the repository license:

```go
/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
```

Use the comment syntax of the language. Leave existing headers as they are.

## Releases

Maintainers cut releases from `main` by tag. The procedure is in [RELEASING.md](RELEASING.md).

## Security issues

Do not open a public issue for a vulnerability. See [SECURITY.md](SECURITY.md).
