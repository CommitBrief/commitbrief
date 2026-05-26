#!/usr/bin/env bash
# release-check.sh — pre-release safety checks for CommitBrief.
# Invoked by `make release-check` and by .github/workflows/release.yml
# before goreleaser runs. Aborts the release if any release-blocker is
# detected. See PRD §10 (Risks) and ADR-0004 / D-24 / OQ-25 for context.

set -euo pipefail

cd "$(dirname "$0")/.."

fail=0
warn() { printf '\033[33mwarn\033[0m: %s\n' "$1" >&2; }
err()  { printf '\033[31mfail\033[0m: %s\n' "$1" >&2; fail=1; }
ok()   { printf '\033[32mok\033[0m:   %s\n' "$1"; }

# 1. internal/rules/default.md must contain the real default system prompt,
#    not the "<!-- TBD: -->" placeholder shipped while the file is unset.
if [ -f internal/rules/default.md ]; then
  if grep -q '<!-- TBD:' internal/rules/default.md; then
    err "internal/rules/default.md still contains a '<!-- TBD:' placeholder"
  else
    ok "internal/rules/default.md has no TBD placeholder"
  fi
else
  err "internal/rules/default.md is missing (release-blocker)"
fi

# 2. CHANGELOG.md should have at least one released version header by the
#    time we tag a non-zero version. During development [Unreleased] alone
#    is acceptable; warn rather than fail.
if ! grep -qE '^## \[[0-9]+\.[0-9]+\.[0-9]+\]' CHANGELOG.md 2>/dev/null; then
  warn "CHANGELOG.md has no released version header yet"
else
  ok "CHANGELOG.md has at least one released version header"
fi

# 3. i18n message bundles parity — every key in messages.en.yml must exist
#    in messages.tr.yml and vice versa. Cheap shell sanity check; the
#    authoritative check lives in Go (internal/i18n.MustHave test).
en=internal/i18n/messages.en.yml
tr=internal/i18n/messages.tr.yml
if [ -f "$en" ] && [ -f "$tr" ]; then
  en_keys=$(grep -E '^[A-Za-z0-9_.]+:' "$en" | cut -d: -f1 | sort -u || true)
  tr_keys=$(grep -E '^[A-Za-z0-9_.]+:' "$tr" | cut -d: -f1 | sort -u || true)
  missing_in_tr=$(comm -23 <(echo "$en_keys") <(echo "$tr_keys") || true)
  missing_in_en=$(comm -13 <(echo "$en_keys") <(echo "$tr_keys") || true)
  if [ -n "$missing_in_tr" ]; then
    err "i18n keys in en but missing in tr: $(echo "$missing_in_tr" | tr '\n' ' ')"
  fi
  if [ -n "$missing_in_en" ]; then
    err "i18n keys in tr but missing in en: $(echo "$missing_in_en" | tr '\n' ' ')"
  fi
  if [ -z "$missing_in_tr" ] && [ -z "$missing_in_en" ]; then
    ok "i18n message bundles in parity"
  fi
else
  warn "i18n bundles not yet present (internal/i18n/messages.{en,tr}.yml)"
fi

if [ "$fail" -ne 0 ]; then
  echo
  echo "release-check: BLOCKED" >&2
  exit 1
fi

echo
echo "release-check: ok"
