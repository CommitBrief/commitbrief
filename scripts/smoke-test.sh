#!/usr/bin/env bash
# smoke-test.sh — exercise the CommitBrief binary end-to-end without
# touching a live LLM. Builds the binary, creates a throwaway git repo
# with a small diff staged, then walks through `--version`, `list`,
# `init`, and `dry-run` to confirm the pipeline assembles a prompt and
# computes a cache key. A real review (with API call) is documented in
# README/CHANGELOG and is the user's responsibility.

set -euo pipefail

cd "$(dirname "$0")/.."

BIN="./commitbrief"
ok()   { printf '\033[32mok\033[0m:    %s\n' "$1"; }
step() { printf '\033[36mstep\033[0m:  %s\n' "$1"; }
fail() { printf '\033[31mfail\033[0m:  %s\n' "$1" >&2; exit 1; }

# 1. Build
step "make build"
make build >/dev/null
[ -x "$BIN" ] || fail "binary not produced at $BIN"
ok "binary built at $BIN"

# 2. Version + help smoke
step "binary --version"
"$BIN" --version | grep -q "commitbrief" || fail "--version missing 'commitbrief'"
ok "--version output sane"

step "binary --help"
"$BIN" --help | grep -q "Available Commands" || fail "--help missing 'Available Commands'"
ok "--help renders"

step "binary list"
"$BIN" list | grep -q "Review (default)" || fail "list output unexpected"
ok "list renders command reference"

# 3. Pipeline smoke against a throwaway git repo
TMPDIR=$(mktemp -d 2>/dev/null || mktemp -d -t cb-smoke)
trap 'rm -rf "$TMPDIR"' EXIT

step "init throwaway repo at $TMPDIR"
(
  cd "$TMPDIR"
  git init -q -b main
  git -c user.email=smoke@test git -c user.name=smoke -c init.defaultBranch=main >/dev/null 2>&1 || true
  git config user.email smoke@test
  git config user.name smoke
  cat > app.go <<'EOF'
package app

func Login(user, pw string) error {
  return nil
}
EOF
  git add app.go
  git commit -q -m "initial"
  # Stage a change so --staged has something to review
  cat > app.go <<'EOF'
package app

import "errors"

func Login(user, pw string) error {
  if user == "" {
    return errors.New("empty user")
  }
  return nil
}
EOF
  git add app.go
)
ok "throwaway repo + staged change ready"

# 4. init — writes COMMITBRIEF.md (team-shared) and .commitbrief/OUTPUT.md
#    (per-user, gitignored) from embedded defaults.
step "commitbrief init"
(cd "$TMPDIR" && "$OLDPWD/$BIN" init -y >/dev/null)
[ -s "$TMPDIR/COMMITBRIEF.md" ] || fail "init did not write COMMITBRIEF.md"
[ -s "$TMPDIR/.commitbrief/OUTPUT.md" ] || fail "init did not write .commitbrief/OUTPUT.md"
ok "init wrote COMMITBRIEF.md ($(wc -c <"$TMPDIR/COMMITBRIEF.md") bytes) + .commitbrief/OUTPUT.md ($(wc -c <"$TMPDIR/.commitbrief/OUTPUT.md") bytes)"

# 5. dry-run — full pipeline without provider call
step "commitbrief dry-run --staged"
out=$(cd "$TMPDIR" && "$OLDPWD/$BIN" dry-run --staged)
echo "$out" | grep -q "Origin:" || fail "dry-run missing Origin line"
echo "$out" | grep -q "Cache key:" || fail "dry-run missing Cache key line"
echo "$out" | grep -q "Est. tokens:" || fail "dry-run missing token estimate"
echo "$out" | grep -q "staged" || fail "dry-run did not detect staged origin"
ok "dry-run pipeline produced a prompt + cache key"

# 6. Re-run dry-run after editing rules — cache key must change
step "edit rules and verify cache key changes"
(cd "$TMPDIR" && echo "Always also flag missing context.Context parameters." >>COMMITBRIEF.md)
out2=$(cd "$TMPDIR" && "$OLDPWD/$BIN" dry-run --staged)
key1=$(echo "$out" | awk '/Cache key:/ {print $3}')
key2=$(echo "$out2" | awk '/Cache key:/ {print $3}')
[ "$key1" != "$key2" ] || fail "cache key did not invalidate after COMMITBRIEF.md edit"
ok "cache key invalidated after rules change (was $key1 → $key2)"

# 7. .commitbriefignore — stage a file that the built-in layer filters
#    (go.sum), confirm it's excluded; then add a negative pattern to
#    .commitbriefignore and confirm it gets reviewed.
step ".commitbriefignore exclusion + negative-pattern revert"
(
  cd "$TMPDIR"
  cat > go.sum <<'EOF'
example.com/foo v1.0.0/go.mod h1:abc
EOF
  git add go.sum
)
out3=$(cd "$TMPDIR" && "$OLDPWD/$BIN" dry-run --staged)
builtin_filtered=$(echo "$out3" | awk '/built-in ignore filtered:/ {print $NF}')
[ "$builtin_filtered" -ge 1 ] || fail "built-in layer did not catch go.sum (filtered=$builtin_filtered)"
ok "built-in layer filtered $builtin_filtered file(s) including go.sum"

(cd "$TMPDIR" && echo '!go.sum' > .commitbriefignore)
out4=$(cd "$TMPDIR" && "$OLDPWD/$BIN" dry-run --staged)
builtin_after=$(echo "$out4" | awk '/built-in ignore filtered:/ {print $NF}')
repo_net=$(echo "$out4" | awk '/.commitbriefignore net filtered:/ {print $NF}')
# Negative pattern: built-in still reports the match, but repo layer reverts it
# (net effect: one file un-filtered).
[ "$repo_net" -lt 0 ] || fail "negative pattern did not revert built-in (repo_net=$repo_net)"
ok "!go.sum reverted built-in exclusion (repo net = $repo_net)"

echo
ok "smoke-test: all checks passed"
echo
echo "Next step (manual, requires real Anthropic API key):"
echo "  1. ./commitbrief setup        # enter your sk-ant-... key"
echo "  2. cd /path/to/test/repo && git add somefile.go"
echo "  3. ./commitbrief --staged     # should produce a review"
