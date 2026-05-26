#!/usr/bin/env bash
# manpage.sh — regenerate man/commitbrief.1 via cobra's GenManTree.
# Requires the CLI root command to be implemented (Phase 5+). Invoked by
# `make manpage` and during the v0.6.0 polish phase.

set -euo pipefail

cd "$(dirname "$0")/.."

mkdir -p man

# Try the conventional flag first. If the binary does not yet support it,
# print a helpful message and exit non-zero so the caller knows.
if ! go run ./cmd/commitbrief --gen-man man 2>/dev/null; then
  cat >&2 <<'EOF'
manpage.sh: cmd/commitbrief does not yet support `--gen-man`.

Wire up cobra's GenManTree in cmd/commitbrief/main.go, e.g.:

    if *genMan != "" {
        header := &doc.GenManHeader{Title: "COMMITBRIEF", Section: "1"}
        return doc.GenManTree(rootCmd, header, *genMan)
    }

See https://pkg.go.dev/github.com/spf13/cobra/doc#GenManTree
EOF
  exit 1
fi

echo "manpage.sh: wrote man/commitbrief.1"
