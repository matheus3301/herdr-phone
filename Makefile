# herdr-phone developer tasks.
# `make check` runs every local gate that needs no network or credentials.
#
# herdr-phone is a Go 1.26 relay with a React/TypeScript PWA embedded into the
# binary. Backend targets use the Go toolchain; frontend targets run in ./web
# against the committed package-lock.json. `make build-web` builds the frontend
# and syncs it into the Go embed directory (internal/webui/generated); `make
# build` then asserts the assets are embedded before compiling.

GO ?= go
NPM ?= npm
# Go packages under test: the command entrypoint and the internal packages only.
# This excludes ./web (no Go, plus npm node_modules) entirely.
GO_PKGS := ./cmd/... ./internal/...
# Coverage is measured over the internal packages, where the real logic lives;
# the cmd entrypoint is a trivial main and is excluded so it does not dilute the
# threshold.
COVER_PKGS := ./internal/...
COVERAGE_MIN ?= 80
COVERAGE_FILE := coverage.txt
BIN := bin/herdr-phone
CMD := ./cmd/herdr-phone
WEB := web

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

# ---- Go: format, tidy, vet ----

.PHONY: fmt
fmt: ## Format all Go sources
	$(GO) fmt $(GO_PKGS)

.PHONY: fmt-check
fmt-check: ## Fail if any Go source is not gofmt-clean
	@out="$$(gofmt -l $$(find . -name '*.go' -not -path './vendor/*'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	@echo "gofmt: clean"

.PHONY: tidy
tidy: ## Run go mod tidy
	$(GO) mod tidy

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy (restores originals via trap)
	@set -e; \
	cp go.mod go.mod.bak; cp go.sum go.sum.bak; \
	trap 'mv -f go.mod.bak go.mod; mv -f go.sum.bak go.sum' EXIT INT TERM; \
	if ! $(GO) mod tidy; then echo "go mod tidy failed"; exit 1; fi; \
	if ! cmp -s go.mod go.mod.bak || ! cmp -s go.sum go.sum.bak; then \
		echo "go.mod/go.sum are not tidy; run 'make tidy'"; exit 1; fi; \
	echo "go mod tidy: clean"

.PHONY: vet
vet: ## Run go vet
	$(GO) vet $(GO_PKGS)

# ---- Frontend (./web) ----

.PHONY: web-install
web-install: ## Install frontend dependencies from the committed lockfile
	cd $(WEB) && $(NPM) ci

.PHONY: lint
lint: vet web-install ## Run go vet and the frontend linter
	cd $(WEB) && $(NPM) run lint

.PHONY: typecheck
typecheck: web-install ## TypeScript typecheck of the frontend (tsc --noEmit)
	cd $(WEB) && $(NPM) run typecheck

.PHONY: test-web
test-web: web-install ## Run frontend unit and component tests
	cd $(WEB) && $(NPM) test

.PHONY: test-e2e
test-e2e: web-install ## Run Playwright mobile journeys (Chromium Pixel 7, WebKit iPhone 15)
	cd $(WEB) && $(NPM) run test:e2e

.PHONY: build-web
build-web: web-install ## Build the frontend and sync it into the Go embed dir
	cd $(WEB) && $(NPM) run build
	sh scripts/embed-web.sh

# ---- Go: test, coverage, build ----

.PHONY: test
test: ## Run Go tests
	$(GO) test $(GO_PKGS)

.PHONY: test-race
test-race: ## Run Go tests with the race detector
	$(GO) test -race $(GO_PKGS)

.PHONY: coverage
coverage: ## Run Go tests with coverage over ./internal/... and enforce the threshold
	$(GO) test -covermode=atomic -coverprofile=$(COVERAGE_FILE) $(COVER_PKGS)
	@total=$$($(GO) tool cover -func=$(COVERAGE_FILE) | awk '/^total:/ {print $$3}' | tr -d '%'); \
	echo "total coverage: $$total% (min $(COVERAGE_MIN)%)"; \
	awk "BEGIN{exit !($$total+0 >= $(COVERAGE_MIN))}" || \
		{ echo "coverage $$total% is below the $(COVERAGE_MIN)% threshold"; exit 1; }

.PHONY: build
build: build-web ## Build ./bin/herdr-phone with the frontend embedded
	# Fail closed if the production frontend was not embedded into
	# internal/webui/generated before compiling.
	HERDR_PHONE_REQUIRE_WEB=1 $(GO) test ./internal/webui
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN) $(CMD)

# ---- Plugin verification and install smoke ----

.PHONY: verify-plugin
verify-plugin: ## Validate the manifest against Herdr in isolated state
	sh scripts/verify-plugin.sh

.PHONY: smoke-install
smoke-install: ## Smoke-test the offline install fallback with local fake release assets
	sh scripts/smoke-install.sh

.PHONY: smoke-embed
smoke-embed: ## Smoke-test the frontend embed sync/clean in an isolated temp dir
	sh scripts/embed-web.sh --smoke

.PHONY: sh-syntax
sh-syntax: ## Check POSIX shell syntax of the scripts
	@for f in scripts/*.sh; do sh -n "$$f" && echo "sh -n ok: $$f"; done

# ---- Aggregate gate ----

.PHONY: check
check: fmt-check tidy-check vet typecheck lint test-race coverage test-web build sh-syntax smoke-embed smoke-install ## Run all local quality gates
	@echo "all checks passed"

.PHONY: clean
clean: ## Remove build, coverage, and frontend artifacts
	rm -rf bin dist $(COVERAGE_FILE) coverage.html
	rm -rf $(WEB)/dist $(WEB)/node_modules $(WEB)/test-results $(WEB)/playwright-report
	# Restore the embed directory to its committed marker-only state.
	sh scripts/embed-web.sh --clean
