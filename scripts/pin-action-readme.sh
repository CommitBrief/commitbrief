#!/usr/bin/env bash
# pin-action-readme.sh — pin CommitBrief/commitbrief-action to a given CLI
# release tag.
#
# Invoked by .github/workflows/release.yml on every stable tag (vX.Y.Z, no
# prerelease suffix), against a checkout of the commitbrief-action repo, to
# open a "chore: pin CLI version to <tag>" PR there (D-VP-2). The name is
# historical: the script started out touching only README.md.
#
# Rewrites, all to the same already-released semver tag:
#   README.md
#     1. every `version: vX.Y.Z` line inside the workflow snippets
#     2. the Inputs table's `e.g. `vX.Y.Z`` example for the `version` input
#   action.yml
#     3. the `default:` of the `version` input (ADR-0044: the action installs
#        a pinned prebuilt release by default, so this is the CLI version
#        every `@v1` consumer gets once the `v1` tag is re-cut on the merge)
#
# Idempotent: running it twice with the same tag produces zero further diff,
# because the replacement value is itself a valid vX.Y.Z match for the next
# run's pattern. Also refuses to move a pin backwards (e.g. a re-run against
# an older tag, or a backport tag pushed after a newer release) — each file
# is checked on its own and silently left alone instead. A non-semver
# action.yml default (such as the historical "latest") is always replaced.
#
# Usage: pin-action-readme.sh <path-to-commitbrief-action-checkout> <new-tag>
#   e.g. pin-action-readme.sh commitbrief-action v1.18.0

set -euo pipefail

usage() {
  echo "usage: $(basename "$0") <path-to-commitbrief-action-checkout> <new-tag>" >&2
  exit 1
}

[ $# -eq 2 ] || usage

repo=$1
tag=$2
readme="${repo}/README.md"
action="${repo}/action.yml"

for f in "$readme" "$action"; do
  [ -f "$f" ] || { echo "pin-action-readme: no such file: $f" >&2; exit 1; }
done

# Strict — vX.Y.Z only, nothing else. This also doubles as an injection
# guard: the tag is later spliced into a sed replacement and an awk string
# unescaped, so a value containing metacharacters (`&`, `/`, `\`, `"`) must
# never pass here.
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "pin-action-readme: tag '$tag' doesn't look like vX.Y.Z" >&2
  exit 1
fi

# A literal backtick, kept in a variable so it can be safely interpolated
# into a double-quoted sed script below without tripping bash's own
# command-substitution parsing of unescaped backticks.
bt='`'
semver='v[0-9]+\.[0-9]+\.[0-9]+'

# is_downgrade <current> — true when <current> is a vX.Y.Z tag that is the
# same as or newer than $tag, i.e. pinning would be a no-op or a downgrade.
is_downgrade() {
  local current=$1 newer
  [[ "$current" =~ ^${semver}$ ]] || return 1
  [ "$current" = "$tag" ] && return 1 # same tag: rewrite, which is a no-op
  newer=$(printf '%s\n%s\n' "$current" "$tag" | sort -V | tail -1)
  [ "$newer" != "$tag" ]
}

# ------------------------------------------------------------------ README.md

current=$(grep -m1 -oE "version: ${semver}" "$readme" | sed -E 's/^version: //') || true
if is_downgrade "${current:-}"; then
  echo "pin-action-readme: new tag $tag is not newer than README.md's pin $current; skipping README.md" >&2
else
  sed -E -i.bak \
    -e "s/version: ${semver}/version: ${tag}/g" \
    -e "s/e\\.g\\. ${bt}${semver}${bt}/e.g. ${bt}${tag}${bt}/g" \
    "$readme"
  rm -f "${readme}.bak"
fi

# ----------------------------------------------------------------- action.yml

# The `default:` line of the top-level `version:` input: a two-space-indented
# `version:` key under `inputs:` opens the block, the next two-space key
# closes it.
read_default() {
  awk '
    /^  version:[[:space:]]*$/ { inblock = 1; next }
    inblock && /^  [^ ]/ { inblock = 0 }
    inblock && /^    default:/ {
      v = $0
      sub(/^    default:[[:space:]]*/, "", v)
      gsub(/"/, "", v)
      print v
      exit
    }
  ' "$action"
}

current=$(read_default)
if [ -z "$current" ]; then
  echo "pin-action-readme: no default for the version input in $action" >&2
  exit 1
fi
if is_downgrade "$current"; then
  echo "pin-action-readme: new tag $tag is not newer than action.yml's default $current; skipping action.yml" >&2
else
  tmp="${action}.tmp"
  awk -v tag="$tag" '
    /^  version:[[:space:]]*$/ { inblock = 1; print; next }
    inblock && /^  [^ ]/ { inblock = 0 }
    inblock && /^    default:/ { print "    default: \"" tag "\""; inblock = 0; next }
    { print }
  ' "$action" >"$tmp"
  mv "$tmp" "$action"
fi
