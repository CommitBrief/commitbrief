# CommitBrief

LLM-powered local code review for git diffs. Run a "second pair of eyes"
review on your staged changes, a specific file, a single commit, or a
PR-style three-dot range — without leaving the terminal.

Pick a provider once, then:

```sh
commitbrief                                # review your staged changes
commitbrief diff HEAD                      # review working tree vs HEAD
commitbrief diff main...feature/x          # review a PR
commitbrief --unstaged --dir app/Models    # narrow any scope to a directory
```

Output is rendered as colored markdown in the terminal, plain markdown
to a file, or strict JSON for tooling — your choice.

## Why

A real reviewer is the gold standard, but they aren't always available
the moment you stage a change. CommitBrief gives you a quick, structured
read on your diff before another human (or your future self) sees it.

- **Local-first.** Diffs and review output stay on your machine. The
  only network egress is to the provider you chose.
- **Provider-agnostic.** Anthropic, OpenAI, Gemini, or Ollama (no API
  key needed for local Ollama).
- **Cache aware.** Re-running on an unchanged diff is essentially free —
  one disk read, no token spend. `--verbose` shows what you saved.
- **Custom review rules.** A repo's `COMMITBRIEF.md` is sent as the
  system prompt; per-user `OUTPUT.md` controls how findings are
  formatted.

## Install

### Homebrew (macOS / Linux)

```sh
brew install CommitBrief/tap/commitbrief
```

### Scoop (Windows)

```sh
scoop bucket add commitbrief https://github.com/CommitBrief/scoop-bucket
scoop install commitbrief
```

### Go

```sh
go install github.com/CommitBrief/commitbrief/cmd/commitbrief@latest
```

### GitHub Releases

