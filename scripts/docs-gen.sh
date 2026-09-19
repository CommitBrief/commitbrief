#!/usr/bin/env bash
# docs-gen.sh — regenerate the generated-docs artifacts (ADR-0039).
#
# Two writes, always in this order, because the second reads the first:
#
#   1. internal/meta/surface.json — the code-derived CLI surface inventory
#      (providers, commands, flags, config keys, MCP review tool args),
#      rebuilt from the CURRENT binary via the hidden --gen-surface flag.
#   2. README.md's four `<!-- commitbrief:gen NAME -->` … `<!-- commitbrief:end
#      NAME -->` regions, rewritten from the surface.json just written in
#      step 1 (internal/meta's TestDocsInSync, run with -update).
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

echo "==> regenerating README.md's generated regions"
go test ./internal/meta -run '^TestDocsInSync$' -update

echo "docs-gen: done"
