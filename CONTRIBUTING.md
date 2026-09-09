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
| Run the auth unit tests | `make test` |
| Run the admin SPA unit tests | `make admin-ui-test` |
| Bring up the full stack and smoke-test it | `docker compose up -d && bash verify.sh` |

Run `make help` to list every target.

## Workflow

1. Open or find an issue first. Every pull request references an issue with a closing keyword (`Closes #123`).
2. Branch from `main`. Use `<type>/<short-description>`, for example `fix/forward-auth-401`.
3. Keep one logical change per pull request.
4. Pull requests are squash-merged. The PR title becomes the commit subject on `main`, so give it a Conventional Commit title.
5. CI must be green before merge. The `main` ruleset requires five checks: `Auth lint`, `Auth unit tests`, `Build Docker images`, `Build docs` and `Workflow lint`. The first four are path-filtered and skip when nothing they watch changed; `Workflow lint` runs on every pull request.

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

### Keeping the trailer through the merge

Repeat both trailers at the end of the pull request description, not only on the branch commit.

Pull requests here are squash-merged, and GitHub builds the squash commit message from the pull request description rather than from the branch commits.
A trailer that lives only on a branch commit is dropped when the pull request merges, so `Assisted-by` disappears from `main` even though the branch carried it correctly.

GitHub appends its own `Signed-off-by` after the description, separated by a blank line.
That blank line splits the two into separate trailer blocks, so `git log --format='%(trailers)'` reports only the sign-off.
Search the message body instead:

```bash
git log --format='%H %s' --grep='^Assisted-by:' main
```

## Source file headers

Every new source file starts with an SPDX header matching the repository license:

```go
/*
 * Copyright 2026 Ronny Trommer <ronny@no42.org>
 * SPDX-License-Identifier: GPL-3.0-or-later
 */
```

Use the comment syntax of the language. Leave existing headers as they are.

## Container image pins

Every third-party image, in a `Dockerfile` or a compose file, is pinned by tag and digest:

```yaml
image: traefik:3.6.12@sha256:171c9c3565b29f6c133f1c1b43c5d4e5853415198e9e1078c001f8702ff66aec
```

A tag alone is mutable, and the deployment host runs watchtower, which re-pulls a tag that moved.
`make lint-image-pins` fails the build on an unpinned image and on a Zot version that differs between `compose.yml` and `compose.override.arm64.yml`.
The `ghcr.io/no42-org/packyard-*` images are the exception: the release process writes their tag, so they carry no digest.

Dependabot maintains the pins weekly: the `docker-compose` ecosystem for `compose.yml` and the `docker` ecosystem for each service `Dockerfile`.
Its compose matcher does not see `compose.override.<name>.yml`, so a bump there is manual and the lint check is what catches a forgotten one.
In practice that means one thing: when Dependabot bumps Zot in `compose.yml`, push the matching `zot-linux-arm64` change onto its branch before merging, or the gate stays red.

## Releases

Maintainers cut releases from `main` by tag. The procedure is in [RELEASING.md](RELEASING.md).

## Security issues

Do not open a public issue for a vulnerability. See [SECURITY.md](SECURITY.md).
