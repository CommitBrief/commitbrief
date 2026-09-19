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
#      Layer 2 does NOT trust `go test`'s exit code alone: `go test -run
#      '<pattern>'` exits 0 with "ok ... [no tests to run]" when the
#      pattern matches nothing at all — a renamed, skipped, or deleted
#      TestDocsInSync would make this script report success having
#      verified nothing (review turn 1, P1). So layer 2 additionally greps
#      the captured `-v` output for the literal "--- PASS: TestDocsInSync ("
#      line before calling it a pass, and tells a genuine README/renderer
#      mismatch (an explicit "--- FAIL: TestDocsInSync" line) apart from
#      everything else that can make the exit code non-zero or the PASS
#      line absent — a build break, a missing `go` toolchain, a panic, or
#      the test having vanished/renamed/skipped underneath us (P3): only
#      the first gets the "-update" suggestion, because it is the only
#      case that actually means "regenerate the docs".
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
# INT/TERM too, not just EXIT (P4): otherwise Ctrl-C during the `go run`
# below leaves a stray tmp.XXXX file behind in $TMPDIR.
trap 'rm -f "$tmp_surface"' EXIT INT TERM

# Keep stdout out of the way but capture stderr, so a build break in the
# code `--gen-surface` walks (provider metadata, cobra tree, MCP schema)
# still names its file and line instead of being swallowed whole (P2).
if gen_err=$(go run ./cmd/commitbrief --gen-surface "$tmp_surface" 2>&1 1>/dev/null); then
  gen_status=0
else
  gen_status=$?
fi

if [ "$gen_status" -ne 0 ]; then
  err "go run ./cmd/commitbrief --gen-surface failed to rebuild the surface inventory from the current code"
  [ -n "$gen_err" ] && printf '%s\n' "$gen_err" >&2
elif [ ! -f "$SURFACE" ]; then
  # Distinct from "stale" (P4): a missing committed artifact is not one
  # that disagrees with the code, it simply isn't there yet.
  err "$SURFACE does not exist"
  printf 'fix: go run ./cmd/commitbrief --gen-surface %s   (or: make docs-gen)\n' "$SURFACE" >&2
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

if [ "$docs_sync_status" -eq 0 ] && printf '%s\n' "$docs_sync_output" | grep -q -- '--- PASS: TestDocsInSync ('; then
  ok "README.md's generated regions match $SURFACE (TestDocsInSync)"
elif printf '%s\n' "$docs_sync_output" | grep -q -- '--- FAIL: TestDocsInSync'; then
  err "TestDocsInSync failed: README.md's generated regions do not match $SURFACE (see output above)."
  printf 'fix: go test ./internal/meta -run TestDocsInSync -update   (or: make docs-gen), then review git diff README.md\n' >&2
else
  # Anything else that can make the exit code non-zero, or make the PASS
  # line above simply never appear, WITHOUT ever running an assertion:
  # go/the package failed to build, the go toolchain is missing, the test
  # panicked, or TestDocsInSync itself was renamed/skipped/deleted (the
  # exact "no tests to run" false-green this check exists to catch, P1).
  # None of these mean the docs are stale, so this branch deliberately
  # does not suggest `-update`.
  err "could not confirm TestDocsInSync ran and passed (see output above) — this is a build/toolchain/test-selection problem, not necessarily README drift."
  printf 'fix: run `go test ./internal/meta -run TestDocsInSync -v` yourself and read the failure before assuming the docs are stale\n' >&2
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "docs-check: BLOCKED" >&2
  exit 1
fi

echo
echo "docs-check: ok"
