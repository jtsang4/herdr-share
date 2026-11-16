SHELL := /bin/sh

# The pnpm version pinned in web/package.json, run through npx so that no
# global pnpm (or a working self-switch) is required.
PNPM    ?= npx -y $(shell node -p "require('./web/package.json').packageManager")
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# Pinned to the version CI uses; built with the project's Go toolchain.
GOLANGCI_LINT ?= go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0

PKG     := github.com/jtsang4/herdr-share/internal/version
LDFLAGS := -s -w -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: web-deps
web-deps: ## Install web dependencies
	cd web && $(PNPM) install --frozen-lockfile

.PHONY: web
web: web-deps ## Build the guest web client into web/build/app
	cd web && $(PNPM) run build

.PHONY: build
build: ## Build herdr-share and herdr-share-relay into ./bin (run `make web` first to embed the client)
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/ ./cmd/herdr-share ./cmd/herdr-share-relay

.PHONY: test
test: ## Run Go unit tests and web checks and tests
	go test ./...
	cd web && $(PNPM) run check && $(PNPM) run test

.PHONY: e2e
e2e: build ## Run end-to-end tests against an isolated herdr-core, relay and browser
	scripts/e2e.sh

.PHONY: lint
lint: ## Run linters
	$(GOLANGCI_LINT) run ./...
	cd web && $(PNPM) run lint

.PHONY: check
check: lint test ## Run the checks CI runs on every change (lint and tests)

.PHONY: hooks
hooks: ## Install git pre-commit hooks (lefthook) that check staged files
	go run github.com/evilmartians/lefthook/v2@v2.2.2 install

.PHONY: fmt
fmt: ## Format Go and web sources
	$(GOLANGCI_LINT) fmt ./...
	cd web && $(PNPM) run format

.PHONY: dev-relay
dev-relay: ## Run a relay locally on :8080
	go run ./cmd/herdr-share-relay

.PHONY: dev-web
dev-web: ## Run the web dev server (proxies the relay API on :8080)
	cd web && $(PNPM) run dev

.PHONY: install
install: build ## Install herdr-share into ~/.local/bin and the Herdr mod
	install -d $(HOME)/.local/bin
	install bin/herdr-share $(HOME)/.local/bin/herdr-share
	$(HOME)/.local/bin/herdr-share mod install

.PHONY: snapshot
snapshot: web ## Build a local release snapshot with GoReleaser
	goreleaser release --snapshot --clean

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin dist web/build/app web/.svelte-kit
