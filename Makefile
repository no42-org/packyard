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
        build build-clean

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
