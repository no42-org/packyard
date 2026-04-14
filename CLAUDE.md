# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Packyard** is a BMad (Build Method Architecture by Design) framework installation — a multi-module AI agent methodology system for structured product development. It is not a compiled project; there are no build, lint, or test commands. All content is Markdown-based workflow definitions, agent personas, and YAML configuration.

- **BMad version:** 6.2.2
- **User:** Indigo (intermediate skill level)
- **Configured IDEs:** Claude Code, Codex

## Module Structure

The framework lives under `_bmad/` and is organized into six modules:

| Module | Path | Purpose |
|--------|------|---------|
| core | `_bmad/core/` | Shared utilities: brainstorming, document review, party-mode, distillation |
| bmm | `_bmad/bmm/` | Business Method Module — 4-phase product lifecycle (analysis → plan → solution → implement) |
| bmb | `_bmad/bmb/` | Builder Module — create/edit custom agents and workflow skills |
| cis | `_bmad/cis/` | Creative Intelligence Suite — storytelling, innovation, design thinking |
| tea | `_bmad/tea/` | Test Architecture Enterprise — full test lifecycle (ATDD, CI/CD, NFR, coverage) |
| wds | `_bmad/wds/` | Workflow Design System — UX/product design pipeline (wds-0 through wds-8) |

## Output Locations

- Planning artifacts (briefs, PRDs, specs): `_bmad-output/planning-artifacts/`
- Implementation artifacts (code, design systems): `_bmad-output/implementation-artifacts/`
- Test artifacts (plans, automation): `_bmad-output/test-artifacts/`
- Design workflow outputs (visual/UX): `design-artifacts/A-G/`
- Project knowledge docs: `docs/`

## Agent Roster

18 named agents, each with a distinct role. Invoke by name or skill ID:

- **Mary** (`bmad-agent-analyst`) — business analysis & requirements
- **John** (`bmad-agent-pm`) — product management & PRD creation
- **Sally** (`bmad-agent-ux-designer`) — UX/UI design
- **Winston** (`bmad-agent-architect`) — systems architecture
- **Amelia** (`bmad-agent-dev`) — developer / story implementation
- **Quinn** (`bmad-agent-qa`) — QA & test automation
- **Barry** (`bmad-agent-quick-flow-solo-dev`) — rapid full-stack development
- **Bob** (`bmad-agent-sm`) — scrum master & sprint ceremonies
- **Paige** (`bmad-agent-tech-writer`) — technical documentation
- **Murat** (`bmad-tea`) — master test architect (TEA module)
- **Freya** (`wds-agent-freya-ux`) — WDS UX designer
- **Saga** (`wds-agent-saga-analyst`) — WDS business analyst
- **Carson / Dr. Quinn / Maya / Victor / Caravaggio / Sophia** — CIS creative agents

## Workflow Execution Pattern

TEA and many BMM workflows use a **step-file architecture**: strict sequential step files prevent AI improvisation. When executing a workflow, follow the numbered steps in order without skipping. Each skill directory contains a `SKILL.md` defining the entry point, modes (Create / Edit / Validate), and execution rules.

## Documentation

All user-facing documentation lives in `docs/` and is published via MkDocs + mike to https://no42-org.github.io/packyard/. The GitHub Wiki is retired and must not be used.

Structure:

| Path | Section |
|------|---------|
| `docs/index.md` | Home page |
| `docs/getting-started/` | Quick-start and local development guides |
| `docs/ops/` | Operational runbooks (production deployment, restore procedures) |
| `docs/reference/` | Architecture, API, configuration, subscriber usage, promotion pipeline, observability |

When adding or updating documentation:
- Edit the relevant file under `docs/` — do not create Wiki pages
- New pages must be added to the `nav:` section in `mkdocs.yml`
- Run `make docs-serve` locally to preview before committing
- Docs are published automatically on each GitHub release via `docs.yml`

## Docker Compose

Always use `compose.yml` as the filename, not `docker-compose.yml`. Docker Compose v2 searches for `compose.yml` first — it is the current convention.

Override files follow the same pattern: `compose.override.ci.yml`, `compose.override.arm64.yml`.

## Git Workflow

- **Never commit story work directly to `main`** — every completed story must be submitted as a pull request.
- **One PR per story**: when a story reaches `done` status, create a branch, commit the story's implementation files, and open a PR against `main`.
- Story branch naming: `feat/story-{epic}-{story}-{slug}` (e.g. `feat/story-1-1-docker-compose-platform-scaffold-with-traefik-tls`)
- Use **Conventional Commits** for all commit messages:
  - `feat:` — new feature or content
  - `fix:` — bug fix or correction
  - `docs:` — documentation changes
  - `chore:` — maintenance, config, tooling
  - `refactor:` — restructuring without behaviour change
- Non-story branches: `<type>/<short-description>` (e.g. `feat/add-prd-template`, `docs/update-agent-roster`)

## API Error Response Guideline

When implementing API error responses in packyard projects, use the **Code + Message** pattern:

```json
{
  "code": "KEY_SCOPE_MISMATCH",
  "message": "Key 'abc123' is scoped to 'core' but requested path is '/minion/'",
  "component_requested": "minion",
  "key_scope": "core"
}
```

- `code` — machine-readable constant (SCREAMING_SNAKE_CASE) for programmatic handling
- `message` — human-readable description with enough context to diagnose without reading logs
- Additional fields — context-specific key/value pairs that make the error self-contained for support and debugging

Bare `HTTP 401` with no body is acceptable for package manager serving endpoints (RPM/DEB/OCI) since `dnf`/`apt`/`docker` do not parse response bodies on auth failure. The structured error schema applies to the admin API only.

## Configuration Files

- `_bmad/_config/manifest.yaml` — module versions and install dates
- `_bmad/_config/agent-manifest.csv` — all 18 agent definitions
- `_bmad/_config/skill-manifest.csv` — all 79 skill definitions
- `_bmad/bmm/config.yaml` — project name, user name, output paths
- `_bmad/tea/config.yaml`, `_bmad/wds/config.yaml`, `_bmad/bmb/config.yaml` — per-module settings
