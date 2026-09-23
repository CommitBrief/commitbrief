#!/usr/bin/env bash
# pin-action-readme.sh — pin the version examples in
# CommitBrief/commitbrief-action's README.md to a given CLI release tag.
#
# Invoked by .github/workflows/release.yml on every stable tag (vX.Y.Z, no
# prerelease suffix), against a checkout of the commitbrief-action repo, to
# open a "docs: pin README examples to <tag>" PR there. See .ssot/plans/
# 2026-09-23-version-pins/02-action-pin-pr.md (D-VP-2).
#
# Rewrites two things in README.md, both already-released semver tags:
#   1. every `version: vX.Y.Z` line inside the workflow snippets
#   2. the Inputs table's `e.g. `vX.Y.Z`` example for the `version` input
# action.yml's own description example is intentionally out of scope (see
# the phase brief) and untouched here.
#
# Idempotent: running it twice with the same tag produces zero further diff,
# because the replacement value is itself a valid vX.Y.Z match for the next
# run's pattern. Also refuses to move the pin backwards (e.g. a re-run
# against an older tag, or a backport tag pushed after a newer release) —
# it silently no-ops instead of opening a downgrade PR.
#
# Usage: pin-action-readme.sh <path-to-README.md> <new-tag>
#   e.g. pin-action-readme.sh commitbrief-action/README.md v1.18.0

set -euo pipefail

usage() {
  echo "usage: $(basename "$0") <path-to-README.md> <new-tag>" >&2
  exit 1
}

[ $# -eq 2 ] || usage

readme=$1
tag=$2

[ -f "$readme" ] || { echo "pin-action-readme: no such file: $readme" >&2; exit 1; }

# Strict — vX.Y.Z only, nothing else. This also doubles as an injection
# guard: the tag is later spliced into a sed replacement unescaped, so a
# value containing sed metacharacters (`&`, `/`, `\`) must never pass here.
if ! [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  echo "pin-action-readme: tag '$tag' doesn't look like vX.Y.Z" >&2
  exit 1
fi

# A literal backtick, kept in a variable so it can be safely interpolated
# into a double-quoted sed script below without tripping bash's own
# command-substitution parsing of unescaped backticks.
bt='`'
semver='v[0-9]+\.[0-9]+\.[0-9]+'

# Refuse to downgrade: if README already pins a version and it's the same
# tag or newer than the one we were asked to pin, there's nothing to do.
current=$(grep -m1 -oE "version: ${semver}" "$readme" | sed -E 's/^version: //') || true
if [ -n "${current:-}" ] && [ "$tag" != "$current" ]; then
  newer=$(printf '%s\n%s\n' "$current" "$tag" | sort -V | tail -1)
  if [ "$newer" != "$tag" ]; then
    echo "pin-action-readme: new tag $tag is not newer than the current pin $current; skipping" >&2
    exit 0
  fi
fi

sed -E -i.bak \
  -e "s/version: ${semver}/version: ${tag}/g" \
  -e "s/e\\.g\\. ${bt}${semver}${bt}/e.g. ${bt}${tag}${bt}/g" \
  "$readme"
rm -f "${readme}.bak"
