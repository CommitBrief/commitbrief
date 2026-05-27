SHELL := /usr/bin/env bash

BIN     := commitbrief
PKG     := github.com/CommitBrief/commitbrief
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  := $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG)/internal/version.Version=$(VERSION) \
	-X $(PKG)/internal/version.Commit=$(COMMIT) \
	-X $(PKG)/internal/version.Date=$(DATE)

GO ?= go

.PHONY: help build test test-live bench lint fmt tidy clean check release-check license-check i18n-check manpage smoke

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Compile the commitbrief binary into ./$(BIN)
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/commitbrief

test: ## Run unit + integration tests (live provider tests excluded)
	$(GO) test ./...

test-live: ## Run live provider tests (real API keys required)
	$(GO) test -tags=live ./...

bench: ## Run local-pipeline + cache benchmarks (PRD §7.1 targets)
	$(GO) test -bench=. -benchmem -run=^$$ ./internal/diff ./internal/cache

lint: ## Run golangci-lint
	golangci-lint run

fmt: ## Format source with gofmt (+ goimports if available)
	gofmt -s -w .
	@if command -v goimports >/dev/null 2>&1; then \
		goimports -w -local $(PKG) . ; \
	else \
		echo "goimports not installed (go install golang.org/x/tools/cmd/goimports@latest)"; \
	fi

tidy: ## Sync go.mod / go.sum
	$(GO) mod tidy

clean: ## Remove build artifacts
	rm -f $(BIN) $(BIN).exe
	rm -rf dist/

release-check: ## Run pre-release safety checks (scripts/release-check.sh)
	bash scripts/release-check.sh

license-check: ## Audit dependency licenses for GPL-3.0 compatibility
	bash scripts/license-check.sh

i18n-check: ## Flag i18n catalog keys with no Go source reference (UC-25)
	bash scripts/i18n-deadkey-check.sh

check: ## Run every guard CI runs (fmt-drift, vet, lint, test, release-check, i18n-check)
	@echo "==> gofmt drift"
	@drift=$$(gofmt -l -s . | grep -v '^vendor/' || true); \
	if [ -n "$$drift" ]; then \
		echo "gofmt: needs '-s -w' on:"; echo "$$drift"; exit 1; \
	fi
	@echo "==> go vet"
	@$(GO) vet ./...
	@echo "==> golangci-lint"
	@$(MAKE) -s lint
	@echo "==> go test"
	@$(GO) test ./...
	@echo "==> release-check"
	@$(MAKE) -s release-check
	@echo "==> i18n-check"
	@$(MAKE) -s i18n-check
	@echo "==> all checks passed"

manpage: ## Regenerate man/commitbrief.1
	bash scripts/manpage.sh

smoke: ## Build + walk the pipeline end-to-end without an API call
	bash scripts/smoke-test.sh
