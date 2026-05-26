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

.PHONY: help build test test-live lint fmt tidy clean release-check license-check manpage smoke

help: ## Show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Compile the commitbrief binary into ./$(BIN)
	$(GO) build -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/commitbrief

test: ## Run unit + integration tests (live provider tests excluded)
	$(GO) test ./...

test-live: ## Run live provider tests (real API keys required)
	$(GO) test -tags=live ./...

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

manpage: ## Regenerate man/commitbrief.1
	bash scripts/manpage.sh

smoke: ## Build + walk the pipeline end-to-end without an API call
	bash scripts/smoke-test.sh
