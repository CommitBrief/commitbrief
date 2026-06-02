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

<img width="1252" height="672" alt="commitbrief" src="https://github.com/user-attachments/assets/643aaf6a-7020-41e6-a12c-e6c46c54cd7b" />


## Why

A real reviewer is the gold standard, but they aren't always available
the moment you stage a change. CommitBrief gives you a quick, structured
read on your diff before another human (or your future self) sees it.

- **Local-first.** Diffs and review output stay on your machine. The
  only network egress is to the provider you chose.
- **Provider-agnostic.** Anthropic, OpenAI, Gemini, or Ollama as
  API-backed providers; `claude-cli`, `gemini-cli`, and `codex-cli`
  reuse your local Claude Code / Gemini / Codex CLI subscription (no
  extra API key).
- **Cache aware.** Re-running on an unchanged diff is essentially free —
  one disk read, no token spend. `--verbose` shows what you saved.
- **Custom review rules.** A repo's `COMMITBRIEF.md` is sent as the
  system prompt; per-user `OUTPUT.md` controls how findings are
  formatted.

## Measured review quality

CommitBrief ships an eval harness (`make eval`) that scores real review
output against a 23-fixture known-answer corpus — 23 planted defects
across security, correctness, concurrency, resource-leak, error-handling
and performance categories, plus 3 clean controls a good review must stay
silent on. About a quarter of the corpus is a **held-out slice** that
prompt and corpus tuning never inspect, so each cell below reports
`dev · held` — the tunable slice and the held-out generalization slice
separately (ADR-0018). Numbers are from `make eval-live`, captured
2026-05-29 (mean of *Runs* live runs each):

| Model              | Recall (dev · held) | FP-rate (dev · held) | Precision (dev · held) | Runs |
|--------------------|:-------------------:|:--------------------:|:----------------------:|:----:|
| Claude Haiku 4.5   | 1.00 · 1.00         | 0.00 · 0.00          | 0.70 · 0.62            | 5    |
| Claude Sonnet 4.6  | 1.00 · 1.00         | 0.00 · 0.50          | 0.68 · 0.48            | 3    |
| Claude Opus 4.8    | 0.94 · 1.00         | 0.00 · 0.00          | 0.61 · 0.53            | 3    |
| Gemini 2.5 Flash   | 0.96 · 1.00         | 0.44 · 0.00          | 0.84 · 0.56            | 3    |
| OpenAI GPT-4o      | 0.85 · 1.00         | 0.44 · 0.33          | 0.79 · 0.75            | 3    |

- **Recall** — share of planted defects caught. Every model recalls the
  full held-out slice; the dev dips (Opus, GPT-4o) come from the harder
  multi-finding dev fixtures, not from missing whole defects.
- **FP-rate** — findings landing on a clean-control line (flagging a benign
  change). Note where the noise lives: Sonnet trips the held-out clean
  control; Gemini and GPT-4o trip the dev ones.
- **Precision** — a *conservative floor*: any finding outside the answer
  key counts as a false positive, but on these small diffs many "extra"
  findings are legitimate secondary observations (a second panic, an
  ignored error) rather than noise. The terser models (GPT-4o, Gemini)
  score higher precisely because they say less — at the cost of recall.
  Read recall + FP-rate as the cleaner signals; precision is sensitive to
  how exhaustively the corpus is annotated.

