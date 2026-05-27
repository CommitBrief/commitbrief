#!/usr/bin/env bash
# i18n-deadkey-check.sh — flag i18n catalog keys that no Go source
# refers to. UC-25 in PATCH_ROADMAP: dead keys accumulate every time
# a CLI surface changes and the translator forgets to delete the
# obsoleted line, which makes the next translator's diff noisy and
# hides real drift. The check is cheap enough to run in CI on every
# push.
#
# Conventions:
#   - en is the source of truth; we scan its keys, then ensure each
#     one is referenced by at least one *.go file under internal/ or
#     cmd/. tr is held to parity by release-check.sh, so a dead key
#     in en automatically means a dead key in tr.
#   - Skip lines that are not key/value pairs (comments, blanks).
#   - Treat the key as "referenced" if any *.go file mentions the
#     literal string with surrounding quotes — handles every static
#     call site (`Catalog.T("...")`) and is safe against false
#     positives in struct-tag-like syntax (keys are dotted, struct
#     tags rarely contain dots).

set -euo pipefail
cd "$(dirname "$0")/.."

CATALOG="internal/i18n/messages.en.yml"
if [ ! -f "$CATALOG" ]; then
  printf 'i18n-deadkey-check: %s missing\n' "$CATALOG" >&2
  exit 1
fi

fail=0
while IFS= read -r line; do
  case "$line" in
    ''|\#*) continue ;;
  esac
  key="${line%%:*}"
  case "$key" in
    *.*) ;;
    *) continue ;;
  esac

  if ! grep -rq --include='*.go' "\"$key\"" internal/ cmd/ 2>/dev/null; then
    printf '\033[31mdead key\033[0m: %s — no Go reference found\n' "$key" >&2
    fail=1
  fi
done < "$CATALOG"

if [ "$fail" -eq 0 ]; then
  printf '\033[32mok\033[0m:   no dead i18n keys in %s\n' "$CATALOG"
fi
exit "$fail"
