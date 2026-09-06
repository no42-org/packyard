# Docs toolchain (Docusaurus). Contributors install Node dependencies into
# ./node_modules via `npm ci`; Node version is pinned in .nvmrc.
NPM ?= npm
GO ?= go

ADMIN_UI_DIR := auth/internal/admin-ui
ADMIN_UI_DIST := auth/internal/adminui/dist

# --warn-undefined-variables surfaces typo'd Makefile vars (e.g. a missing
# underscore in $(ADMIN_UIDIR) that would silently expand to "" and `cd` to
# the project root) instead of letting the broken command run.
MAKEFLAGS += --warn-undefined-variables

UNAME_S := $(shell uname -s)

.DEFAULT_GOAL := help

.PHONY: help docs-install docs-serve docs-build docs-clean check-node \
        admin-ui admin-ui-install admin-ui-dev admin-ui-clean \
        build build-clean test lint lint-workflows build-image-auth build-image-rpm build-images \
        ci-guard ci-stack-up ci-stack-down ci-stack-logs ci-seed ci-verify-env e2e-observability

## help: Show this help
help:
	@sed -n 's/^## //p' $(MAKEFILE_LIST) | column -t -s ':' | sed -e 's/^/ /'

## docs-install: Install the Docusaurus toolchain via npm ci
docs-install: check-node node_modules/.package-lock.json

# node_modules/.package-lock.json is created by `npm ci`/`npm install` and
# rewritten on every install. Using it as the make target for
# node_modules freshness means `docs-serve` / `docs-build` automatically
# re-install when package-lock.json changes, without forcing `npm ci` on
# every invocation. Contributors with a stale tree no longer silently
# run against old deps.
node_modules/.package-lock.json: package-lock.json | check-node
	$(NPM) ci

## docs-serve: Run docusaurus start (live-reload, default port 3000)
docs-serve: node_modules/.package-lock.json
	$(NPM) run start

## docs-build: Build the docs site (onBrokenLinks=throw; fails on broken links / warnings)
docs-build: node_modules/.package-lock.json
	$(NPM) run build

## docs-clean: Remove built docs artefacts and installed Node dependencies
docs-clean:
	rm -rf build/ .docusaurus/ node_modules/

check-node:
	@command -v $(NPM) >/dev/null 2>&1 || { \
	  echo "Error: '$(NPM)' not found."; \
	  echo "       Install Node 20 LTS (see .nvmrc) — e.g. 'nvm install && nvm use'"; \
	  echo "       or 'brew install node@20' / the installer at https://nodejs.org/."; \
	  exit 1; \
	}

## admin-ui-install: Install the admin SPA toolchain via npm ci
admin-ui-install: check-node $(ADMIN_UI_DIR)/node_modules/.package-lock.json

$(ADMIN_UI_DIR)/node_modules/.package-lock.json: $(ADMIN_UI_DIR)/package-lock.json | check-node
	cd $(ADMIN_UI_DIR) && $(NPM) ci

## admin-ui: Build the admin SPA bundle into the Go embed directory
admin-ui: $(ADMIN_UI_DIR)/node_modules/.package-lock.json
	cd $(ADMIN_UI_DIR) && $(NPM) run build
	@rm -rf $(ADMIN_UI_DIST)
	@mkdir -p $(ADMIN_UI_DIST)
	cp -r $(ADMIN_UI_DIR)/dist/. $(ADMIN_UI_DIST)/
	@touch $(ADMIN_UI_DIST)/.gitkeep

## admin-ui-dev: Run the Vite dev server for SPA hot-reload (NOT for end-to-end OAuth — see admin-ui/README.md)
admin-ui-dev: $(ADMIN_UI_DIR)/node_modules/.package-lock.json
	cd $(ADMIN_UI_DIR) && $(NPM) run dev

## admin-ui-clean: Remove the SPA build output and installed Node modules
admin-ui-clean:
	rm -rf $(ADMIN_UI_DIR)/dist $(ADMIN_UI_DIR)/node_modules
	@test -n "$(ADMIN_UI_DIST)" || { echo "ADMIN_UI_DIST is empty — refusing to find -delete"; exit 1; }
	@find $(ADMIN_UI_DIST) -mindepth 1 ! -name .gitkeep -delete