The two slices are **not difficulty-matched** — the split exists to catch
overfitting in *future* tuning (a dev gain that doesn't carry to held-out),
not for a direct dev-vs-held comparison today. Reproduce any row with
`COMMITBRIEF_EVAL_PROVIDER=<name> make eval-live`, which prints FULL / DEV /
HELD-OUT scorecards (using the key already in `~/.commitbrief/config.yml`).

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

## Stability

The v1.0.0 line is an **API freeze**. CLI flag surface, the JSON
schema v1 (`{schema, content, findings, summary, meta}` — emitted by
`--json`), `COMMITBRIEF.md` and `OUTPUT.md` formats, and the public
config keys all follow strict semver from v1.0.0 onwards — breaking
changes ship in v2.x. The current line is `v1.0.0-rc.1`, the freeze
checkpoint; anything locked in here is the long-term contract.

Upgrading from v0.x? See the [migration guide in
CHANGELOG.md](CHANGELOG.md#migration-guide-v0x--v10) — the scope
flags (`--commit` / `--branch` / `--pull-request`) and `--yes`
semantics changed during the v0.9.x line.

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

# Commit message (writes to git, with confirmation)
commitbrief commit                           # suggest a message for the staged diff, then commit
commitbrief commit --type conventional       # pick a format (-t); see "commitbrief commit" below
commitbrief commit --generate 3              # offer 3 alternatives to choose from (-g)
commitbrief commit --yes                     # commit the first suggestion non-interactively

# Setup and rules
commitbrief setup [--local]                  # provider + API key wizard
commitbrief providers list|use|test          # switch active provider without re-running setup
commitbrief config show|get|set              # inspect / tweak the merged YAML config
commitbrief init [--force]                   # write COMMITBRIEF.md + OUTPUT.md template
commitbrief compress [--level=balanced] [--dry-run]  # shrink COMMITBRIEF.md (preview first if you want)
commitbrief doctor                           # health-check the pipeline
commitbrief install-hook [--hook=...]        # install a git hook that runs commitbrief
commitbrief dry-run                          # pipeline preview; no API call
commitbrief list                             # command reference

# Cache maintenance
commitbrief cache clear                    # wipe every cached LLM response for this repo
commitbrief cache prune [flags]            # bounded cleanup; defaults --keep-last 500 --older-than 7d
commitbrief cache stats                    # entry count, size, age range, per-provider breakdown
commitbrief cache inspect <key>            # one entry's metadata (add --show-content for the body)
```

Global flags: `--json`, `--markdown`, `--output <file>`, `--copy`,
`--suggest-commit` (after the review, suggest a Conventional Commit
message for the staged diff; prints to stdout, requires `--staged`, not
with `--json`/`--markdown`/`--output`), `--compact`, `--no-cache`,
`--fail-on=<sev>`, `--min-severity=<sev>`
(hide findings below this severity in the rendered output; `--json` and
`--fail-on` still see the full set), `-f/--file` (repeatable),
`-d/--dir` (repeatable), `--yes`, `--verbose`, `--quiet`, `--lang`,
`--provider`, `--model`, `--cli <claude|gemini|codex>` (shorthand for the
CLI-tool-backed providers; mutually exclusive with `--json` /
`--markdown`), `--with-context` (CLI providers only — let the host CLI
read project files beyond the diff to ground the review; see below),
`--allow-secrets` (acknowledge a flagged credential in
the diff), `--no-cost-check` (skip cost preflight),
`--show-prompt` (print the exact system + user prompt that would be sent,
then exit — no provider call, no cost; honours `--output`), `--color`. See
`commitbrief --help`.

### `commitbrief commit`

Generate a commit message from the **staged** diff and, after you confirm,
run `git commit`. This is the only command that writes to git — every
review path is read-only.

```sh
commitbrief commit                          # suggest one message, confirm (default Yes), commit
commitbrief commit -t conventional+body     # conventional subject + a generated body
commitbrief commit -g 4                      # pick from 4 alternatives
commitbrief commit --provider openai --model gpt-5.4-mini
commitbrief commit --yes                     # CI/non-interactive: commit the first suggestion
```

- **`--type` / `-t`** — message format: `plain` (default), `conventional`,
  `conventional+body`, `gitmoji`, `subject+body`.
- **`--generate` / `-g <N>`** — produce N alternatives (1–10) and choose one
  in an arrow-key selector. A single provider call generates all N.
- **`--provider` / `--model` / `--cli`** — select the backend, same as a
  review. Messages are always written in English regardless of `--lang`.
- Defaults come from the `commit.type` and `commit.generate` config keys when
  the flags are omitted (precedence: flag > config > built-in).
- The pre-send `.commitbrief/**` guard, secret scan, and cost preflight run on
  the staged diff before the call; the suggestion is cached.
- With **nothing staged** it errors (stage with `git add` first). On a
  **non-TTY** without `--yes` it errors, because it cannot show the confirm or
  selector. `--yes` commits the first suggestion (it does **not** bypass the
  secret scan or cost preflight).

> The tool never auto-stages and never edits files — it only runs `git commit`
> on changes you already staged, and only after you say Yes.

### `--with-context` (CLI providers only)

By default a review sees only the diff. With `--with-context`, a
CLI-backed provider (`--cli claude|gemini|codex`) is allowed to read
other files in the repo — callers of the changed code, type definitions,
sibling modules, project conventions — to ground its review in the wider
codebase. The diff stays the subject of the review; the rest is context.
The host CLI runs **read-only** (it never modifies your tree) in the
repository root. API providers can't read files, so the flag errors for
them.

> ⚠ **Security:** with `--with-context` the agent decides which files to
> read, so file contents **beyond the diff** — including untracked
> secrets (`.env`, key files) — can reach the provider's backend. The
> pre-send secret scan covers the **diff only**, not files the agent
> reads on its own. CommitBrief prints this caution on every
> `--with-context` run. Use it on repositories you trust.

## Providers and pricing

Four API providers + two CLI-tool-backed providers ship in the box:

| Provider | Models | Notes |
|----------|--------|-------|
| **Anthropic** | Claude Opus 4.8 (default), Sonnet 4.6, Haiku 4.5 | Ephemeral prompt caching (5 m TTL) cuts repeated input cost ~10×. Opus 4.8 advertises a 1 M-token context. |
| **OpenAI** | GPT-5.4-mini (default), GPT-5.5, GPT-5.5-pro, GPT-4o, GPT-4o-mini | Automatic prompt caching at ≥1024-token prefixes. `gpt-5.5-pro` runs via the Responses API (not Chat Completions) and can take several minutes per review. |
| **Google Gemini** | Gemini 3.5 Flash (default), 3.1 Pro, 3.1 Flash-Lite | ~1 M-token context windows. `gemini-3.1-pro-preview` is a preview model. |
| **DeepSeek** | deepseek-chat, deepseek-reasoner | OpenAI-compatible API (`DEEPSEEK_API_KEY`); JSON is prompt-driven (degrades gracefully). |
| **Mistral** | Mistral Large / Small, Codestral | OpenAI-compatible API (`MISTRAL_API_KEY`). |
| **Cohere** | Command R+ / R, Command A | Cohere's OpenAI-compatibility endpoint (`COHERE_API_KEY`). |
| **Ollama** | Whatever you've `ollama pull`'d | Local-only, no API key, no per-token cost. |
| **`claude-cli`** | Whatever your local Claude Code uses | Subprocess of `claude -p -` — no API key on our side; reuses your Claude Code subscription. `commitbrief --cli claude --staged`. |
| **`gemini-cli`** | Whatever your local Gemini CLI uses | Subprocess of `gemini -p` — no API key on our side; reuses your Gemini CLI auth. `commitbrief --cli gemini --staged`. |
| **`codex-cli`** | Whatever your local Codex CLI uses | Subprocess of `codex exec --sandbox read-only --skip-git-repo-check` — no API key on our side; reuses your Codex CLI (ChatGPT) auth. `commitbrief --cli codex --staged`. |

CLI-backed providers emit pre-formatted plain text — they bypass the
structured-findings JSON path, the per-finding cards renderer, and the
`--fail-on` severity gate (the host CLI's response shape isn't our
contract to enforce). The review block is bracketed top and bottom
with a `--------------------` rule (the same separator used between
findings) and written to stdout, so
`commitbrief --cli claude --output review.md` writes the file just
like the API providers do; `--json` / `--markdown` are rejected
upfront. Useful when you've already paid for a Claude or Gemini CLI
subscription and don't want to manage a second API key.

Adding a provider is one new package under `internal/provider/<name>/`.

> The `remote pr` subcommand (below) requires an **API provider** when it
> posts to GitHub — `claude-cli` / `gemini-cli` / `codex-cli` don't
> produce structured findings to anchor comments. (In `--no-post` mode it
> only prints locally, so CLI providers work there.)

## Reviewing pull requests from the terminal

`commitbrief remote pr <ID>` reviews a GitHub pull request and writes the
result back to GitHub: each finding becomes an inline review comment and
the review is submitted with a verdict (approve / comment /
request-changes). It drives your local `gh` CLI — no hosted bot, no extra
auth.

```sh
commitbrief remote pr 42                       # PR #42 in the current repo
commitbrief remote pr CommitBrief/web#10       # cross-repo (owner/repo#N)
commitbrief remote pr 42 --request-changes-on=high
commitbrief remote pr 42 --no-post             # review locally, write nothing to GitHub
commitbrief remote pr 42 --no-post --output review.md   # …or --json / --cli gemini, etc.
```

`--request-changes-on=<critical|high|medium|low>` (default `critical`)
sets the severity at or above which the verdict becomes request-changes;
`--repo owner/repo` overrides git-context repo discovery. Requires an API
provider. `--fail-on` is ignored here — the GitHub verdict replaces the
exit-code gate.

**`--no-post`** turns `remote pr` into a read-only review: it fetches the
PR diff via `gh` and renders the result to your terminal exactly like a
local review, **writing nothing to GitHub** (no comments, no verdict).
Because the output is local, the flags posting mode rejects all apply —
`--json`, `--markdown`, `--output`, `--copy`, `--compact`, `--cli`, and
`--fail-on` — and there's no self-PR restriction (you can review your own
PR). Results are cached like any local review. Handy for triaging a PR,
piping findings into another tool, or reviewing with a CLI provider.

Each comment is anchored to the diff side its line lives on — `RIGHT`
(new file) for added/context lines, `LEFT` (old file) for removed ones.
A finding whose line falls outside the diff (or whose POST is rejected)
is not dropped: it is appended to the review summary so nothing is lost.

## Continuous integration

Run CommitBrief on pull requests with the **[CommitBrief Review GitHub
Action](https://github.com/CommitBrief/commitbrief-action)**:

```yaml
# .github/workflows/commitbrief.yml
on: pull_request
permissions:
  contents: read
  pull-requests: write   # comment mode posts the review
jobs:
  review:
    runs-on: ubuntu-latest
    steps:
      - uses: CommitBrief/commitbrief-action@v1
        with:
          provider: anthropic
          api-key: ${{ secrets.ANTHROPIC_API_KEY }}
```

It posts each finding as an inline review comment plus a verdict
(`comment` mode, via `remote pr`), or runs an exit-code gate
(`mode: gate`, via `diff --fail-on`). You can also drive the binary
directly in any workflow: `commitbrief diff <base>...<head> --fail-on=high`.

## Configuration

Two-tier YAML config with field-level merge:

- **User:** `~/.commitbrief/config.yml` — defaults that apply everywhere
- **Repo:** `./.commitbrief/config.yml` — overrides for this repo
  (gitignored by default; run `commitbrief setup --local` to write it)

Plus environment variables for credentials and runtime tweaks, and
CLI flags for one-off overrides (`--provider gemini --model
gemini-3.5-flash`).

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
    model: claude-opus-4-8
    pricing:                         # optional: override built-in $/1M rates
      claude-opus-4-8:               # (cost preflight / verbose footer / cache)
        input_per_1m: 5.0
        output_per_1m: 25.0          # omitted fields keep the built-in value
  openai:
    model: gpt-5.4-mini
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
  max_size_mb: 0                   # 0 = unlimited; >0 evicts oldest entries past the cap
guard:
  secret_scan: true                # scan diff + rules for credential patterns before sending
  token_preflight: false           # opt-in: confirm/abort when the prompt overflows the model's context window
command:
  default: ""                      # args applied to a bare `commitbrief`; empty = `--staged`
commit:
  type: plain                      # default --type for `commitbrief commit` (plain|conventional|conventional+body|gitmoji|subject+body)
  generate: 1                      # default --generate (number of message alternatives)
```

### Default command (`command.default`)

A bare `commitbrief` reviews staged changes (`commitbrief --staged`). To
change that default, set `command.default` to the argument string you'd
otherwise type:

```yaml
command:
  default: --unstaged --cli gemini   # now `commitbrief` == `commitbrief --unstaged --cli gemini`
```

It applies **only** to the truly bare invocation. The moment you pass any
flag or subcommand — `commitbrief --staged`, `commitbrief --json`,
`commitbrief dry-run` — the default is bypassed and you get exactly what
you typed. Empty/unset keeps the built-in `--staged`. Tokens are
whitespace-split (no shell quoting).

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
review. Default TTL is 7 days; configurable via `cache.ttl_days`. Set
`cache.max_size_mb` (>0) to bound the on-disk cache: writes that push it
past the limit evict the oldest entries first (the entry just written is
never evicted). Inspect it with `cache stats` / `cache inspect <key>`.

**Can I run it in CI?**
The primary target is the developer's terminal, but the CI-friendly
pieces are in place: `--fail-on=<severity>` (or `--fail-on=any`)
returns a non-zero exit code when a finding meets or exceeds the
threshold, `--json` emits the structured-findings document machine-
readably, and `commitbrief install-hook` scaffolds a pre-commit /
commit-msg / pre-push hook locally. For pull-request CI there's the
[CommitBrief Review GitHub Action](https://github.com/CommitBrief/commitbrief-action)
(see "Continuous integration" above), or you can drive the binary
directly from any workflow.

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
