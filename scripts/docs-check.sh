#!/usr/bin/env bash
# docs-check.sh — CI/local drift gate for the code-derived surface inventory
# (ADR-0039, narrowed by ADR-0041), plus a leak-lint pass over README.md
# (ADR-0041 §4). Invoked by `make docs-check` (folded into `make check`) and
# by the `docs-check` job in .github/workflows/ci.yml.
#
# Layer 1: code -> internal/meta/surface.json (checked HERE, not by `go
# test`). Rebuilds the surface inventory from the CURRENT code into a scratch
# file and diffs it against the committed artifact. This is the layer that
# closes the actual gap this phase exists for: change a provider's
# DefaultModel (or any other code the inventory reads) and forget to
# regenerate, and `go test ./...` alone would stay green — nothing else reads
# surface.json off disk and re-derives it from code. Without this diff, a
# stale committed surface.json would look "in sync" forever.
#
# What this script no longer does (ADR-0041): README.md is a hand-written
# front door, not a generated artifact, so there is no second
# surface.json -> README.md layer here anymore. The Markdown-rendering
# engine that used to own that layer (internal/meta/blocks.go, docs.go, and
# their TestDocsInSync/TestRegions*/TestApply*/TestVerify* tests) was deleted
# outright, not weakened — see internal/meta/surface_test.go for what moved
# forward (TestEnvVarsCoverApplyEnv, TestMCPToolArgsRowCountMatchesRealSchema)
# and .ssot/architecture/adr/0041-documentation-source-of-truth.md §3 for the
# full "moved, not deleted" list.
#
# Layer 2 (ADR-0041 §4): README.md itself IS checked here — for internal
# maintainer traces (ADR/OQ/D-NN ids) and prose version traces ("Since
# v1.17.0", "(v1.7.0)"). See the "README.md: no internal traces" section
# below.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
err() { printf '\033[31mfail\033[0m: %s\n' "$1" >&2; fail=1; }
ok()  { printf '\033[32mok\033[0m:   %s\n' "$1"; }

SURFACE=internal/meta/surface.json

# --- code -> surface.json -------------------------------------------------

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

# --- README.md: no internal traces (ADR-0041 §4) --------------------------
#
# Published text must not carry maintainer-only identifiers (ADR/OQ/D-NN) or
# prose version traces ("Since v1.17.0", "(v1.7.0)", "v0.9.0+"). Same two
# rules and the same allowed phrase as commitbrief.com/scripts/lint-leaks.mjs
# — kept in sync by hand, since this is a standalone awk+sed+grep pass here,
# not a shared library.

R1='\bADR-[0-9]{4}\b|\bOQ-[0-9]+\b|\bD-[0-9]{2}\b'
R2='(^|[^A-Za-z0-9/._])v[0-9]+\.[0-9]+'
STRIP='s/`[^`]*`//g; s/\]\([^)]*\)/]/g; s/(\*\*)?Requires (CLI )?v[0-9]+\.[0-9]+\.[0-9]+ or (later|newer)(\*\*)?//g'

# Rule 1: whole file, including fenced code.
r1_hits=$(grep -nE "$R1" README.md || true)
if [ -n "$r1_hits" ]; then
  err "README.md carries a maintainer identifier (ADR/OQ/D-NN) — internal trace in published text (ADR-0041 §4)"
  printf '%s\n' "$r1_hits" >&2
fi

# Rule 2: prose only — skip fenced code blocks, then strip inline code spans,
# link/image targets, and the one allowed "Requires vX.Y.Z or later" phrase
# before testing. CommonMark lets an inline code span (or, in principle, the
# allowed phrase) wrap across a line break, so stripping strictly line by
# line can miss (or misfire on) a span that crosses that boundary. To stay
# correct without a multi-line-aware regex engine, each paragraph (the run
# of non-blank, non-fenced, non-frontmatter lines up to the next blank line)
# is joined onto one record — tagged with its first line number — before
# STRIP and the rule 2 test run.
r2_hits=$(awk '
  BEGIN { in_fence = 0; buf = ""; startline = 0 }
  function flush() { if (buf != "") { print startline "\t" buf; buf = ""; startline = 0 } }
  /^[[:space:]]*(```|~~~)/ { in_fence = !in_fence; flush(); next }
  in_fence { flush(); next }
  /^[[:space:]]*$/ { flush(); next }
  { if (buf == "") startline = FNR; buf = (buf == "") ? $0 : buf " " $0 }
  END { flush() }
' README.md \
  | sed -E "$STRIP" \
  | sed -E 's/\t/: /' \
  | grep -E "$R2" || true)
if [ -n "$r2_hits" ]; then
  err "README.md carries a prose version trace — internal trace in published text (ADR-0041 §4)"
  printf '%s\n' "$r2_hits" >&2
fi

if [ -z "$r1_hits" ] && [ -z "$r2_hits" ]; then
  ok "README.md has no internal traces (ADR/OQ/D-NN ids or version traces)"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "docs-check: BLOCKED" >&2
  exit 1
fi

echo
echo "docs-check: ok"
