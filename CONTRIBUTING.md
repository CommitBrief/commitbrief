# Contributing to CommitBrief

Thanks for thinking about contributing. This document captures the
**repo-specific** build, test, and lint flow. The
[org-wide CONTRIBUTING guide](https://github.com/CommitBrief/.github/blob/main/CONTRIBUTING.md)
covers licensing (inbound = outbound, GPL-3.0-or-later), Conventional
Commits, PR conventions, and how to file issues — that document applies
here too.

## Prerequisites

- **Go 1.25+** — lower versions will not compile.
- `golangci-lint` v1.55+ for `make lint`.
- `goimports` for `make fmt` (`go install golang.org/x/tools/cmd/goimports@latest`).
- `bash` for the helper scripts. On Windows use Git Bash or WSL.
- Optional: `goreleaser` v2 for local release dry-runs.

## Day-to-day

```sh
make build        # → ./commitbrief
make test         # unit + integration; live provider tests excluded
make lint         # golangci-lint
make fmt          # gofmt + goimports
make tidy         # go mod tidy
```

Run a single test:

```sh
go test ./internal/<pkg> -run TestName -v
```

Live provider tests (real API keys required) are gated behind a build tag:

```sh
make test-live    # = go test -tags=live ./...
```

## Project layout

- `cmd/commitbrief/` — thin entry point.
- `internal/` — every package; **no `pkg/`** in v1 — there is no stable
  public Go API.
- `testdata/` — fixture repos, diff samples, ignore patterns, golden files.
- `scripts/` — release-check, license-check, manpage generation.

## Before sending a pull request

1. `make fmt lint test` is green locally.
2. New user-facing strings go through `internal/i18n`; add the key to
   **both** `messages.en.yml` and `messages.tr.yml`.
3. Material decisions or scope changes update the appropriate ADR
   (open a new ADR if needed — supersede rather than silently contradict
   an existing one).
4. New dependencies pass `make license-check` (must be GPL-3.0-compatible —
   MIT, Apache-2.0, BSD, ISC, MPL-2.0, GPL/LGPL-3.0+ are fine; AGPL and
   proprietary are not).
5. Pre-release tags additionally pass `make release-check` (e.g. the
   `internal/rules/default.md` placeholder guard from PRD §10 / OQ-25).

## Cross-platform notes

CommitBrief targets macOS, Linux, and Windows on amd64 and arm64 from a
single codebase: always use `path/filepath`, normalize ignore patterns with
`filepath.ToSlash`, prefer `os.UserHomeDir` / `os.UserConfigDir`. Platform
splits use `//go:build` tags (`*_windows.go` / `*_unix.go`).

## Reporting bugs and asking questions

Use the bug-report and feature-request templates inherited from the org
`.github` repository. For "how do I…" questions, open a **Discussion** in
this repo. Security issues go through
[SECURITY.md](https://github.com/CommitBrief/.github/blob/main/SECURITY.md)
privately — do not open public issues for vulnerabilities.

## Contact

Off-channel email: **info@muhammetsafak.com.tr**.