## build: Build the auth binary with the embedded SPA (depends on admin-ui)
build: admin-ui
	cd auth && $(GO) build -o ../bin/auth ./cmd/server

## build-clean: Remove built binaries
build-clean:
	rm -rf bin/

## test: Run the auth service unit tests
test:
	cd auth && $(GO) test ./...

## lint: Check gofmt formatting and run go vet on the auth service
lint:
	@cd auth && unformatted="$$(gofmt -l .)" && { test -z "$$unformatted" || { echo "gofmt: files need formatting:"; echo "$$unformatted"; exit 1; }; }
	cd auth && $(GO) vet ./...

## lint-workflows: Validate YAML embedded in workflow action inputs (paths-filter)
lint-workflows:
	python3 scripts/lint-workflow-filters.py

## build-image-auth: Build the auth container image locally (no push)
build-image-auth:
	docker build ./auth

## build-image-rpm: Build the rpm container image locally (no push)
build-image-rpm:
	docker build ./rpm

## build-images: Build all locally built container images (no push)
build-images: build-image-auth build-image-rpm

# ---------------------------------------------------------------------------
# Integration stack (CI). Every ci-* target refuses to run unless COMPOSE_FILE
# names compose.override.ci.yml and COMPOSE_PROJECT_NAME is set, so a stray
# `make ci-stack-down` can never remove a developer's real stack. A .env (or
# COMPOSE_ENV_FILES) with the required hostnames and credentials must exist.
# ---------------------------------------------------------------------------

ci-guard:
	@case "$${COMPOSE_FILE:-}" in *compose.override.ci.yml*) ;; *) \
	  echo "refusing: COMPOSE_FILE must include compose.override.ci.yml (got '$${COMPOSE_FILE:-}')"; exit 1 ;; esac
	@test -n "$${COMPOSE_PROJECT_NAME:-}" || { echo "refusing: COMPOSE_PROJECT_NAME must be set (e.g. packyard-ci)"; exit 1; }
	@test -n "$${CI_REVISION:-}" || { echo "refusing: CI_REVISION is empty (not a git checkout?); export CI_REVISION=<commit> to build under test"; exit 1; }

# Commit under test; stamped into every locally built image as
# org.opencontainers.image.revision and asserted by ci-verify-env. Evaluated
# once. Suffixed -dirty when tracked files have uncommitted changes, so the
# label never claims a commit the image was not built from. Empty outside a
# git checkout; ci-guard then refuses with a clear message.
ifeq ($(origin CI_REVISION), undefined)
CI_REVISION := $(shell r=$$(git rev-parse HEAD 2>/dev/null) && { git diff --quiet HEAD -- 2>/dev/null && printf '%s' "$$r" || printf '%s-dirty' "$$r"; })
endif
export CI_REVISION

## ci-stack-up: Build the images under test, start the CI compose stack, wait for routing and health
ci-stack-up: ci-guard
	docker compose build
	docker compose up -d
	bash scripts/ci/wait-for-stack.sh

## ci-stack-down: Stop the CI compose stack, remove its volumes and the locally built images
ci-stack-down: ci-guard
	docker compose down -v
	@docker compose config --format json | jq -r '.services[] | select(.build) | .image' \
	  | xargs -r docker image rm -f > /dev/null 2>&1 || true
	rm -f .ci-valid-key

## ci-stack-logs: Dump all CI stack service logs (used on failure)
ci-stack-logs: ci-guard
	docker compose logs --no-color

## ci-seed: Seed an operator session, CI components, an account and a subscription key via the admin API
ci-seed: ci-guard
	bash scripts/ci/seed-integration.sh

## ci-verify-env: Assert PACKYARD_* variables reach the auth container with the expected values, and that compose rejects a missing ADMIN_DOMAIN
ci-verify-env: ci-guard
	bash scripts/ci/verify-env.sh

## e2e-observability: Run the observability end-to-end tests (VALID_KEY from the environment or .ci-valid-key)
e2e-observability: ci-guard
	VALID_KEY="$${VALID_KEY:-$$(cat .ci-valid-key 2>/dev/null)}" \
	BASE_URL="$${BASE_URL:-http://localhost}" METRICS_URL="$${METRICS_URL:-http://localhost:9090/metrics}" \
	bash tests/e2e/observability.sh
