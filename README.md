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

### Upgrade

```sh
commitbrief upgrade          # check, confirm, install
commitbrief upgrade --check  # report only; install nothing
```

`upgrade` detects how the binary was installed and does the right thing for
it: Homebrew, Scoop and `go install` are handed to their own package
manager, while a manually installed binary is downloaded from GitHub
Releases, SHA-256 verified against the release checksums, and swapped in
place. If the binary's directory is not writable, nothing is downloaded and
the exact command you need is printed — CommitBrief never runs `sudo`
itself. Only the binary is replaced; bundled man pages are not installed.

This is the only network request CommitBrief makes on its own behalf, and
only when you run this command. There is no automatic update check.

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

# Narrow any scope with repeatable path filters (exact paths or globs)
commitbrief --unstaged --file app/Http/Controllers/API.php --file routes/web.php
commitbrief --unstaged --dir database/seeder --dir app/Models
commitbrief diff HEAD~3 HEAD --dir docs
commitbrief --staged --file '*.go'                 # gitignore-style glob (any depth)
commitbrief --staged --file 'internal/**/*.ts'     # anchored recursive glob
commitbrief --staged --exclude-file '*_test.go'    # denylist; wins over the includes
commitbrief --staged --dir internal --exclude-dir internal/cli

# Select the commits themselves — author, date window, message or branch name.
# Any of these walks history instead of reading the index, so they replace
# --staged/--unstaged rather than combining with them.
commitbrief --author alice --author bob            # either person's commits
commitbrief --author alice@example.com             # name or email, case-insensitive
commitbrief --start-date 2026-01-01                # on or after (inclusive)
commitbrief --end-date 2026-03-31                  # on or before (inclusive)
commitbrief --text payment                         # commit message OR branch name
commitbrief --committer carol --merges             # committer identity; keep merges
commitbrief --author alice --start-date 2026-06-01 --dir internal   # all combinable
commitbrief diff main..develop --author alice      # bound the walk to a range

# Audit for committed credentials — deterministic, no provider call
commitbrief leaks                            # tracked files + last 200 commits
commitbrief leaks --no-history               # working tree only, fast
commitbrief leaks main..HEAD --no-worktree   # exactly that range
commitbrief leaks --author alice --start-date 2026-01-01
commitbrief leaks --json | commitbrief guard --from-json -   # gate CI on it

# See the commit graph — and exactly what a filter selects
commitbrief map                              # the DAG, newest first
commitbrief map --author alice               # matches highlighted, rest dimmed
commitbrief map --branches                   # ahead/behind the base branch
commitbrief map main..develop --max-commits 50

# Plain-language change digest (read-only; no findings)
commitbrief summary                          # what's staged, grouped by area
commitbrief summary main...develop           # a range; uses the commit messages in it
commitbrief summary HEAD~3 HEAD -o NOTES.md  # write the digest to a file

# Commit message (writes to git, with confirmation)
commitbrief commit                           # suggest a message for the staged diff, then commit
commitbrief commit --type conventional       # pick a format (-t); see "commitbrief commit" below
commitbrief commit --generate 3              # offer 3 alternatives to choose from (-g)
commitbrief commit --yes                     # commit the first suggestion non-interactively

