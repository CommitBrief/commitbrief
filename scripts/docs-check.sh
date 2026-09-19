#!/usr/bin/env bash
# docs-check.sh — CI/local drift gate for the generated-docs pipeline
# (ADR-0039). Invoked by `make docs-check` (folded into `make check`) and
# by the `docs-check` job in .github/workflows/ci.yml.
#
# Two independent layers — one alone is not a gate:
#
#   1. code -> internal/meta/surface.json (checked HERE, not by `go test`).
#      Rebuilds the surface inventory from the CURRENT code into a scratch
#      file and diffs it against the committed artifact. This is the layer
#      that closes the actual gap this phase exists for: change a
#      provider's DefaultModel (or any other code the inventory reads) and
#      forget to regenerate, and `go test ./...` alone stays green —
#      internal/meta's TestDocsInSync loads surface.json off disk rather
#      than rebuilding it (that is deliberate: see internal/meta/doc.go's
#      "No build stamp" section — the artifact is committed so it must not
#      embed anything that churns on every build). Without this diff, a
#      stale committed surface.json and a doc that faithfully matches it
#      would both look "in sync" forever.
#   2. internal/meta/surface.json -> README.md, PLUS "every renderer has a
#      region" completeness (missing/misspelled/indented marker). Both are
#      already covered by internal/meta's TestDocsInSync (Verify +
#      missingRenderers) as part of the normal `go test ./...` job on all
#      three OSes — re-run just that one test here so `make docs-check` /
#      the `docs-check` CI job is a complete gate on its own, not only in
#      combination with the full test suite.
#
# Accumulate-and-continue: both layers run even if the first fails, so a
# single invocation reports everything wrong at once.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
err() { printf '\033[31mfail\033[0m: %s\n' "$1" >&2; fail=1; }
ok()  { printf '\033[32mok\033[0m:   %s\n' "$1"; }

SURFACE=internal/meta/surface.json

# --- 1. code -> surface.json --------------------------------------------

tmp_surface=$(mktemp 2>/dev/null || mktemp -t cb-docs-check)
trap 'rm -f "$tmp_surface"' EXIT

if ! go run ./cmd/commitbrief --gen-surface "$tmp_surface" >/dev/null 2>&1; then
  err "go run ./cmd/commitbrief --gen-surface failed to rebuild the surface inventory from the current code"
else
  diff_output=$(diff -u "$SURFACE" "$tmp_surface" 2>&1 || true)
  if [ -n "$diff_output" ]; then
    err "$SURFACE is stale: it does not match a surface inventory freshly built from the current code."
    printf '%s\n' "$diff_output" >&2
    printf 'fix: go run ./cmd/commitbrief --gen-surface %s   (or: make docs-gen)\n' "$SURFACE" >&2
  else
    ok "$SURFACE matches a freshly built surface inventory"
  fi
fi

# --- 2. surface.json -> README.md (+ renderer/region completeness) -----

docs_sync_status=0
docs_sync_output=$(go test ./internal/meta -run '^TestDocsInSync$' -v 2>&1) || docs_sync_status=$?
printf '%s\n' "$docs_sync_output"

if [ "$docs_sync_status" -ne 0 ]; then
  err "TestDocsInSync failed: README.md's generated regions do not match $SURFACE (see output above)."
  printf 'fix: go test ./internal/meta -run TestDocsInSync -update   (or: make docs-gen), then review git diff README.md\n' >&2
else
  ok "README.md's generated regions match $SURFACE (TestDocsInSync)"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "docs-check: BLOCKED" >&2
  exit 1
fi

echo
echo "docs-check: ok"