Pre-built binaries for Linux, macOS, and Windows on amd64 and arm64 are
attached to each tagged release at
[github.com/CommitBrief/commitbrief/releases](https://github.com/CommitBrief/commitbrief/releases).

## Quick start

```sh
# 1. One-time setup: pick a provider, paste your API key, run a ping.
commitbrief setup

# 2. (optional) Write a project-specific review rules file.
commitbrief init

# 3. Stage some changes and review them.
git add path/to/changed.go
commitbrief --staged
```

`commitbrief list` prints the full command reference; `commitbrief
dry-run --staged` walks the pipeline without spending tokens.

## What you get

Default TTY output is a framed view: a header, a status line, **one
bordered panel per finding** (colored by severity), and a one-line
summary footer. Findings are ordered `critical → info`.

```text
commitbrief v0.6.0 · provider: anthropic/claude-sonnet-4-6 · cache: miss
analyzing 3 files · 42 added · 11 removed · COMMITBRIEF.md loaded

┌─ CRITICAL ─ internal/auth/session.go:142 ──────────────────────────────────┐
│ SQL fragment built from request input                                       │
│                                                                             │
│ String concatenation feeds db.Query() directly, bypassing the prepared      │
│ statement path used elsewhere in this package.                              │
│                                                                             │
│   - q := "SELECT * FROM sessions WHERE token = '" + tok + "'"               │
│   + q := "SELECT * FROM sessions WHERE token = $1"                          │
│     rows, err := db.Query(ctx, q, tok)                                      │
└─────────────────────────────────────────────────────────────────────────────┘

┌─ HIGH ─ internal/db/migrate.go:73 ─────────────────────────────────────────┐
│ NOT NULL column added without a default                                     │
│                                                                             │
│ The new column has no DEFAULT, so the migration will fail on any table     │
│ that already has rows. Either backfill in a prior migration or add a       │
│ DEFAULT before the constraint.                                              │
└─────────────────────────────────────────────────────────────────────────────┘

┌─ LOW ─ internal/api/handler.go:201 ────────────────────────────────────────┐
│ Wrapped error duplicated in message                                         │
│                                                                             │
│ The format string already contains "%w"; the prefix repeats the wrapped    │
│ error verbatim, producing "auth failed: auth failed: …" in logs.           │
└─────────────────────────────────────────────────────────────────────────────┘

✓ Done in 4.2s · 3 findings · 8421 tokens · Cost: $0.0319
```

Five severity levels — `critical`, `high`, `medium`, `low`, `info` —
colored red/orange/yellow/blue/grey. `info` items are always shown;
suppress them with a user-side OUTPUT.md template (see Configuration).

Re-run the same command on the same diff and the footer switches to
`Saved: $0.0319` — a local cache hit, no provider round-trip.
`--json` emits the raw findings (documented schema), `--markdown` runs
your OUTPUT.md template against the findings and writes plain text
suitable for `>> review.md`.

## Command surface

```sh
# Review scopes
commitbrief                                # = --staged (default scope)
commitbrief --unstaged                     # working-tree changes
commitbrief diff HEAD                      # working tree vs HEAD (git-diff passthrough)
commitbrief diff HEAD~3 HEAD               # the last three commits
commitbrief diff main feature              # one branch vs another
commitbrief diff main...feature            # three-dot PR-style range

# Narrow any scope with repeatable path filters
commitbrief --unstaged --file app/Http/Controllers/API.php --file routes/web.php
commitbrief --unstaged --dir database/seeder --dir app/Models
commitbrief diff HEAD~3 HEAD --dir docs

# Setup and rules
commitbrief setup [--local]                # provider + API key wizard
commitbrief providers list|use|test        # switch active provider without re-running setup
commitbrief config show|get|set            # inspect / tweak the merged YAML config
commitbrief init                           # write COMMITBRIEF.md + OUTPUT.md template
commitbrief compress [--level=balanced]    # shrink COMMITBRIEF.md
commitbrief doctor                         # health-check the pipeline
commitbrief install-hook [--hook=...]      # install a git hook that runs commitbrief
commitbrief dry-run                        # pipeline preview; no API call
commitbrief list                           # command reference

# Cache maintenance
commitbrief cache clear                    # wipe every cached LLM response for this repo
commitbrief cache prune [flags]            # bounded cleanup; defaults --keep-last 500 --older-than 7d
```

Global flags include `--json`, `--markdown`, `--output <file>`, `--copy`,
`--compact`, `--no-cache`, `--fail-on=<sev>`, `-f/--file` (repeatable),
`-d/--dir` (repeatable), `--yes`, `--verbose`, `--quiet`, `--lang`,
`--provider`, `--model`, `--color`. See `commitbrief --help`.

## Providers and pricing

| Provider | Models | Notes |
|----------|--------|-------|
| **Anthropic** | Claude Opus 4.7, Sonnet 4.6, Haiku 4.5 | Ephemeral prompt caching (5 m TTL) cuts repeated input cost ~10×. |
| **OpenAI** | GPT-4o, GPT-4o-mini | Automatic prompt caching at ≥1024-token prefixes. |
| **Google Gemini** | Gemini 2.5 Pro (2 M context!), 2.5 Flash, 1.5 Flash | Largest free-tier context windows. |
| **Ollama** | Whatever you've `ollama pull`'d | Local-only, no API key, no per-token cost. |

Adding a provider is one new package under `internal/provider/<name>/`.

## Configuration

Two-tier YAML config with field-level merge:

- **User:** `~/.commitbrief/config.yml` — defaults that apply everywhere
- **Repo:** `./.commitbrief/config.yml` — overrides for this repo
  (gitignored by default; run `commitbrief setup --local` to write it)

Plus environment variables for credentials and runtime tweaks, and
CLI flags for one-off overrides (`--provider gemini --model
gemini-2.5-flash`).

| Variable | Effect |
|---|---|
| `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` | Provider credentials. Overrides the matching `providers.<name>.api_key` in config. |
| `OLLAMA_HOST` | Sets `providers.ollama.base_url` when not set in config. |
| `COMMITBRIEF_PROVIDER` | Selects the active provider (same as `--provider` / `config.provider`). |
| `COMMITBRIEF_MODEL` | Overrides the active provider's model. |
| `COMMITBRIEF_CONFIG` | Absolute path to the user-level config file; replaces the default `~/.commitbrief/config.yml` lookup. Useful for tests and ephemeral CI environments. |
| `COMMITBRIEF_NO_COLOR`, `NO_COLOR` | Force ANSI color off (overrides `--color always`). |
| `LANG` | When `output.lang` is unset, the first two letters of `LANG` (e.g. `tr_TR.UTF-8` → `tr`) seed the active locale. Coerced to `en` if not in the supported set (`en`, `tr`). |

```yaml
# ~/.commitbrief/config.yml
version: 1
provider: anthropic                # default provider
providers:
  anthropic:
    model: claude-opus-4-7
  openai:
    model: gpt-4o
  ollama:
    model: qwen2.5-coder:14b
    base_url: http://localhost:11434
output:
  lang: en                         # CLI strings + review language
  stream: true
  color: auto                      # auto | always | never
cache:
  enabled: true
  ttl_days: 7
```

Review content lives in two files:

- **`COMMITBRIEF.md`** at the repo root — team-shared review rules,
  perspectives, project context. Sent to the LLM as the system prompt.
  Committed to git.
- **`.commitbrief/OUTPUT.md`** (or `~/.commitbrief/OUTPUT.md`) —
  per-user **Go `text/template`** applied locally to the findings for
  `--markdown` and `--output <file>.md`. Never sent to the LLM. The
  template has access to `.Findings` (typed `[]Finding{Severity, File,
  Line, Title, Description, Language, Snippet}`) plus helpers like
  `groupBySeverity`, `upper`, `countFiles`. Gitignored.

`commitbrief init` writes both templates from the embedded defaults.

## Filtering

Three layers, applied in order. Later layers win, so a `!pattern` in
`.commitbriefignore` can revert a built-in exclusion:

1. **Built-in defaults** — binaries, lock files, `vendor/**`,
   `node_modules/**`, generated code, build artifacts, IDE/OS noise.
2. **`.commitbriefignore`** at the repo root — gitignore syntax,
   team-shared.
3. **`COMMITBRIEF.md` semantic filter** — natural-language rules the LLM
   applies to whatever survives the first two layers.

`commitbrief dry-run --staged` reports how many files each layer removed.

## Building from source

Requires **Go 1.25+**.

```sh
git clone https://github.com/CommitBrief/commitbrief
cd commitbrief
make build         # → ./commitbrief (ldflags inject version/commit/date)
make test          # unit + integration tests (live providers skipped)
make lint          # golangci-lint v2
make smoke         # end-to-end pipeline check; no API key needed
make bench         # diff pipeline + cache hit benchmarks
make manpage       # regenerate man/*.1 from cobra
make test-live     # provider tests against real APIs (keys required)
make license-check # GPL-3.0 compatibility audit
```

`make help` lists everything.

## FAQ

**Does CommitBrief replace human review?**
No. It's a first pass — a fast sanity check before a teammate (or your
future self) looks. The default rules deliberately target low-risk,
high-signal stuff: obvious bugs, missing nil checks, accidental
secrets. Treat output as a checklist to skim, not a verdict.

