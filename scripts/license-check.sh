#!/usr/bin/env bash
# license-check.sh — audit transitive dependency licenses for GPL-3.0
# compatibility. See PRD §7.6 and ADR-0012: CommitBrief is GPL-3.0-or-later
# and may only bundle compatible dependencies.

set -euo pipefail

cd "$(dirname "$0")/.."

# Licenses considered compatible with GPL-3.0-or-later for static linking.
# Adjust deliberately; rejecting unknown is the safe default.
ALLOWED='Apache-2.0,BSD-2-Clause,BSD-3-Clause,GPL-3.0,GPL-3.0-or-later,ISC,LGPL-3.0,LGPL-3.0-or-later,MIT,MPL-2.0,Unlicense'

# Empty module? Skip — go-licenses errors on zero packages.
if ! find . -name '*.go' -not -path './vendor/*' -not -path './testdata/*' 2>/dev/null | grep -q .; then
  echo "license-check: no Go files yet; skipping"
  exit 0
fi

if ! command -v go-licenses >/dev/null 2>&1; then
  echo "go-licenses not on PATH; installing via 'go install'..." >&2
  GOBIN="${GOBIN:-$(go env GOPATH)/bin}" go install github.com/google/go-licenses@latest
  export PATH="${GOBIN:-$(go env GOPATH)/bin}:$PATH"
fi

go-licenses check ./... \
  --allowed_licenses="$ALLOWED" \
  --ignore github.com/CommitBrief/commitbrief

echo "license-check: ok"