# Setup and rules
commitbrief setup [--local]                  # provider + API key wizard
commitbrief setup --alias[=cbr]              # install a shell alias for commitbrief (bash/zsh/fish/PowerShell/cmd)
commitbrief providers list|use|test          # switch active provider without re-running setup
commitbrief config show|get|set              # inspect / tweak the merged YAML config
commitbrief init [--force]                   # write COMMITBRIEF.md + OUTPUT.md template
commitbrief compress [--level=balanced] [--dry-run]  # shrink COMMITBRIEF.md (preview first if you want)
commitbrief doctor                           # health-check the pipeline
commitbrief install-hook [--hook=...]        # install a git hook that runs commitbrief
commitbrief upgrade [--check]                # check GitHub Releases and install a newer CommitBrief
commitbrief dry-run                          # pipeline preview; no API call
commitbrief list                             # command reference
commitbrief mcp                              # run an MCP server over stdio (agent review gate; see "MCP server")
commitbrief guard                            # gate a review against .commitbrief/policy.yml (see "Policy gate")

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
`--fail-on` still see the full set), `-f/--file` (repeatable; exact
path or gitignore-style glob), `-d/--dir` (repeatable; exact prefix or
glob), `--yes`, `--verbose`, `--quiet`, `--lang`,
`--provider`, `--model`, `--cli <claude|gemini|codex>` (shorthand for the
CLI-tool-backed providers; mutually exclusive with `--json` /
`--markdown`), `--with-context` (CLI providers only — let the host CLI
read project files beyond the diff to ground the review; see below),
`--allow-secrets` (acknowledge a flagged credential in
the diff), `--no-cost-check` (skip cost preflight),
`--show-prompt` (print the exact system + user prompt that would be sent,
then exit — no provider call, no cost; honours `--output`), `--no-flaky`
(skip the flaky-test detector below), `--sandbox-rerun[=N]` (opt-in
sandbox-rerun confirmation of flagged flaky tests; see below),
`--no-architecture` (skip
architecture-aware review; see below), `--update-baseline` /
`--no-baseline` (signal-control baseline; see below), `--color`,
`--timeout <duration>` (bound the whole run; see below),
`--ignore-unknown-config` (continue past a config key the schema
doesn't define instead of failing; see "Configuration" below). See
`commitbrief --help`.

### Timeouts (`--timeout`, `review.timeout`)

Every provider ships a built-in ceiling: the CLI-tool providers
(`claude-cli` / `gemini-cli` / `codex-cli`) kill their subprocess after 5
minutes, ollama's HTTP client after 5, and the Anthropic SDK refuses a
non-streaming request that could run past 10. On a large diff or a slow
local model that is exactly when the run dies.

`--timeout` replaces those ceilings for one run:

```sh
commitbrief --staged --cli claude --timeout 20m   # give the host CLI 20 minutes
commitbrief --staged --timeout 600                # bare integer = seconds
commitbrief config set review.timeout 15m         # make it the default
```

The value is a Go duration (`90s`, `10m`, `1h30m`) or a whole number of
seconds. It bounds the **whole command run** — diff acquisition, provider
call, render, and any time you spend at a confirmation prompt — and it is
the one knob that can *lengthen* a run, since a deadline alone can only
cut one short. Resolution is `--timeout` → `review.timeout` → the
provider's built-in, so `--timeout 0` restores the built-ins for a single
run. Expiring is a normal failure: exit code 1 with a message naming the
duration. It also applies to `doctor` (whose provider probes otherwise
fast-fail at 5 seconds) and `providers test`; on `commitbrief mcp` it
becomes a per-tool-call budget rather than a lifetime for the server.

### Flaky-test detection (deterministic, ADR-0022)

Before the model is called, a **static pre-pass** scans the added lines of any
changed **test** files for high-precision flakiness anti-patterns:

- **hard-coded sleeps / fixed waits** (`medium`): `time.Sleep`, `Thread.sleep`,
  `Task.Delay`, `asyncio.sleep`, `*.waitForTimeout`, numeric `cy.wait`,
  `usleep`, `sleep(<n>)`;
- **unseeded randomness** (`low`): `Math.random`, Python `random.*`, Go
  `math/rand`;
- **brittle selectors** (`low`, js/ts): absolute/positional XPath, CSS
  `:nth-child` / `:nth-of-type`, Cypress `.eq(<n>)`, Playwright `.nth(<n>)` —
  stable `data-testid` / role / attribute selectors are never flagged;
- **over-mocking** (`low`): a single test function that sets up more than five
  mocks/stubs (`jest.mock`/`spyOn`, `when(…).thenReturn`, `sinon.stub`,
  `patch(…)`, `gomock`/`.EXPECT()`, Mockery) — pinned to implementation, not
  behaviour;
- **time-dependent assertions** (`low`): a wall-clock read (`time.Now()`,
  `Date.now()`, `new Date()`, `datetime.now`, `System.currentTimeMillis()`)
  used directly in an assertion instead of an injected clock.

Matches merge into the normal findings, so they render, count toward
`--fail-on`, and `--copy` like any other finding — but they are **deterministic
and reproducible**: no model call, no JSON-schema change. On by default for the
API/mock providers; turn it off per-run with `--no-flaky` or persistently with
`review.flaky: false`. CLI-tool-backed plain-text providers are unaffected for
now.

**Sandbox-rerun confirmation (opt-in).** The rules above *infer* flakiness from
anti-patterns; sandbox-rerun *confirms* it by actually re-running a flagged test
in isolation N times and classifying it by the observed pass/fail mix:

- **mixed** pass + fail → **confirmed flaky** (the finding is kept and its
  suggestion notes the empirical confirmation);
- **all fail** → a **real failure**, not flakiness — the test is genuinely red,
  so the note says so plainly (don't quarantine it as a flake);
- **all pass** → **transient / resolved** — the flake did not reproduce, so the
  finding is **demoted to `info`** and won't trip a commit-stage `--fail-on`.

Confirmation requires a **double opt-in**: a positive `--sandbox-rerun[=N]` /
`review.sandbox_rerun` (bare flag uses N=5) **and** a non-empty
`review.sandbox_command` — either alone stays a no-op. `sandbox_command` is a
**list of argv elements** (never a shell string), each rendered as a Go
`text/template` over `{{.File}}` (repo-relative), `{{.Line}}`, and `{{.Test}}`
(the enclosing test function name), then executed directly with
`exec.CommandContext` — **no shell is invoked**:

```yaml
review:
  sandbox_rerun: 5
  sandbox_command: ["go", "test", "-count=1", "-run", "^{{.Test}}$", "./..."]
```

`config set review.sandbox_command` is rejected — hand-edit the config file
directly (`config get` prints it read-only). Each rerun attempt is bounded by
a 2-minute timeout (a hung test costs one attempt, not the whole review), and
a stderr notice names the **configured command template** — the un-rendered
argv, e.g. `-run ^{{.Test}}$` — once per review, before any per-test rendering
happens; this is the review path's first code-execution stage, so it is never
silent. The command runs against the **working tree**, not the staged
snapshot a review may be scoped to. `commitbrief mcp` and `commitbrief guard`
never run it, unconditionally, regardless of flag or config — an agent host
must not execute repository code unattended (ADR-0033 §6); the static
findings still return, unconfirmed.

**Sandbox-rerun is not cached.** The flaky pre-pass (and any sandbox-rerun
confirmation inside it) runs *before* the cache lookup, so its findings can
merge into both a cache hit and a fresh call. That means a cache hit does
**not** skip the rerun: re-running the same command against the same diff
still executes the configured command again, even though the review body
itself is served from cache and the footer reports `Saved`. Total cost scales
with the number of flagged findings — worst case `findings × N × 2 minutes`,
with no cap and no progress output while it runs.

**Test-name resolution is Go-only.** `{{.Test}}` resolves via `go/parser`
against `*_test.go` files; every other language resolves to no name, so that
finding **skips the rerun** (with a stderr warning) and keeps its bare static
signal. Python/JS/PHP/Java tests keep full static flaky detection — they just
never get sandbox-rerun confirmation. See ADR-0033 for the full execution-boundary
rationale.

Resolution also skips any file the Go toolchain would not compile on **this**
machine: a `//go:build` constraint that is not satisfied, a `_windows_test.go`
on Linux, anything under `testdata/`. That includes build tags your
`sandbox_command` itself supplies — a `-tags=integration` in your command does
not make an `//go:build integration` file resolvable, so those tests keep their
static finding and skip confirmation. The alternative would be naming a test
`go test` cannot select, which exits 0 and would report a flake as "did not
reproduce" without ever running it.

### Signal control: baseline + inline suppression (ADR-0027)

Three layers keep the noise down. `--min-severity` (above) is **display-only**.
The other two are **true removals** — a removed finding no longer counts toward
`--fail-on`, no longer appears in `--json findings[]`, and is hidden from the
rendered output:

- **Baseline (per-developer, gitignored).** On a brownfield repo, run
  `commitbrief --update-baseline` once to accept the current findings — it writes
  their fingerprints to `.commitbrief/baseline.json` and does **not** filter that
  run. Every later run then surfaces only **new** findings; the known ones are
  removed. The fingerprint is resilient to line drift (a finding that moves up or
  down the file stays baselined). The file is **never committed** — it lives under
  the already-gitignored `.commitbrief/`, so CI and a reviewer's gate apply no
  baseline and see everything (it can't be used to hide a real bug). Re-accept any
  time with `--update-baseline`; ignore the baseline for one run with
  `--no-baseline`; disable persistently with `review.baseline: false`.

- **Inline suppression (in committed source).** Silence one finding with a visible
  reason by adding a comment on the offending line — or the line directly above it:

  ```go
  result := db.Query(userInput) // commitbrief-ignore[high]: input is parameterized below
  ```

  Use `commitbrief-ignore: <reason>` to silence any finding on the line, or
  `commitbrief-ignore[<severity>]: <reason>` to silence only that severity. The
  comment prefix doesn't matter (`//`, `#`, `--`, `/* */` all work). Because the
  marker is in committed source, a reviewer sees it in the diff.

Neither filter is silent: optional `meta.baselined` / `meta.suppressed` counts
appear in `--json` (the schema stays v1) and a one-line `N baselined · M
suppressed` footer prints to stderr.

### Architecture-aware review (ADR-0030)

If your repo ships an `architecture.json` — the config of the sibling tool
[archlint](https://github.com/muhammetsafak/archlint), which deterministically
lints import-boundary rules — CommitBrief reads it and feeds a compact summary of
your declared **layers** and their **allowed / forbidden import edges** into the
review prompt. The reviewer can then flag a diff that crosses an architectural
boundary, e.g. an import that adds a `domain → db` dependency the architecture
forbids:

```json
{
  "layers": { "domain": ["internal/domain"], "db": ["internal/db"] },
  "rules":  { "domain": [], "db": ["domain"] }
}
```

(`rules` lists, per layer, the layers it is **allowed** to import; `[]` means it
may import no other layer.) This is a strict **one-way read** of archlint's public
config — CommitBrief never lints the import graph or enforces anything itself
(run `archlint check` in CI for the deterministic gate); it only grounds the LLM
so it can reason about the change. A missing or malformed file is a transparent
no-op that never breaks a review.

On by default; turn it off per-run with `--no-architecture` or persistently with
`review.architecture: false`. Point it at a non-default location with
`review.architecture_file`. Because the architecture summary folds into the
prompt, editing `architecture.json` automatically invalidates stale cached
reviews, while a repo without the file keeps a byte-identical cache key.

### Pre-commit framework (`.pre-commit-hooks.yaml`)

Already using [pre-commit](https://pre-commit.com)? Add CommitBrief to your
`.pre-commit-config.yaml`:

```yaml
repos:
  - repo: https://github.com/CommitBrief/commitbrief
    rev: v1.8.0
    hooks:
      - id: commitbrief        # language: golang — pre-commit builds the binary
      # - id: commitbrief-system   # or: use an already-installed binary on PATH
```

Both ids run the review gate on the staged diff (`--staged --fail-on=high`,
override `args:` to change the gate). This is **distinct from `commitbrief
install-hook`** (below), which writes a native git hook script and needs no
framework — pick whichever fits your setup. The hook runs non-interactively, so a
flagged secret aborts the commit (it never auto-confirms).

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

### `commitbrief summary`

Explain a set of changes in plain language — a short, human-readable digest of
what changed (and, when the commit messages make it clear, why), grouped by
logical area rather than file by file. Read-only; it produces **no findings**.

```sh
commitbrief summary                         # digest the staged diff
commitbrief summary --unstaged              # digest unstaged working-tree changes
commitbrief summary main...develop          # digest a range (git-diff passthrough, like `diff`)
commitbrief summary HEAD~3 HEAD             # digest the last three commits
commitbrief summary main...develop -o RELEASE.md   # write the digest to a file
commitbrief summary main...develop --lang tr       # Turkish digest
commitbrief summary --cli claude                   # use a host CLI tool as the backend
commitbrief summary --cli claude --with-context    # let the CLI agent read beyond the diff
```

Example output:

```text
Invoice Service: Rounding bug in fee calculation fixed. (a1b2c3d)
Auth: Token refresh flow added. (d4e5f6a)
DB: Index added to the invoices table. (f7a8b9c)
```

- **Scope** mirrors the review surface: no args ⇒ staged (default),
  `--unstaged` for the working tree, or positional `git diff` arguments for an
  arbitrary range — exactly like the `diff` subcommand. `--file` / `--dir`
  narrow it further.
- **For a range**, the commit messages in that range are taken into account and
  each line is attributed to the short commit hash(es) responsible. Staged /
  unstaged changes have no commits, so their lines carry no attribution.
- **Output is plain text;** `-o`/`--output` writes it to a file. `--lang` is
  honoured (e.g. `--lang tr`).
- **Provider selection** is identical to a review: `--provider` / `--model`, or
  `--cli claude|gemini|codex` to use a host CLI tool. With a CLI provider,
  [`--with-context`](#--with-context-cli-providers-only) lets the agent read
  files beyond the diff to ground the digest (it errors on an API provider).
- Reuses the pre-send `.commitbrief/**` guard, secret scan, and cost preflight,
  and is cached. Emits no findings, so `--json`, `--markdown`,
  `--suggest-commit`, `--fail-on`, and `--min-severity` are rejected. Never
  writes to git.

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

<!-- commitbrief:gen providers -->
10 providers ship in the box: 6 API, 1 local, 3 CLI-tool-backed. Context and
price are per model, not per provider. See the accompanying notes for what
it can't say (preview status, latency, auth, invocation).

| Provider | Kind | Model | Default | Context | $/1M in / out / cached |
|---|---|---|---|---|---|
| `anthropic` | API | `claude-opus-4-8` | ✓ | 1,000,000 | $5 / $25 / $0.5 |
| `anthropic` | API | `claude-sonnet-4-6` | — | 1,000,000 | $3 / $15 / $0.3 |
| `anthropic` | API | `claude-haiku-4-5-20251001` | — | 200,000 | $1 / $5 / $0.1 |
| `claude-cli` | CLI-backed | — | — | — | — |
| `codex-cli` | CLI-backed | — | — | — | — |
| `cohere` | API | `command-r-plus` | ✓ | 128,000 | $2.5 / $10 / = $2.5 |
| `cohere` | API | `command-r` | — | 128,000 | $0.15 / $0.6 / = $0.15 |
| `cohere` | API | `command-a-03-2025` | — | 256,000 | $2.5 / $10 / = $2.5 |
| `deepseek` | API | `deepseek-chat` | ✓ | 64,000 | $0.27 / $1.1 / $0.07 |
| `deepseek` | API | `deepseek-reasoner` | — | 64,000 | $0.55 / $2.19 / $0.14 |
| `gemini` | API | `gemini-3.1-pro-preview` | — | 1,000,000 | $2 / $12 / $0.5 |
| `gemini` | API | `gemini-3.5-flash` | ✓ | 1,000,000 | $1.5 / $9 / $0.375 |
| `gemini` | API | `gemini-3.1-flash-lite` | — | 1,000,000 | $0.25 / $1.5 / $0.0625 |
| `gemini-cli` | CLI-backed | — | — | — | — |
| `mistral` | API | `mistral-large-latest` | ✓ | 128,000 | $2 / $6 / = $2 |
| `mistral` | API | `mistral-small-latest` | — | 32,000 | $0.2 / $0.6 / = $0.2 |
| `mistral` | API | `codestral-latest` | — | 256,000 | $0.3 / $0.9 / = $0.3 |
| `ollama` | Local | `qwen2.5-coder:14b` | ✓ | 32,768 | free |
| `ollama` | Local | `qwen2.5-coder:7b` | — | 32,768 | free |
| `ollama` | Local | `llama3.3:latest` | — | 128,000 | free |
| `ollama` | Local | `llama3.2:latest` | — | 128,000 | free |
| `ollama` | Local | `deepseek-coder-v2:latest` | — | 8,192 | free |
| `openai` | API | `gpt-5.4-mini` | ✓ | 400,000 | $0.75 / $4.5 / $0.075 |
| `openai` | API | `gpt-5.5` | — | 1,050,000 | $5 / $30 / $0.5 |
| `openai` | API | `gpt-5.5-pro` | — | 1,050,000 | $30 / $180 / = $30 |
| `openai` | API | `gpt-4o` | — | 128,000 | $2.5 / $10 / $1.25 |
| `openai` | API | `gpt-4o-mini` | — | 128,000 | $0.15 / $0.6 / $0.075 |
<!-- commitbrief:end providers -->

Notes the table above can't carry — not derivable from `surface.json`, so
they stay hand-written here rather than inside the generated region:

- **OpenAI** — `gpt-5.5-pro` runs via the Responses API (not Chat
  Completions) and **can take several minutes per review**.
- **Google Gemini** — `gemini-3.1-pro-preview` is a **preview** model.
- **Anthropic** — ephemeral prompt caching (5 m TTL) cuts repeated input
  cost ~10×.
- **OpenAI** — automatic prompt caching kicks in at ≥1024-token prefixes.
- **DeepSeek**, **Mistral**, **Cohere** — OpenAI-compatible APIs: each
  reuses the `openai-go` client pointed at that provider's own base URL
  instead of a native SDK (see the credentials table below for
  `DEEPSEEK_API_KEY` / `MISTRAL_API_KEY` / `COHERE_API_KEY`). DeepSeek's
  JSON is prompt-driven rather than schema-enforced, so it degrades
  gracefully instead of failing hard on a malformed reply.
- **Ollama** — local only; `ollama pull` whatever model you like, the
  table above only lists what ships as a documented example. Its context
  windows are **best-effort**: a Modelfile can raise a model's `num_ctx`
  past what we advertise.
- **`claude-cli`** — subprocess of `claude -p -`; no API key on our side,
  it reuses your Claude Code subscription. `commitbrief --cli claude --staged`.
- **`gemini-cli`** — subprocess of `gemini -p`; no API key on our side, it
  reuses your Gemini CLI auth. `commitbrief --cli gemini --staged`.
- **`codex-cli`** — subprocess of `codex exec --sandbox read-only
  --skip-git-repo-check`; no API key on our side, it reuses your Codex CLI
  (ChatGPT) auth. `commitbrief --cli codex --staged`.

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

`--request-changes-on=<critical|high|medium|low>` **opts in** to a
request-changes verdict at or above that severity. **Without the flag the
verdict is never request-changes** — a clean or info-only PR is approved,
anything else is left as a plain `comment`. Inline comments are posted
either way. `--repo owner/repo` overrides git-context repo discovery.
Requires an API provider. `--fail-on` is ignored here — the GitHub verdict
replaces the exit-code gate.

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

## MCP server (agent review gate)

`commitbrief mcp` runs a **Model Context Protocol** server over stdio so an
AI agent or host (Claude Desktop, an agent runtime, an MCP-aware IDE) can call
CommitBrief as a tool — typically a **self-review gate the agent runs before it
submits code**. It speaks JSON-RPC 2.0 over the MCP stdio transport, is
stdlib-only (no MCP SDK, no new dependency), and is fully opt-in: it changes
nothing about the existing commands.

The server exposes one tool, **`review`**, which runs the *exact same review
pipeline* as `commitbrief --json` (diff acquisition, filtering, the pre-send
guard + secret scan, cost preflight, cache, the flaky-test pre-pass, and signal
control) and returns the **structured findings (JSON schema v1)** plus a short
text summary. It does not re-implement the review — it reuses `runReview`.

**Tool: `review`** — all arguments optional:

<!-- commitbrief:gen mcp-tool-args -->
| Argument | Type | Required | Description |
|---|---|---|---|
| `author` | `array<string>` | — | Review only commits authored by these people (matches name or email, case-insensitive). Selects a commit set instead of a staged/unstaged diff. |
| `committer` | `array<string>` | — | Review only commits committed by these people (matches name or email, case-insensitive). |
| `diff` | `array<string>` | — | Arbitrary `git diff` arguments to review a range instead of staged/unstaged changes, e.g. ["HEAD~3","HEAD"] or ["main...feature"]. Forwarded verbatim to git. |
| `dir` | `array<string>` | — | Review only files under these directories. A plain value is a \<dir>/ prefix; a glob value is matched gitignore-style. |
| `end_date` | `string` | — | Review only commits on or before this date, YYYY-MM-DD, inclusive. |
| `exclude_dir` | `array<string>` | — | Skip files under these directories or matching dir globs. Applied after `dir` so an exclusion wins. |
| `exclude_file` | `array<string>` | — | Skip these files or globs. Same matching rules as `file`, applied after it so an exclusion wins. |
| `fail_on` | `string` | — | Allowed: `critical`, `high`, `medium`, `low`, `info`, `any`, `none` — Report a gate failure (failed=true in the summary) when a finding meets/exceeds this severity. Findings are still returned regardless. |
| `file` | `array<string>` | — | Review only these files. A plain value is an exact path; a value containing \*/?/[ is a gitignore-style glob (e.g. "\*.go", "internal/\*\*/\*.ts"). |
| `max_commits` | `integer` | — | Cap how many matching commits enter the review (0 = the built-in default). Only meaningful alongside another commit filter. |
| `merges` | `boolean` | — | Include merge commits in a commit-filtered review (excluded by default). Only meaningful alongside another commit filter. |
| `min_severity` | `string` | — | Allowed: `critical`, `high`, `medium`, `low`, `info`, `none` — Hide findings below this severity in the returned set (display filter; the gate still sees the full set). |
| `model` | `string` | — | Override the configured model for this review. |
| `no_flaky` | `boolean` | — | Skip the deterministic flaky-test detector (ADR-0022). |
| `provider` | `string` | — | Override the configured provider for this review (e.g. "anthropic", "openai"). |
| `staged` | `boolean` | — | Review the staged diff (default true). Set false together with unstaged=true to review the working tree. |
| `start_date` | `string` | — | Review only commits on or after this date, YYYY-MM-DD, inclusive. |
| `text` | `string` | — | Review only commits whose message contains this text, plus commits unique to a branch whose name contains it (case-insensitive). |
| `unstaged` | `boolean` | — | Review the unstaged (working-tree) diff instead of the staged diff. Mutually exclusive with staged. |
<!-- commitbrief:end mcp-tool-args -->

The result carries two content blocks: a one-line summary (finding counts,
provider, and a `GATE FAILED` note when `--fail-on` trips) and the schema-v1
JSON document. A genuine failure (no repo/changes, provider error, or an aborted
secret-scan guard) comes back as an MCP tool error.

**Wiring it into a host.** Register `commitbrief mcp` as a stdio MCP server. For
a Claude Desktop-style host config:

```json
{
  "mcpServers": {
    "commitbrief": {
      "command": "commitbrief",
      "args": ["mcp"]
    }
  }
}
```

The host launches the process, performs the `initialize` handshake, discovers the
`review` tool via `tools/list`, and calls it via `tools/call`. The server reads
requests on stdin and writes responses on stdout until the host closes the stream;
diagnostics go to stderr. See [the MCP server wiki page](https://github.com/CommitBrief/commitbrief/wiki/MCP-server)
for the full handshake and a worked example.

## Policy gate (`guard`)

`commitbrief guard` is a **declarative merge gate**: it evaluates a review's
actionable findings against a `.commitbrief/policy.yml` and exits non-zero when
the policy is breached. It is richer than the single `--fail-on=<severity>`
threshold — a per-severity *budget* — and is aimed at gating high-volume (often
AI-authored) pull requests.

Create the policy (the gate is opt-in — no file means no gate):

```yaml
# .commitbrief/policy.yml
version: 1
thresholds:        # max findings allowed per severity (omit or ~ = unlimited)
  critical: 0
  high: 0
  medium: 5
  low: ~
total: 20          # optional overall cap
```

> **Sharing the policy with your team.** CommitBrief adds `.commitbrief/` to your
> `.gitignore` the first time it writes a cache entry, which also ignores the
> policy file. To version-control the gate, add a negation below that line:
>
> ```gitignore
> .commitbrief/
> !.commitbrief/policy.yml
> ```
>
> The baseline (`.commitbrief/baseline.json`) is deliberately **not** shared — it
> is per-developer, so it can never hide a finding from CI or a reviewer.

Then gate a change — two modes:

```sh
# run-mode: review the diff, then evaluate (reuses the full pipeline)
commitbrief guard                     # staged diff
commitbrief guard --unstaged
commitbrief guard --diff main...HEAD

# consume-mode: evaluate a review you already produced — no provider call
commitbrief --json --staged > review.json
commitbrief guard --from-json review.json
```

It evaluates the findings that survive **baseline + suppression** (signal
control) — exactly what `--json` shows. The exit code is **0 (pass)** or
**non-zero (blocked)**; a load/parse failure also blocks (a merge gate must not
pass when it cannot prove the change is within policy). `--json` emits a machine
verdict (`{passed, counts, total, violations}`). `guard` complements `--fail-on`
(the simple one-threshold gate) — use either or both. Rule-id-scoped allow/deny
lists are not yet supported (findings carry no stable rule id). See
[the guard wiki page](https://github.com/CommitBrief/commitbrief/wiki/Guard-command).

## Configuration

Two-tier YAML config with field-level merge:

- **User:** `~/.commitbrief/config.yml` — defaults that apply everywhere
- **Repo:** `./.commitbrief/config.yml` — overrides for this repo
  (gitignored by default; run `commitbrief setup --local` to write it)

Plus environment variables for credentials and runtime tweaks, and
CLI flags for one-off overrides (`--provider gemini --model
gemini-3.5-flash`).

<!-- commitbrief:gen env-vars -->
These are read AFTER config is loaded and merged, so each one below
always overrides its config value for this run — including one left
over in your shell from a previous session.

| Variable | Effect | Config key |
|---|---|---|
| `ANTHROPIC_API_KEY` | Anthropic API credential — overrides `providers.anthropic.api_key` in config. | `providers.anthropic.api_key` |
| `OPENAI_API_KEY` | OpenAI API credential — overrides `providers.openai.api_key` in config. | `providers.openai.api_key` |
| `GEMINI_API_KEY` | Google Gemini API credential — overrides `providers.gemini.api_key` in config. | `providers.gemini.api_key` |
| `DEEPSEEK_API_KEY` | DeepSeek API credential — overrides `providers.deepseek.api_key` in config. | `providers.deepseek.api_key` |
| `MISTRAL_API_KEY` | Mistral API credential — overrides `providers.mistral.api_key` in config. | `providers.mistral.api_key` |
| `COHERE_API_KEY` | Cohere API credential — overrides `providers.cohere.api_key` in config. | `providers.cohere.api_key` |
| `OLLAMA_HOST` | Ollama base URL — overrides `providers.ollama.base_url` in config UNCONDITIONALLY, even if you set base_url explicitly. | `providers.ollama.base_url` |
| `COMMITBRIEF_PROVIDER` | Selects the active provider for this run — overrides `provider` in config. | `provider` |
| `COMMITBRIEF_MODEL` | Selects the active provider's model for this run — overrides `providers.<name>.model` in config. | `providers.<name>.model` |
<!-- commitbrief:end env-vars -->

`COMMITBRIEF_CONFIG`, `NO_COLOR`/`COMMITBRIEF_NO_COLOR` and `LANG` affect the
CLI too, but they aren't config overrides `ApplyEnv` applies — they're read
directly where they're used, so they have no `Config key` to point at:

| Variable | Effect |
|---|---|
| `COMMITBRIEF_CONFIG` | Absolute path to the user-level config file; replaces the default `~/.commitbrief/config.yml` lookup. Useful for tests and ephemeral CI environments. |
| `COMMITBRIEF_NO_COLOR`, `NO_COLOR` | Force ANSI color off (overrides `--color always`). |
| `LANG` | No longer drives language (ADR-0021): language is config-driven (`--lang` → repo `output.lang` → user `output.lang` → English). |

<!-- commitbrief:gen config-schema -->
```yaml
# Schema reference generated from code: every settable key, its type,
# and its built-in default (when it has one). <name> and <model> are
# placeholders you choose — see "Providers and pricing" above for the
# real provider names.
cache:
  enabled: true                           # bool
  max_size_mb: 0                          # int
  ttl_days: 7                             # int
command:
  default: ""                             # string
commit:
  generate: 1                             # int
  type: "plain"                           # string
cost:
  warn_threshold_usd: 0.5                 # float
guard:
  injection_scan: true                    # bool
  secret_patterns:                        # []object
    - name: ""                            # string — no built-in default
      regex: ""                           # string — no built-in default
  secret_scan: true                       # bool
  token_preflight: false                  # bool
output:
  color: "auto"                           # string
  lang: "en"                              # string
  stream: true                            # bool
provider: "anthropic"                     # string
providers:                                # map[string]object — key is user-chosen
  <name>:
    api_key: ""                           # string — no built-in default
    base_url: ""                          # string — no built-in default
    model: ""                             # string — no built-in default
    pricing:                              # map[string]object — key is user-chosen
      <model>:
        cached_input_per_1m: 0            # float — no built-in default
        input_per_1m: 0                   # float — no built-in default
        output_per_1m: 0                  # float — no built-in default
review:
  architecture: true                      # bool
  architecture_file: "architecture.json"  # string — effective default; `config get` returns "" (auto-discovery applies this)
  baseline: true                          # bool
  flaky: true                             # bool
  sandbox_command: []                     # []string — no built-in default
  sandbox_rerun: 0                        # int
  timeout: ""                             # string
version: 1                                # int
```
<!-- commitbrief:end config-schema -->

What the schema above can't say — not derivable from `config_keys`, so
these stay hand-written here rather than inside the generated region:

- **`providers.<name>.pricing.<model>`** — optional: overrides the
  built-in $/1M rate table for that one model. Any field you omit keeps
  the built-in value; the `0` shown above is just this reference's
  placeholder, not a claim that the built-in price is zero. Example:
  ```yaml
  providers:
    anthropic:
      pricing:
        claude-opus-4-8:
          input_per_1m: 5.0
          output_per_1m: 25.0   # omitted fields keep the built-in value
  ```
- **`guard.secret_patterns`** — purely additive (ADR-0024): the built-in
  credential patterns always run and cannot be disabled through this
  field (set `secret_scan: false` to turn the whole scan off instead).
  Example entry: `{name: "Internal Service Token", regex: 'INT-[0-9]{10}'}`.
- **`guard.token_preflight`** — opt-in (default `false`); when on, a
  review whose estimated prompt tokens exceed the provider's context
  window prompts for confirmation (TTY) or aborts (non-TTY) before the
  paid round-trip, instead of letting the provider reject it.
- **`guard.injection_scan`** — on by default; **warns, never aborts**, if a
  non-default `COMMITBRIEF.md`/`OUTPUT.md` contains prompt-injection-shaped
  phrasing ("ignore previous instructions", etc.). Defense-in-depth
  visibility alongside the passive XML-wrap immutability guard (ADR-0025);
  the embedded defaults are trusted and skipped.
- **`output.lang`** — the AI review's OUTPUT language: any language name or
  code you write here is passed straight to the provider (e.g. `fr`). The
  CLI's own interface text only localizes for `en`/`tr`, independently of
  this setting.
- **`output.color`** — one of `auto` (the default, TTY-detecting),
  `always`, or `never`.
- **`cost.warn_threshold_usd`** — the estimated-cost ceiling (USD) above
  which a review prompts for confirmation (TTY) or aborts (non-TTY) before
  contacting the provider; `0` or negative disables the check. The default
  (`0.5`) is an "occasional dev review" budget — raise it for scheduled
  jobs, or pass `--no-cost-check` per run.

### Config strictness

A key `config.yml` has no field for — a typo like `regexp:` for
`regex:`, or a setting from a future version — is now a **hard error**
naming the offending file, the exact dotted key, the allowed siblings
at that level, and (when it's a plausible typo) a "did you mean"
suggestion. Before this, an unknown key was silently discarded during
load and simply had no effect.

That's a separate bug from the one this release also fixes in `config
set`/`providers use`'s write path (see the CHANGELOG) — writing to a
config file with fields it didn't mention used to reset every one of
them to its zero value, `guard.secret_scan: false` included, with no
unknown key needed to trigger it. Neither bug causes the other; they
share a root cause: nothing in the old pipeline distinguished "this
key isn't set", "this key is set to its zero value", and "this isn't
a real key at all."

A top-level key meant to hold only a YAML anchor (for `<<:` merging)
is exempt when prefixed `x-` (the same convention docker-compose and
OpenAPI use), e.g.:

```yaml
x-defaults: &defaults
  ttl_days: 3
cache:
  <<: *defaults
  enabled: true
```

`--ignore-unknown-config` downgrades the failure back to a warning for
one run — every ignored key is still named on stderr, and it still has
no effect, so this is a stopgap for "my config broke on upgrade and I
need to run something *now*", not a way to silence the check
permanently. Fix or remove the offending key instead.

`config set` and `providers use` write by patching the existing YAML
document in place rather than decoding it into `Config` and
re-marshalling the whole thing: comments, key order, anchors/aliases,
and any key the typed schema doesn't know about (including one an
`--ignore-unknown-config` run just skipped) all survive a write
untouched.

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

Two independent axes: **which commits** are reviewed, and **which files**
within them.

### Commit filters (ADR-0035)

`--author`, `--committer`, `--start-date`, `--end-date` and `--text` select a
set of commits. Setting any of them switches the scope from "the index" to a
history walk, so they cannot be combined with `--staged` / `--unstaged` — those
have no commits yet. `commitbrief commit` and `commitbrief remote pr` reject
them outright (the first describes the index; the second reads its diff from
`gh`, not local git).

| Flag | Matches |
|---|---|
| `--author` | author name **or** email, case-insensitive substring; repeatable, OR'd |
| `--committer` | committer name or email; repeatable, OR'd |
| `--start-date YYYY-MM-DD` | author date on or after this day (inclusive) |
| `--end-date YYYY-MM-DD` | author date on or before this day (**inclusive** — unlike git's bare `--until`) |
| `--text` | the commit message, **plus** commits unique to a branch whose name contains the text |
| `--max-commits N` | cap the selection (default 200); truncation is always reported |
| `--merges` | keep merge commits, which are excluded by default |

Different kinds are AND'd, multiple values of one kind are OR'd:
`--author alice --author bob --start-date 2026-06-01` means "(Alice or Bob)
**and** since June".

The revision range walked is `HEAD` by default, or the range you give a
subcommand: `commitbrief diff main..develop --author alice`. The resulting
diff is the **concatenation of the matching commits' patches**, not a
cumulative range diff — so a file changed in three of them appears three
times, and no unmatched commit's work leaks in.

Branch-name matching is best-effort by nature: a squash- or rebase-merged
branch no longer owns its commits, so nothing will be found for it.

### File filters

Three ignore layers, applied in order. Later layers win, so a `!pattern` in
`.commitbriefignore` can revert a built-in exclusion:

1. **Built-in defaults** — binaries, lock files, `vendor/**`,
   `node_modules/**`, generated code, build artifacts, IDE/OS noise.
2. **`.commitbriefignore`** at the repo root — gitignore syntax,
   team-shared.
3. **`COMMITBRIEF.md` semantic filter** — natural-language rules the LLM
   applies to whatever survives the first two layers.

On top of those, `--file` / `--dir` narrow to a path allowlist and
`--exclude-file` / `--exclude-dir` remove from it. All four share the same
matching rules (exact path or gitignore-style glob), and exclusion is applied
last, so it always wins.

`commitbrief dry-run` reports how many commits matched and how many files each
layer removed. `commitbrief map` shows *which* commits a filter selects — matches
highlighted, everything else dimmed as context — which is the fastest way to check a
filter before paying for a review.

## Finding committed credentials

The pre-send secret scanner is a gate: it sees the one diff about to be sent. It
cannot tell you whether a key is sitting in your tree right now, or whether one was
committed and later removed — and a removed key is still in the history, still
reachable in every clone.

`commitbrief leaks` answers both, with the same pattern set and no provider call:

```sh
commitbrief leaks                      # tracked files + the last 200 commits
commitbrief leaks --patterns           # what it looks for (built-ins + yours)
commitbrief leaks --fail-on none       # report without failing the build
```

It exits 1 on any hit, so it gates CI out of the box, and `--json` emits schema v1 —
so `commitbrief leaks --json | commitbrief guard --from-json -` enforces a
`.commitbrief/policy.yml` budget with no extra plumbing.

Findings report a **file, a line and the pattern names** — never the matched text. The
scanner reads whole files, so its own report must not become a second copy of the
secret.

Two limits worth knowing: it honors the ignore layers above, so a key inside
`vendor/**` is not reported; and it is regex-only, so a high-entropy blob with no
recognizable prefix is invisible. It is a targeted check, not a general-purpose
secret scanner.

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