**Where does my code go?**
Diffs leave your machine only when sent to the provider you picked.
Anthropic, OpenAI, and Gemini get the diff + your `COMMITBRIEF.md` over
HTTPS to their official endpoints. Ollama is local-only; nothing leaves
the host. Review output is rendered locally and cached under
`./.commitbrief/cache/` — never uploaded.

**Will it break my workflow if the LLM provider is down?**
The CLI fails loudly and exits non-zero. There's no degraded mode that
silently skips review. Use `commitbrief dry-run` to test the pipeline
end-to-end without an API call.

**How do I exclude generated code or vendored files?**
Built-in defaults already skip `vendor/**`, `node_modules/**`, lock
files, binaries, and most generated artifacts. Drop a
`.commitbriefignore` at the repo root for project-specific rules
(gitignore syntax, supports `!negation` to revert a built-in).
`commitbrief dry-run --staged` reports how many files each layer
removed.

**When does the cache invalidate?**
The cache key is a SHA-256 of `diff + system prompt + provider + model
+ lang + schema version`. Change any of those and you get a fresh
review. Default TTL is 7 days; configurable via `cache.ttl_days`.

**Can I run it in CI?**
The primary target is the developer's terminal, but the CI-friendly
pieces are in place: `--fail-on=<severity>` (or `--fail-on=any`)
returns a non-zero exit code when a finding meets or exceeds the
threshold, `--json` emits the structured-findings document machine-
readably, and `commitbrief install-hook` scaffolds a pre-commit /
commit-msg / pre-push hook locally. A dedicated GitHub Action wrapper
is still on the v1.x roadmap; for now drive the binary directly from
your workflow.

**Why GPL-3.0?**
The CLI is end-user software, and copyleft keeps forks and
redistributions open. New dependencies must stay GPL-compatible
(MIT/Apache-2.0/BSD/ISC/MPL-2.0/LGPL-3.0+ are fine);
`make license-check` enforces it.

## License

[GPL-3.0-or-later](LICENSE). Provider SDK dependencies are Apache-2.0 or
MIT; the full audit is `make license-check`.

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the project-specific build
and test flow, and the
[org-wide CONTRIBUTING guide](https://github.com/CommitBrief/.github/blob/main/CONTRIBUTING.md)
for inbound-equals-outbound licensing and PR conventions.

Bug reports and questions are welcome in
[Issues](https://github.com/CommitBrief/commitbrief/issues) and
[Discussions](https://github.com/CommitBrief/commitbrief/discussions).
