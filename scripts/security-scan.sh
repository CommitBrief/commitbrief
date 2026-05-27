#!/usr/bin/env bash
# security-scan.sh — gosec wrapper for CommitBrief.
#
# Runs gosec against the module with a documented exclusion set so the
# output is signal, not noise. The excluded rule IDs all fire
# systematically against legitimate CLI patterns; the reasoning is
# inline below and was reviewed during the v1.0.0-rc.1 security audit.
# Revisit if the codebase grows new privilege boundaries.
#
# Invoked locally by `make security-check` and in CI by
# .github/workflows/security.yml (informational only — does not gate
# merges; CodeQL covers the supply-chain side).

set -euo pipefail
cd "$(dirname "$0")/.."

# Rule exclusions, with rationale. Bump GOSEC_EXCLUDE in lockstep with
# the comments so a future reader can audit the call.
#
#   G304 — file inclusion via variable.
#     A CLI tool's job is to read user/repo-relative paths
#     (config.yml, COMMITBRIEF.md, .commitbriefignore, --output
#     destinations). Every os.ReadFile / os.Open hit is parameterised
#     by design; no privilege boundary to escape.
#
#   G306 — WriteFile mode 0644 / 0755.
#     Shared text files (.gitignore is committed, COMMITBRIEF.md
#     backups inherit source mode) and executable hook scripts
#     (.git/hooks/* MUST be 0755 to fire). 0600 would either break
#     git's mode invariants or hide the file from collaborators.
#
#   G301 — Mkdir mode 0755 on .git/hooks.
#     Matches git's own default. Tightening diverges from upstream
#     umask handling.
#
#   G204 — Subprocess launched with variable.
#     Fixed binary allowlist (git, claude, gemini, wl-copy/xclip).
#     exec.Command builds argv variadically — no shell interpretation,
#     no metacharacter injection surface.
#
#   G101 — Potential hardcoded credentials.
#     Hits are help-text strings ("Get an API key from https://…") and
#     renderer labels ("(local cache hit)") that pattern-match
#     key-shaped substrings. Zero actual secrets in source.
#
#   G122 — Filesystem race in WalkDir callback.
#     Only fires in cache_prune.go's pre-write probe. The cache lives
#     inside the user's repo and is not on a security boundary; a
#     symlink swap mid-prune would at worst remove a different file
#     the user owns. Worth revisiting if cache ever stores secrets.
GOSEC_EXCLUDE="G304,G306,G301,G204,G101,G122"

# Real findings (G115 int-overflow, future G401 weak crypto, etc.) are
# NOT excluded — they should fail the scan and demand a fix.
exec gosec \
  -quiet \
  -exclude="$GOSEC_EXCLUDE" \
  -exclude-dir=reviews \
  -fmt=text \
  ./...
