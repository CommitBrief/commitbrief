#!/usr/bin/env bash
# spdx-check.sh — fail if any Go source file is missing the per-file SPDX
# license header. Invoked by `make spdx-check` (folded into `make check`)
# and by .github/workflows/ci.yml. Keeps the 100% header coverage reached
# in v0.9.0 from regressing as new files land. See ADR-0012 (§"SPDX header
# status" + "A CI guard that fails on a new source file missing the
# header...").
#
# Scope: Go sources only (cmd/ + internal/ + any tracked *.go). ADR-0012
# asserts the header on every Go source; shell/YAML/Markdown are out of
# scope here (their licensing rides the repo LICENSE file).
#
# The header may sit on line 1 or just below a build-constraint block, so
# we scan the first few lines rather than requiring line 1 exactly.

set -euo pipefail

cd "$(dirname "$0")/.."

readonly HEADER='SPDX-License-Identifier: GPL-3.0-or-later'
readonly SCAN_LINES=5

# Prefer git for the file list; fall back to find when run outside a
# checkout. `--cached --others --exclude-standard` is deliberate: it lists
# tracked files AND brand-new untracked (non-ignored) ones, so a freshly
# added source file is caught locally before it is even committed — which
# is the whole point of this guard.
if git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  mapfile -t files < <(git ls-files --cached --others --exclude-standard '*.go')
else
  mapfile -t files < <(find . -name '*.go' -not -path './dist/*' -not -path './vendor/*')
fi

missing=()
for f in "${files[@]}"; do
  [ -f "$f" ] || continue
  if ! head -n "$SCAN_LINES" "$f" | grep -q "$HEADER"; then
    missing+=("$f")
  fi
done

if [ "${#missing[@]}" -gt 0 ]; then
  printf '\033[31mfail\033[0m: %d Go file(s) missing the SPDX header (%s):\n' \
    "${#missing[@]}" "$HEADER" >&2
  for f in "${missing[@]}"; do
    printf '  %s\n' "$f" >&2
  done
  printf '\nAdd this as the first line of each file:\n  // %s\n' "$HEADER" >&2
  exit 1
fi

printf '\033[32mok\033[0m:   all %d Go source files carry the SPDX header\n' "${#files[@]}"
