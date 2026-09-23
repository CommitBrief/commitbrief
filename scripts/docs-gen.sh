#!/usr/bin/env bash
# docs-gen.sh — regenerate the code-derived surface inventory (ADR-0039,
# narrowed by ADR-0041).
#
# One write: internal/meta/surface.json — the code-derived CLI surface
# inventory (providers, commands, flags, config keys, MCP review tool args),
# rebuilt from the CURRENT binary via the hidden --gen-surface flag.
#
# README.md is no longer generated from this artifact (ADR-0041): it is a
# hand-written front door, and the Markdown-rendering engine that used to
# splice surface.json into README.md's `<!-- commitbrief:gen NAME -->` …
# `<!-- commitbrief:end NAME -->` regions (internal/meta/blocks.go, docs.go)
# was deleted, not weakened. commitbrief.com is the documentation source of
# truth going forward.
#
# Invoked by `make docs-gen`, by `go generate ./internal/meta` (see the
# //go:generate directive in internal/meta/doc.go), and manually after any
# change to a provider's metadata, a command/flag, a config key, or the MCP
# review tool's input schema. `scripts/docs-check.sh` is the read-only
# counterpart that fails CI instead of rewriting anything.

set -euo pipefail

cd "$(dirname "$0")/.."

SURFACE=internal/meta/surface.json

echo "==> regenerating $SURFACE"
go run ./cmd/commitbrief --gen-surface "$SURFACE"

echo "docs-gen: done"
