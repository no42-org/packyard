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
        ci-stack-up ci-stack-down ci-stack-logs ci-seed ci-verify-env e2e-observability

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
# Integration stack (CI). Callers set COMPOSE_PROJECT_NAME and COMPOSE_FILE
# (compose.yml:compose.override.ci.yml) in the environment; a .env with the
# required hostnames and credentials must exist in the working directory.
# ---------------------------------------------------------------------------

## ci-stack-up: Start the compose stack and wait for Traefik and auth to be ready
ci-stack-up:
	docker compose up -d
	bash scripts/ci/wait-for-stack.sh

## ci-stack-down: Stop the compose stack and remove its volumes
ci-stack-down:
	docker compose down -v

## ci-stack-logs: Dump all service logs (used on failure)
ci-stack-logs:
	docker compose logs --no-color

## ci-seed: Seed an operator session, CI components and a subscription key via the admin API
ci-seed:
	bash scripts/ci/seed-integration.sh

## ci-verify-env: Assert PACKYARD_* variables reach the auth container and the ADMIN_DOMAIN guard fires when missing
ci-verify-env:
	@cid=$$(docker compose ps -q auth); test -n "$$cid" || { echo "auth container not running"; exit 1; }; \
	for v in PACKYARD_ADMIN_HOST PACKYARD_BOOTSTRAP_OPERATOR_EMAIL PACKYARD_PUBLIC_COMPONENT_CACHE_TTL PACKYARD_GITHUB_CLIENT_ID; do \
	  docker inspect --format '{{range .Config.Env}}{{println .}}{{end}}' "$$cid" | grep -q "^$$v=" \
	    || { echo "missing $$v in auth container environment"; exit 1; }; \
	done; echo "auth container receives the forwarded PACKYARD_* variables"
	@tmp=$$(mktemp); grep -v '^ADMIN_DOMAIN=' "$${COMPOSE_ENV_FILES:-.env}" > "$$tmp"; \
	if docker compose --env-file "$$tmp" config > /dev/null 2> "$$tmp.err"; then \
	  echo "expected compose to reject a missing ADMIN_DOMAIN"; rm -f "$$tmp" "$$tmp.err"; exit 1; fi; \
	grep -q 'ADMIN_DOMAIN must be set' "$$tmp.err" || { echo "unexpected error:"; cat "$$tmp.err"; rm -f "$$tmp" "$$tmp.err"; exit 1; }; \
	rm -f "$$tmp" "$$tmp.err"; echo "compose guard rejects a missing ADMIN_DOMAIN as expected"

## e2e-observability: Run the observability end-to-end tests against the running stack (needs VALID_KEY)
e2e-observability:
	BASE_URL=$${BASE_URL:-http://localhost} METRICS_URL=$${METRICS_URL:-http://localhost:9090/metrics} bash tests/e2e/observability.sh
