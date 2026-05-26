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

## Performance

PRD §7.1 sets four ceilings for local-only work (no network):

| Target | Ceiling | Where it lives |
|---|---|---|
| Cold start (`commitbrief --version`) | < 100 ms | binary startup |
| Local pipeline on a 10k-line diff (parse + filter + token estimate) | < 200 ms | `internal/diff` |
| `dry-run` total wall time | < 500 ms | `internal/cli` (integration) |
| Cache hit additional latency | < 100 ms | `internal/cache` |

The two component targets are exercised by `make bench`
(`internal/diff/bench_test.go`, `internal/cache/bench_test.go`).
Cold start is a binary-startup measurement, not a Go benchmark; time
it on a release build with the shell builtin (see below). The
`dry-run` target is integration-level — time it end-to-end after a
real `git init` + staged change.

### Reproducing

```sh
# Component benchmarks (parse + filter + token estimate + cache hit):
make bench

# Cold start (steady state — discard the first run, it includes
# macOS Gatekeeper / unsigned-binary verification ~600 ms on a
# `go build` artifact; brew-installed binaries are verified once at
# install time):
make build
for i in 1 2 3 4 5; do { time ./commitbrief --version >/dev/null; } 2>&1 | tail -1; done

# dry-run wall time (against a real staged change):
time commitbrief dry-run --staged
```

### Reference run

Reference numbers on **darwin/arm64 (Apple M4 Pro), Go 1.25.6,
v0.6.0-pre**. Update the table when you bump targets, change
algorithms, or add a new layer to the pipeline:

| Benchmark | ns/op | ms/op | vs PRD target |
|---|---|---|---|
| `Parse10kLines` | 656,745 | 0.66 | — (component) |
| `Filter10kLines` | 1,931,639 | 1.93 | — (component) |
| `EstimateTokens10kLines` | 11,052 | 0.011 | — (component) |
| `Pipeline10kLines` (headline) | 2,602,795 | **2.60** | < 200 ms ✅ (~75× headroom) |
| `CacheHit` | 12,764 | 0.013 | < 100 ms ✅ (~7700× headroom) |
| Cold start (steady) | — | ~16 | < 100 ms ✅ (~6× headroom) |

Hot-path watch-outs that have eaten margin in the past:

- **`bufio.Scanner` buffer** in `internal/diff/parse.go` is sized to
  16 MiB to tolerate a single huge hunk. Shrinking this hits very-large
  diffs hard; do not lower without measurement.
- **`go-git`'s gitignore matcher** is allocation-heavy on initial
  pattern compile but cheap per match; `ignore.Compose` does the
  compile once per `Filter()` call. Per-file matching is the bulk of
  `Filter10kLines`.

If a benchmark in this table regresses by **>20%** in a PR, mention
it explicitly in the description with a justification (a clearer
implementation that costs a few ns may still be net-positive).

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
