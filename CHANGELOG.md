# Changelog

All notable changes to **CommitBrief** are documented in this file.

The format follows [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

> Tags prior to **v0.4.0** were cut in the private repository and produced no
> public artifacts; the first publicly released version is v0.4.0.

## [1.2.1]

### Fixed
- **`commitbrief remote pr` no longer mis-places inline comments.**
  Comments are now anchored to the diff side each finding's line lives on
  — `RIGHT` (new file) for added/context lines, `LEFT` (old file) for
  removed lines — instead of unconditionally posting `side=RIGHT`. A
  finding whose line falls outside the diff (or whose POST GitHub rejects)
  is appended to the review summary under a "Findings that could not be
  attached to a specific line" heading rather than being silently dropped.
### Changed
- **Line-numbered diffs for more accurate finding locations.** Every
  review (local and `remote pr`) now sends the model a diff with each
  changed line prefixed by the line number a comment would anchor to
  (`<n>| <marker><text>`), so the model copies line numbers instead of
  counting them from the `@@` hunk header. This sharply reduces findings
  landing on the wrong line (closing braces, blank lines). The on-disk
  cache is rebuilt once on upgrade because the system prompt changed; the
  diff component of the cache key is unaffected (the numbered form is a
  deterministic function of the plain diff).
- **`remote pr` suggestion lines are prefixed with `💡`.** The remediation
  line in both inline comments and the review-summary fallback now starts
  with `💡 ` so it reads distinctly from the description.

## [1.2.0]

### Fixed
- **`commitbrief remote pr` no longer requests a non-existent `gh` JSON
  field.** `gh pr view --json` has no `baseRepository` field, so the PR
  fetch failed with `Unknown JSON field: "baseRepository"` against every
  real `gh` version. The base repository slug used for posting inline
  comments is now derived from the PR's `url` field (which always points
  at the base repo, including cross-fork PRs).
### Added
- **Per-model pricing override (OQ-09).** `providers.<name>.pricing.<model>`
  in config overrides the built-in `$/1M`-token rate snapshot used by the
  cost preflight, the verbose footer, and cached-cost figures. Fields:
  `input_per_1m`, `output_per_1m`, `cached_input_per_1m`; zero/omitted
  fields fall back to the built-in value (partial override OK). Edited in
  the config file and shown by `commitbrief config show`. Useful when the
  hard-coded snapshot drifts or for a negotiated rate.
- **CI integration: the `commitbrief-action` GitHub Action.** A separate
  repo (`CommitBrief/commitbrief-action`) ships a composite action that
  runs CommitBrief on pull requests — either posting inline review
  comments + a verdict (`comment` mode, via `remote pr`) or running an
  exit-code gate (`gate` mode, via `diff --fail-on`). The README's new
  "Continuous integration" section documents usage; no CLI change.
- **Three new providers: DeepSeek, Mistral, Cohere.** Each is a standalone
  provider package reusing the `openai-go` SDK pointed at the provider's
  OpenAI-compatible endpoint — **no new dependency** (DeepSeek
  `api.deepseek.com`, Mistral `api.mistral.ai/v1`, Cohere's
  `compatibility/v1`). API keys via config or `DEEPSEEK_API_KEY` /
  `MISTRAL_API_KEY` / `COHERE_API_KEY`; all three appear in
  `commitbrief setup`. Structured output is prompt-driven (no
  `response_format`) since these providers' strict-JSON support varies —
  the retry-once-then-degrade pipeline (ADR-0014) covers non-conforming
  output, same as Ollama. Total live providers: **9** (4 API + these 3 +
  2 CLI-backed).
- **`--suggest-commit`.** After the review, makes a second free-form
  provider call and prints a single Conventional Commit message for the
  staged diff to stdout. Read-only — it suggests, never writes git
  (NG4-safe). Requires the staged scope (`--staged` or the default run);
  rejected with `--unstaged`, the `diff` subcommand, and
  `--json`/`--markdown`/`--output`. Works with every provider via the new
  additive `provider.Request.FreeForm`, which makes API providers
  (Anthropic / OpenAI / Gemini / Ollama) skip their structured-output
  enforcement for this one call. The suggestion itself is not yet cached
  (the review — the expensive call — still is). See ADR-0015.
- **`--min-severity=<level>` display filter.** Hides findings below the
  given severity in the rendered output (Cards, Markdown, `--copy`).
  `--json` stays complete (machine contract) and `--fail-on` always
  evaluates the full, unfiltered set — so CI gating is never weakened by
  a display filter. Accepts `critical|high|medium|low|info|none`; an
  invalid value errors before the provider call. Complements `--fail-on`
  (which governs the exit code).

## [1.1.0] - 2026-05-28

### Added
- **`commitbrief remote pr <ID>` — terminal-driven GitHub PR review**
  (ADR-0016, targeted at v1.1.0). Pulls a PR's diff via the `gh` CLI,
  runs the review pipeline, posts each finding as an inline review
  comment, and submits a verdict (approve / comment / request-changes).
  Subcommand-local `--request-changes-on=<critical|high|medium|low>`
  (default `critical`) sets the request-changes threshold; `--repo
  owner/repo` overrides git-context repo discovery. API providers only —
  `claude-cli` / `gemini-cli` are refused (no structured findings).
  Bot-mode: the pre-send guards auto-allow with a stderr warning instead
  of aborting; `--fail-on` is ignored (the GitHub verdict replaces the
  exit-code gate). Race-safe: one retry if the PR head moves during the
  review, then abort. GitHub-posted text is fixed English; local
  stderr is localized (EN/TR). New `internal/remote` package +
  `remote.*` catalog keys.
- **Trailing blank line after review output** and **`----` brackets
  around CLI-provider (`claude-cli` / `gemini-cli`) output** for
  readability.

## [1.0.0] - 2026-05-28

### Migration guide (v0.x → v1.0)

The v1.0.0 line is the **API freeze checkpoint**. CLI flag surface,
JSON schema v1, and the `COMMITBRIEF.md` / `OUTPUT.md` formats follow
strict semver from here on — breaking changes wait for v2.x. If
you're upgrading from anywhere on the v0.x line, the moves below are
the ones that matter:

- **Scope flags replaced with `commitbrief diff`.** `--commit
  <hash>`, `--branch <name>`, and `--pull-request <range>` were
  collapsed into the single `diff` subcommand in v0.9.0, which
  forwards verbatim to `git diff <args>`. Rewrites:

  | v0.x                                 | v1.0                                |
  |--------------------------------------|--------------------------------------|
  | `commitbrief --commit abc123`        | `commitbrief diff abc123`            |
  | `commitbrief --branch main feature`  | `commitbrief diff main feature`      |
  | `commitbrief --pull-request main...feature` | `commitbrief diff main...feature` |

  `--staged` and `--unstaged` are unchanged.

- **`--yes` no longer bypasses the secret scanner or cost preflight.**
  Use the dedicated flags: `--allow-secrets` to acknowledge a
  credential-shaped string in the diff, `--no-cost-check` to skip
  the preflight prompt. CI scripts that wired `--yes` in for both
  guard prompts AND cost approval need to opt in to each
  explicitly now. (UC-01, UC-06, shipped v0.9.1)

- **`cache.max_size_mb` config key removed.** It was defined in the
  struct but nothing ever read it; the cache only honors `enabled`
  and `ttl_days`. Setting it now errors as an unknown field. Remove
  the line from your config; eviction is TTL-based. (UC-02, shipped v0.9.1)

- **Locale surface is now strictly `{en, tr}`.** Pre-v0.9.2 the
  config accepted any ISO code; `i18n.Load` silently fell through
  to English for everything except `tr`, leaving the dry-run footer
  claiming "Lang: Deutsch" while the output was actually English.
  Now `output.lang: <unsupported>` is coerced to `en` while the
  Resolution's `Source` is preserved. (UC-09, shipped v0.9.2)

- **`Diff.IsMerge` field removed.** The merge-commit warning was
  retired with the scope-flag collapse — `commitbrief diff
  <merge-sha>` gives first-parent semantics with no special prompt
  (same as `git diff <merge-sha>`). Library consumers reading the
  field need to drop it. (UC-20, shipped v1.0.0-rc.1)

JSON schema v1 (`{schema, content, findings, summary, meta}`) is
unchanged across the upgrade — anything piping `--json` to a tool
keeps working. The `findings[].suggestion` field is required from
v0.9.0 onwards; older parsers that treated it as optional still work
since the field is always populated, but new parsers should rely on
it being present.

### Changed
- **CLAUDE.md documents the `make check` post-development gate.** New
  hard rule: any Go change under `commitbrief/` must pass
  `cd commitbrief && make check` — gofmt drift + `go vet` +
  `golangci-lint` + `go test` + `release-check` + `i18n-check` — before
  being declared push-ready. Catches the lint regressions that used
  to surface only after CI ran.

- **`Diff.IsMerge` field and `cli.warn.merge_commit` catalog key
  removed.** The merge-commit warning was retired with the
  `--commit` / `--branch` / `--pull-request` scope flags in v0.9.0
  (`commitbrief diff <merge-sha>` now gives first-parent semantics
  with no special warning, matching git itself). The struct field +
  per-backend computation were dead code; both backends drop the
  `rev-list --parents` / `commit.NumParents()` probe. CLAUDE.md's
  CLI-scopes note updated to reflect the post-v0.9.0 surface. (UC-20)

### Added
- **README "Stability" section.** Declares the v1.0.0 API freeze
  scope: CLI flag surface, JSON schema v1, `COMMITBRIEF.md` /
  `OUTPUT.md` formats, public config keys — all under strict semver
  from v1.0.0 onwards. Links to the upgrade-from-v0.x migration
  guide in CHANGELOG.

- **BENCHMARKS.md baseline snapshot.** Captures the diff-pipeline
  and cache-hit numbers at the v1.0.0-rc.1 freeze point on the
  reference hardware (Apple M4 Pro, Go 1.25, darwin/arm64). Used
  as a regression detector — a future 2× slowdown is the trigger
  for an investigation. Not auto-failed in CI (cross-runner CPU
  variance generates too much noise).

- **CHANGELOG migration guide (v0.x → v1.0).** Single section
  collecting every breaking change since the v0.9.x line started:
  scope-flag collapse, `--yes` scope narrowing, `cache.max_size_mb`
  removal, locale narrow, `Diff.IsMerge` removal. Linked from
  README.

- **README command-surface refresh.** Adds `init --force`,
  `compress --dry-run`, `--cli`, `--allow-secrets`, and
  `--no-cost-check` to the Global flags list and command table.
  The `--provider-agnostic` bullet at the top now mentions
  `claude-cli` / `gemini-cli` alongside the four API providers.

- **New `make check` target.** Runs every guard CI runs, in CI order,
  bailing on the first failure. Single entry-point for "is this
  push-ready?" — see CLAUDE.md hard rules.

- **gosec security scan + `make security-check`.** Static security
  analysis runs on every push to main and on a weekly schedule
  (`.github/workflows/security.yml`). Local devs get the same wrapper
  via `scripts/security-scan.sh` so a finding surfaces identically
  in both contexts. The exclusion set (G304/G306/G301/G204/G101/G122)
  is documented inline with one-paragraph rationale per rule —
  reviewed once during the v1.0.0-rc.1 audit, revisit if the
  codebase grows new privilege boundaries. Real high-confidence
  findings (G115 etc.) stay enabled and fail the scan.

- **`claude-cli` and `gemini-cli` providers promoted to stable.**
  README now documents both alongside the four API providers; the
  v0.9.0 "experimental" disclaimer is gone. The plain-text emit
  pipeline (UC-07, UC-22, UC-23, UC-24 in v0.9.2) closed the last
  reliability gaps — `--output` routes correctly, the host CLI's
  version is memoised + bounded, and the prompt transport for
  claude-cli switched to stdin so ARG_MAX is no longer a ceiling.

- **`COMMITBRIEF_CONFIG` environment variable documented.** Setting
  it to an absolute path replaces the default
  `~/.commitbrief/config.yml` lookup — useful for ephemeral CI
  environments and reproducible tests. The variable has been live
  for ages; only the README was missing the entry. (UC-12)

### Fixed
- **Gemini provider hardens int→int32 conversion for max-output
  tokens.** `req.MaxTokens` (plain int) used to be cast directly to
  the SDK's int32 parameter; a value above `math.MaxInt32` would
  silently wrap to negative. Now bounded to `[1, math.MaxInt32]`
  with the default falling back to 4096. Found via gosec G115 during
  the v1.0.0-rc.1 security audit.

- **`KeyMeta.DiffHash` and `KeyMeta.SystemPromptHash` now carry real
  SHA-256 digests.** Pre-v1.0.0-rc.1 the diff hash stored the first
  16 hex chars of the composite cache key (NOT a diff hash) and the
  system-prompt hash was always empty. Both fields now match what
  `docs/03-configuration.md` advertises — full
  `sha256:<64-hex>` digests over their respective inputs. Useful
  when debugging cache-key drift. (UC-26)

- **Generated git hooks embed the absolute path to commitbrief.**
  macOS GUI git clients (Tower, GitHub Desktop, Fork, JetBrains IDEs,
  …) run hooks with a stripped `$PATH` that typically omits
  `/opt/homebrew/bin`, so `exec commitbrief --staged …` silently
  failed to launch. `install-hook` now resolves the running binary
  via `os.Executable` + `filepath.EvalSymlinks` and embeds the
  result as a single-quoted token. Survives `brew upgrade` (which
  swaps the keg symlink target). (UC-27)

## [0.9.3] - 2026-05-27

### Added
- **CLI splash logo on every run.** A 16×16 half-block rendering of
  the CommitBrief mark (the same gradient + arrow as the favicon
  and web logo), shown alongside the wordmark, tagline, and OSC 8
  hyperlinks to Home / Docs / GitHub / Sponsor / Author. Printed to
  stderr only — `commitbrief --json | jq` and `--markdown > file`
  stay uncorrupted — and gated on a TTY-capable stderr so redirected
  CI logs don't fill with raw 24-bit color escapes. The wordmark
  line embeds the resolved build version (`version.Version`), so it
  always matches the running binary.

## [0.9.2] - 2026-05-27

### Changed
- **CLI providers respect `--output`.** The plain-text emit path used
  by `--cli claude` / `--cli gemini` now routes through the same
  `openOutput` helper the structured renderers use, so `--cli claude
  --output review.md` writes to the file instead of silently dropping
  the destination and printing to stdout. (UC-07)

- **CLI provider prompt transport switched to stdin for claude-cli.**
  Combined system+user prompts on large diffs were hitting the
  platform ARG_MAX limit (~128KB on Linux/macOS), surfacing as
  `argument list too long`. claude-cli now invokes `claude -p -` and
  pipes the prompt via stdin, removing the size ceiling. gemini-cli
  stays on argv for now — upstream lacks a documented stdin
  shorthand; we'll flip it when there is one. New `clireview.Spec`
  field: `UseStdin bool`. (UC-24)

- **`DefaultModel` for CLI providers is now memoised + bounded.** The
  cache-key path queries `DefaultModel` on every review, which used
  to re-shell out to `<cli> --version` each time and could hang a
  pipeline behind a misbehaving host CLI. A `sync.Once` memo plus a
  5-second timeout cap the cost at one short subprocess per
  Backend. (UC-23)

### Added
- **`--cli` is mutually exclusive with `--json` and `--markdown`.**
  CLI-provider output is pre-formatted plain text; combining it with
  a structured renderer either re-flows the formatting we just paid
  the host CLI for or, worse, parses prose as JSON. Cobra now rejects
  the pairing before any provider call. (UC-07)

- **`dry-run` now reports output tokens, context window, and cost
  estimate.** The previous report stopped at the input-tokens
  estimate, which made it useful for "will this fit?" but useless
  for "what will this cost?". The new lines mirror the verbose
  footer of a real review and let users decide whether to fire the
  request without having to. (UC-19)

- **`commitbrief compress --dry-run`.** Runs the LLM compression
  call and prints the Result block (sizes, savings, per-review
  saved $, compression call cost) but does NOT replace
  COMMITBRIEF.md or write a backup. Useful for previewing how
  aggressive a level you actually want before committing to the
  rewrite. Mutually exclusive with `--out`. (UC-22)

### Fixed
- **`commitbrief diff` now accepts pathspecs and >2 args.** The
  subcommand used to cap at two positional args, which rejected
  legitimate `git diff <ref> -- <pathspec>` invocations (e.g.
  `commitbrief diff main -- '*.go'`). Cobra constraint relaxed to
  `MinimumNArgs(1)`; everything past the first arg is forwarded to
  `git diff` verbatim and git itself arbitrates validity. (UC-08)

- **`ui.EnableANSI` is now called from `Execute`.** On legacy Windows
  consoles the VT100 escape mode must be opted into before any ANSI
  codes are written; we shipped the helper but never invoked it at
  the entry point, so colored output landed as raw escape sequences
  on those terminals. POSIX builds keep the no-op stub. (UC-18)

- **Shared interactive stdin across the review pipeline.** Guard,
  secret scanner, and cost preflight used to each instantiate their
  own `bufio.Scanner` over `os.Stdin`. With multiple prompts firing
  in sequence on a single review, the first scanner's lookahead
  could swallow input meant for the next site — so piping
  `e\ne\ne\n` would only reach one prompt and the next would block
  or read empty. A single `*bufio.Reader` is now plumbed through
  all three sites. New helper: `readPromptLine`. (UC-21)

### Removed
- **Dead i18n keys cleaned up.** `setup.test.*`, `setup.scope.*`,
  `cli.error.{no_repo,no_provider,no_api_key,unsupported_lang}`,
  `guard.aborted`, `cache.hit`, `cache.disabled`, `common.cancelled`,
  `init.exists`, and `review.pr_format` had no Go source references
  — left over from earlier revisions where the surfaces were
  rewritten without trimming the catalog. EN ↔ TR parity preserved
  via `release-check.sh`. New CI guard
  (`scripts/i18n-deadkey-check.sh`, `make i18n-check`) fails on the
  first unreferenced key so the catalog can't grow stale again.
  (UC-25)

- **Locale surface narrowed to `{en, tr}`.** The `langNames` map used
  to advertise 15 languages (`de`, `fr`, `es`, `it`, `pt`, `ja`,
  `zh`, `ko`, `ru`, `ar`, `nl`, `pl`, `sv`) for which we never
  shipped translations — `i18n.Load` silently fell through to
  English, leaving the dry-run footer claiming "Lang: Deutsch" while
  the actual output was in English. Resolve now coerces any
  unsupported code (`output.lang: de`, `--lang fr`, `LANG=es_ES`) to
  `en` while preserving the original `Source` for attribution. New
  exported helper: `lang.CoerceCLIFlag`. (UC-09)

## [0.9.1] - 2026-05-27

### Changed
- **`--yes` no longer bypasses the secret scanner or cost preflight.**
  Previously, setting `--yes` (intended to auto-answer the
  `.commitbrief/` pre-send guard) also silently approved any flagged
  credential and any above-threshold cost estimate. That's a footgun:
  users routinely wire `--yes` into CI to skip the guard prompt, and
  it shouldn't also nuke unrelated safety nets. The dedicated
  bypasses (`--allow-secrets` for the scanner, `--no-cost-check` for
  the preflight) remain. (UC-01, UC-06)

- **Active provider doctor check.** `commitbrief doctor` now verifies
  that the *currently selected* provider (`config.provider`) has its
  own credentials — not just that *some* provider does. Closes a gap
  where setting `provider: openai` while only `anthropic.api_key` was
  configured would pass doctor but fail every review. (UC-03)

- **Localised confirm vocabulary, guard prompt, and setup wizard.**
  The catalog now drives accept-vocabulary (`y/yes` in EN, `e/evet`
  in TR), the `[y/N]` / `[e/H]` suffix, the `.commitbrief/` guard
  warning header and detail, and every label in `commitbrief setup`.
  EN remains the default; TR users get fully translated prompts.
  (UC-14, UC-15, UC-16)

### Added
- **Rules content secret scan.** The pre-send secret scanner now
  inspects user-authored `COMMITBRIEF.md` and `OUTPUT.md` content in
  addition to the diff itself. Rules join the system prompt verbatim,
  so a credential pasted into either file would leak just as surely
  as one in a diff. Embedded defaults are presumed-clean and skipped.
  New public API: `guard.ScanText`. (UC-05)

- **`cache.enabled` and `cache.ttl_days` are now honored.** Previously
  defined but inert. Setting `cache.enabled: false` skips the on-disk
  store entirely (no Get, no Put, no orphan directory). `ttl_days`
  passes through to `cache.Options.TTL` for normal expiry math. (UC-02)

### Fixed
- **`install-hook --hook=pre-push` now ships a real pre-push body.**
  Previously every hook variant got the same `commitbrief --staged`
  invocation, which silently no-op'd at push time (the index is
  typically clean when you push). The new pre-push script parses
  git's per-ref stdin protocol and runs `commitbrief diff
  <remote-sha>..<local-sha> --fail-on=critical --quiet --no-cost-check`
  for each ref being pushed, skipping deletions and reviewing the
  tip commit for brand-new branches. The push is blocked on the
  first critical finding. (UC-04)

- **`init` no longer aborts on the first existing file.** Re-running
  `commitbrief init` (or running it on a repo with a customised
  COMMITBRIEF.md from a pre-v0.6 install that pre-dated OUTPUT.md)
  used to error out before the second artefact was attempted, which
  meant a partially-initialised repo could never reach the
  fully-scaffolded state without first overwriting the customised
  file with `--yes`. Existing files are now skipped with a per-file
  log line and the missing sibling is still written. (UC-17)

- **`init --force` is now a real flag.** Previously the docs
  promised it but the CLI returned "unknown flag"; users had to know
  to reach for the global `--yes` instead. Same semantic as `--yes`
  for init's overwrite check; `--yes` continues to work for
  back-compat. Long form only — `-f` is already bound globally to
  `--file`. (UC-28)

### Removed
- **`cache.max_size_mb` config field.** Defined in the struct and
  surfaced via `config get/set`, but no code ever read the value —
  cache eviction is TTL-based, not size-based. Setting it now errors
  as an unknown field; reading it errors the same way. (UC-02)

## [0.9.0] - 2026-05-27

### Changed
- **Diff aggregate caching.** `Diff.AddedLines()` / `DeletedLines()`
  switched from O(N) live traversal to O(1) reads of fields populated
  once during `Parse` / `Filter` / `KeepPaths`. The review pipeline
  was calling each method 2–3× per run (info line, render meta,
  cache-hit + fresh branch); on large diffs the redundant walks
  showed up in profiles. New `countLineKinds` helper does both
  totals in a single pass and feeds the memo. Per-call cost is now
  zero; total construction cost is unchanged (we'd walk the tree
  anyway during Parse).

- **Hoisted `Diff.String()` to a local variable** in `runReview` and
  `dryRun`. The string was being recomputed 3× (secret scan, prompt
  builder, cache key) — each call rebuilt the whole diff text from
  the file / hunk / line tree. On a 5 MB diff that's 15 MB of
  throwaway allocations per review. Now built once, passed by value
  to the consumers. No API change.

- **Pre-split path parts on `FileDiff`.** Added `FileDiff.PathParts`
  and `OldPathParts` populated once during `parseDiffHeader`. The
  ignore matcher previously called `strings.Split(path, "/")` on
  every Match call, which fired per-file × per-filter-layer (built-in
  ignore + repo `.commitbriefignore` = 2 layers). New
  `Matcher.MatchParts(parts []string)` takes the pre-split slice
  directly; `diff.shouldExclude` uses it. On a 500-file diff,
  ~1000 redundant slice allocations gone.

- **Shared token-estimate heuristic.** Created `internal/tokens`
  with a single `Estimate(s string) int` function (chars/4 round-up).
  Replaces 6 inline `(len(s) + 3) / 4` copies in
  `internal/diff/tokens.go`, `internal/compress/compress.go`,
  `internal/provider/{anthropic,openai,gemini,ollama,mock,clireview}/`.
  Cache keys / cost preflight / context-window gate now share one
  source of truth — drift between providers can no longer give the
  same string different "sizes". Leaf package with zero deps; no
  import-cycle risk.

  Sourced from a Gemini-authored optimization review of the
  codebase. Two of Gemini's findings deferred to v1.x: the
  template-parse-per-render finding (not actually hot — render runs
  once per CLI invocation) and the remaining filter-layer
  optimizations (fires 1–2× per review, low ROI vs. effort).

### Added
- **Per-finding `suggestion` field — required actionable remediation.**
  Every finding now carries a 2–3 sentence concrete fix recommendation
  alongside the existing title/description. Rendered:

  - **Cards** — chevron-prefixed paragraph below the diff strip
    (`→ Switch to a prepared statement …`), `cardWhite` foreground so
    it reads as the actionable next step distinct from the muted
    description above. Continuation lines indent to align under the
    chevron's body column.
  - **Markdown** (`OUTPUT.md`) — `→ {{ .Suggestion }}` block after
    the snippet fence.
  - **JSON** (`--json`) — `"suggestion": "…"` field; required, fails
    `ParseFindings` if empty so the retry/degrade path fires instead
    of a hollow card.
  - **`--copy` clipboard payload** — chevron line at the bottom of
    each finding block, flattened to single-paragraph form so chat
    clients (Slack, Discord) don't mangle it.
  - **CLI providers** (claude-cli, gemini-cli) — prompted format
    includes the `→ Suggestion` line per finding; the host CLI emits
    it verbatim to stdout. Adjacent findings are now separated by a
    horizontal rule (`--------------------` on its own line between
    blank lines) so paste-into-chat output stays readable even
    without the box-drawing cards have. The rule is prompt-side
    (the model is instructed to emit it); CLI providers don't get
    the parse-and-reject enforcement the API path does, so format
    drift is possible — accept as v0.9.0 experimental limit.

  Prompt contract tightens the field expectations: concrete and
  specific (name functions, parameters, approaches), no
  generic-advice padding, no restating the description. The
  suggestion answers "what now?", not "what is wrong" — the latter
  is the description's job.

  Provider structured-output schemas all declare `suggestion` as
  required: Anthropic tools mode, Gemini ResponseSchema, OpenAI
  strict mode (strict mode rejects optional properties but CAN list
  required ones — so suggestion gets first-class enforcement
  there, unlike `language`/`snippet`/`line_end` which stay
  prompt-driven). BREAKING for any external consumer parsing the
  v1 JSON schema, but the change is on the producing-side contract
  the LLM is told to emit — schema version stays at 1 since this
  is additive in our schema-policy framing (the missing/empty case
  failing parse mirrors how missing `title`/`description` already
  behaved). New `internal/render/findings_test.go` cases
  (`TestParseFindings_SuggestionRequired`,
  `TestCardsPanelRendersSuggestionWithChevron`,
  `TestCopyTextIncludesSuggestionWithChevron`) pin the surface.

- **CLI-tool-backed providers — `claude-cli`, `gemini-cli`** (experimental).
  Drive the user's locally-installed Claude Code (`claude`) or Gemini
  CLI (`gemini`) as the review backend via subprocess, instead of an
  HTTPS API call. Two ways to select:

  ```
  commitbrief --cli claude --staged           # shorthand
  commitbrief --provider claude-cli --staged  # explicit (also visible in `providers list`)
  ```

  No API key needed when the host CLI is already authenticated. Cost
  is whatever the user's CLI subscription bills (Claude Pro, Gemini
  Advanced, etc.); per-token cost reporting is unavailable through
  subprocess so the verbose footer omits it.

  Implementation: new `provider.PlainTextEmitter` marker interface
  on the provider type; review.go branches on it to swap the prompt
  (`prompt.BuildPlainText` with a fixed plain-text response-format
  contract instead of the JSON contract), skip retry-once and the
  cards renderer entirely, and stream the host CLI's already-
  formatted output verbatim to stdout. New cache format marker
  `cache.FormatPlainText` so cached entries replay through the same
  verbatim-emit path with no `review.degraded` warning. Shared
  backend at `internal/provider/clireview/` (12 unit tests against
  fake shell scripts on POSIX); concrete adapters at
  `internal/provider/claude-cli/` and `internal/provider/gemini-cli/`.
  The `-cli` directory suffix is the deliberate developer-side
  signal that these go through a local subprocess, not the
  `internal/provider/{anthropic,gemini}/` HTTPS API packages — easy
  to confuse without it.

  Limits: agentic host CLIs don't expose native structured-output
  enforcement, so format adherence relies on prompt instructions.
  `--fail-on` is a no-op in CLI mode (no Findings struct to inspect);
  `--json` / `--markdown` are ignored (stdout is the CLI's plain
  text); `--copy` copies the verbatim output. Mutually exclusive
  with `--provider` at the cobra layer. Cache key includes the host
  CLI's reported version so upgrades cleanly invalidate prior
  entries.

- **Progress animation during the review pipeline** — four-stage tree
  with breathing-dot animation on the active stage and palette-aligned
  terminal states for completed/failed stages. Stages:

  ```
  ├─ ⏺ Searching for changes...     ← fetchDiff + parse + filter
  ├─   36 files +1233 -34            ← static info line (no glyph)
  ├─ ⏺ Preparing request...          ← rules / prompt / cache lookup
  └─ ⏺ Thinking...                   ← provider Review call
  ```

  Dot color cycles through the cards palette muted greys
  (`#3a3f4f → #6b7280 → #9CA3AF`) every ~1.1s. Finished stage → solid
  `#22d3a0` green (cardAddFg). Failed stage → solid `#ff6b8a` red
  (cardDelFg) with the error indented underneath. Retry-once
  (ADR-0014 §4) marks the first Thinking attempt as neutral muted
  (`#9CA3AF`) and starts a fresh `Retrying...` stage so the user
  sees the recovery.

  Three operating modes decided at construction: animated (TTY +
  colors), plain (non-TTY → `[start]/[done]/[info]/[fail]` lines for
  CI logs), silent (`--quiet`). `Pause()`/`Resume()` hand the
  terminal back to the cost-preflight prompt and the secret-scan
  prompt so animations never overdraw a `y/N` question. New
  `internal/ui/progress.go` (~300 LoC), 13 unit tests covering each
  mode + the breathing cycle, 2 integration tests pinning the
  pipeline emissions.

  `tryStructuredReview` gained an `onRetry func()` callback (nil-safe)
  so the CLI layer drives the Soft/Start transition without leaking
  retry semantics into the provider package.

- **`commitbrief diff <args...>` subcommand** — git-diff passthrough
  for reviewing arbitrary historic ranges. Args are forwarded verbatim
  to `git diff --no-color --no-ext-diff <args>` (1 or 2 positional
  args; cobra rejects 0 or 3+). Replaces the v0.8.x scope flags
  `--commit`, `--branch`, and `--pull-request` — anything those did,
  git's native syntax does:

  | Old                                     | New                                |
  |-----------------------------------------|------------------------------------|
  | `commitbrief --commit HEAD~1`           | `commitbrief diff HEAD~1`          |
  | `commitbrief --branch main`             | `commitbrief diff main`            |
  | `commitbrief --pull-request main...x`   | `commitbrief diff main...x`        |
  |                                         | `commitbrief diff HEAD~3 HEAD`     |

  `--file` / `--dir` path filters compose on top.

- **Global `--file` / `-f` and `--dir` / `-d` flags** — path filters
  applied post-parse, repeatable, work on any scope (`--staged`,
  `--unstaged`, `diff`). When neither scope flag is given, the path
  filters default to `--staged` semantics — `commitbrief --file x.go`
  ≡ `commitbrief --staged --file x.go`. Examples:

  ```
  commitbrief --unstaged --file app/Http/Controllers/API.php --file routes/web.php
  commitbrief --unstaged --dir database/seeder --dir app/Models
  commitbrief diff HEAD~3 HEAD --dir docs
  ```

  `--dir` matches by `<path>/` prefix on path-segment boundaries
  (`database/seed` does NOT match `database/seedother/*`). Renamed
  files match on either new path or `OldPath` so the user can refer
  to either side of a rename. New `diff.KeepPaths(d, files, dirs)`
  helper in `internal/diff/filter.go`.

- **`commitbrief cache prune` subcommand** — bounded cache cleanup
  with sensible defaults. No flags ⇒ `--keep-last 500 --older-than
  7d`: entries survive only when both windows are satisfied; ones
  beyond the newest 500 OR older than seven days get deleted.

  | Flag                  | Default | Behavior                                                          |
  |-----------------------|---------|-------------------------------------------------------------------|
  | `--keep-last <int>`   | `500`   | Keep the N newest entries (within the active filter scope).       |
  | `--older-than <dur>`  | `7d`    | Delete entries older than this. Units: d / w / m (30d) / y (365d).|
  | `--provider <name>`   | —       | Narrow the candidate pool to one provider; others stay untouched. |
  | `--model <name>`      | —       | Narrow the candidate pool to one model; others stay untouched.    |

  Provider/model are NARROWING filters: omitting them includes every
  entry; supplying them limits what the keep-last + older-than rules
  touch, so other providers'/models' caches are unaffected. Custom
  duration parser (`internal/cli/cache_prune.go`) accepts only
  `<int>[d|w|m|y]` — bare integers, decimals, negatives, mixed
  units, and stdlib `h/m/s` shorthand all reject, so off-by-one
  surprises are impossible.

- **Multi-line findings via `line_end` (schema-additive)** — Finding
  payloads can now carry a `line_end` integer alongside `line` to mark
  spans like a function body or a multi-statement block. When set and
  greater than `line`, every renderer (cards header, `--compact`,
  `--copy` clipboard payload, `--markdown` template) shows
  `file:start-end` instead of just `file:start`. Single-line findings
  keep emitting only `line` — the model is instructed to *omit*
  `line_end` rather than emit `line_end == line`. Backward-compatible:
  consumers that ignore the new field see the same `line` value they
  always did, no breaking change (JSON schema policy in
  `internal/render/json.go` explicitly permits additive Finding
  fields without a version bump). New `Finding.LineRef()` and
  `Finding.PathRef()` template methods so custom OUTPUT.md files can
  call `{{ .PathRef }}` instead of replaying the if/printf logic;
  the embedded default template was updated to demonstrate.
  Anthropic and Gemini structured-output schemas now declare
  `line_end`; OpenAI strict mode stays prompt-driven for the same
  reason `language` / `snippet` already do (strict mode rejects
  optional properties). Anthropic and Gemini tests cover the
  end-to-end round-trip.

### Removed
- **`--commit`, `--branch`, `--pull-request` review-scope flags**
  collapse into `commitbrief diff <args...>`. The three flags were
  three slightly-different wrappers around `git diff` shapes the user
  already knows by hand. Replacement table:

  ```
  --commit HEAD~1            →  diff HEAD~1
  --branch main              →  diff main
  --pull-request main...x    →  diff main...x
  ```

  BREAKING: scripts and aliases relying on the old flags must
  migrate. Path filtering (previously a side-effect of `--file`)
  is now a global `--file`/`--dir` filter that layers on any
  scope, so the cross-cutting cases work out of the box.

- **Single-path `--file` scope flag** replaced by the repeatable
  global `--file`/`--dir` filter pair (see Added above). The old
  shape — `commitbrief --file path/to/file.go` — still parses but
  now means "filter this file out of the staged review" rather
  than "fetch only this file's diff against HEAD". For the
  HEAD-vs-working-tree-single-file behavior, use
  `commitbrief diff HEAD --file path/to/file.go`.

- **`cli.warn.merge_commit` i18n key** (EN + TR) — the warning
  fired only on the `--commit <merge>` path, which no longer
  exists. `commitbrief diff <hash>` users see git's own first-parent
  semantics, which they already know if they're typing `git diff`
  syntax.

- **`Provider.ReviewStream` removed from the provider interface.**
  Every provider's `stream.go` (`internal/provider/{anthropic,openai,
  gemini,ollama}/stream.go`), `internal/provider/mock/mock.go`'s
  stream branch, the `Event` / `EventType` / `EventDelta` / `EventUsage`
  / `EventDone` / `EventError` types in `internal/provider/request.go`,
  and the `internal/ui/stream.go` consumer are gone. ADR-0014 took
  the review path off streaming in v0.6.0; the plumbing has been dead
  since then. BREAKING for third-party packages that imported the
  Provider interface (none known outside this repo). Re-introducing
  streaming for a future "thinking / trace" mode would mean writing
  a fresh adapter against each provider's SDK — the SDK methods
  themselves are still available. See ADR-0009's updated
  Supersession note. -3 stream.go files (~250 LoC), 11 streaming
  tests, 5 helper SSE-fixture servers, ~80 LoC of mock streaming
  state.

- **`internal/ui/stream.go`** (`Drain` channel consumer) — was the
  CLI-side counterpart to `ReviewStream`. Removed with it.

- **`internal/ui/spinner.go`** — the single-operation spinner primitive
  was never wired into any caller. Its role is now covered by the
  multi-stage `Progress` UI added in this release. Net deletion: the
  type + 2 tests.

### Changed
- **Tightened snippet contract** in the system prompt so findings
  stop showing irrelevant or invented code excerpts. The contract
  now spells out: (1) lines MUST be copied verbatim from the diff
  supplied — no paraphrasing, no edits, no invention; (2) max 6
  lines; (3) only the exact `-` / `+` / two-space prefixes are
  allowed; (4) no hunk headers (`@@ …`), no line numbers, no file
  headers; (5) emit a snippet ONLY when it materially clarifies the
  finding ("when in doubt, omit — an unhelpful snippet is worse than
  no snippet"). Prompt change only — same JSON contract, same
  renderer, no migration needed.
- **Severity chip glyphs swapped to emoji** for stronger visual cues
  across both the card panel header and the `--compact` line layout
  (and `--copy` clipboard payload, since it mirrors the chip label):
  - `⊘ CRITICAL` → `💥 CRITICAL`
  - `⚠ HIGH` → `🚨 HIGH`
  - `● MEDIUM` → `⚡ MEDIUM`
  - `○ LOW` → `📌 LOW`
  - `ℹ INFO` → `💡 INFO`

  Emoji are 2 cells wide (vs the 1-cell line-drawing glyphs they
  replace) but the fixed-width panel fill via lipgloss `Width()`
  absorbs the difference — corners still close cleanly. Tested with
  the long-line wrap regression to confirm the sign-column
  alignment in diff strips is unaffected.

### Added
- **`--copy` flag** — pushes a plain-text summary of the review
  findings onto the system clipboard via two complementary transports:
  OSC 52 escape (works over SSH; honored by iTerm2, kitty, WezTerm,
  Alacritty, Ghostty, recent xterm, tmux with `allow-passthrough`)
  *and* native shellout (pbcopy / wl-copy / xclip / xsel / clip.exe
  for terminals that ignore OSC 52, like macOS Terminal.app or Warp).
  Payload format ported verbatim from the maintainer's secguard
  prototype: `[<severity-label>] <path>:<line>\n<title>\n\n<description>`
  per finding, joined with `\n---\n\n`. Diff snippet deliberately
  omitted — chat clients mangle multi-line code blocks. OSC 52
  escape routes through stderr (not stdout) so `commitbrief --json
  --copy | jq` stays clean. A short hint line ("N findings copied to
  clipboard (OSC 52 + native) — paste anywhere") goes to stderr,
  suppressed by `--quiet` but the escape itself is never silenced —
  it's a terminal side-channel, not info text. New `internal/clipboard`
  package (transport) + `render.CopyText` / `render.BuildCopyPayload`
  (format). 3 EN/TR i18n keys, 12 unit + integration tests, ~330 LoC.

- **`commitbrief cache clear` subcommand** — removes the repo-local
  response cache directory (`<repoRoot>/.commitbrief/cache/`) and
  reports how many entries were deleted plus the disk space freed.
  Empty cache short-circuits with a "nothing to remove" message
  (exit 0). Without `--yes` and without a TTY stdin the command
  aborts safely — same non-interactive guard as `compress` and the
  pre-send write check. Useful after `compress` (system prompt hash
  changes guarantee a cache miss anyway) or when switching test
  config/provider. Mounted under a new `cache` parent so future
  inspection helpers can slot in without re-flattening the CLI
  surface. 5 EN/TR i18n keys, 3 integration tests, ~130 LoC.

## [0.8.1] - 2026-05-27

### Changed
- **Finding card design** ported verbatim from the maintainer's
  `./secguard/main.go` reference. Replaces the v0.8.0 visual layer
  end-to-end (hex codes, labels, layout, sizing heuristic). Each
  severity now ships its own dark theme — panel bg, border, accent
  color, chip label — sourced as-is:
  - `⊘ CRITICAL` on `#1A1116` / border `#602B38` / chip `#ff6b8a`
  - `⚠ HIGH` on `#1A1511` / border `#603F2B` / chip `#ffa86b`
  - `● MEDIUM` on `#1A1A11` / border `#5A5A2B` / chip `#f0d050`
  - `○ LOW` on `#11161A` / border `#2B4760` / chip `#6bb8ff`
  - `ℹ INFO` on `#11181A` / border `#2B5560` / chip `#6be0e0`

  Diff lines render as full-row strips: removed `#22141A` bg /
  `#ff6b8a` text, added `#111C1C` bg / `#22d3a0` text, context
  `#E5E7EB` on default bg. A faint `#3a3f4f` sign color de-emphasises
  the `-`/`+` char so the body reads as the signal.

  Borders are drawn manually (`╭ ╮ ╰ ╯ ─ │` painted on the panel bg)
  with a `\x1b[0m\x1b[49m\x1b[K` terminator on every row so colored
  backgrounds don't bleed past the card's right edge in wide
  terminals.

  **Fixed inner content width** of 96 columns (100 outer, including
  borders + side padding) — long LLM descriptions and titles wrap to
  panel-bg-filled continuation rows via lipgloss `Width()` instead of
  expanding the card past the terminal edge. Diff lines wrap the same
  way; truncation was rejected because code reviewers want to see
  whole lines even when the wrap break lands mid-statement. Earlier
  secguard `contentWidth + 24` heuristic dropped — it made each card
  a different size based on its content, which broke alignment in
  multi-finding views.

  **Long diff/snippet lines now wrap with sign-column alignment
  preserved.** Continuation rows of `-`/`+` lines no longer hug
  column 0 — they get a blank-bg pad equal to the sign-column width
  (`" -  " / " +  "` → 4 cols), and the strip background extends onto
  the wrapped row. Without this the card body lost its rectangular
  shape on wide source lines (long SQL, full URLs, multi-arg calls).
  Context lines wrap with matching whitespace indent. New
  `TestRenderDiffWrapsLongLinesWithSignAlignment` pins this.
  - **Border now blends with the panel background** via
    `BorderBackground(bg)`. The rounded corners (`╭ ╮ ╰ ╯`) previously
    sat on the terminal-default colour, which read as a dark gap
    between the border and the content area; now they share the
    severity-tinted bg and the card looks like one continuous block.
  - **More breathing room** — internal padding bumped from `(0, 1)`
    to `(1, 2)`. Title/description no longer crowd the border.
  - **File path and line number text now use the high-contrast
    `cardText` foreground** (white-ish on dark terminals, near-black
    on light) instead of the muted grey that was hard to read on the
    severity-tinted background.
  - **Diff lines in snippets are now full-width colored strips**, not
    just colored text. `-` lines paint a red background (ANSI 52 dark
    / 217 light) edge-to-edge of the content area; `+` lines paint
    green (22 / 151). White text on the strips matches the rest of
    the panel body and reads cleanly on both dark and light terminals.
  - **Rounded corners now pop visually** — same `RoundedBorder()` as
    before, but with the border-bg fix above the curves are no longer
    swallowed by surrounding terminal black.
  - **Code-fence noise removed.** Snippet rendering no longer wraps the
    excerpt in literal `` ```<language> `` ... `` ``` `` lines, which
    used to leak through as raw text in card output. The diff-coloured
    strips already mark the region as code; glamour markdown parsing
    isn't an option there since it would override the strip backgrounds.
    `TestCardsSnippetOmitsCodeFences` regression guard added.

## [0.8.0] - 2026-05-26

### Added
- **`commitbrief install-hook` subcommand** — one-command scaffold for
  a git hook that runs `commitbrief --staged --fail-on=critical
  --quiet --no-cost-check` on every commit. Default target is
  `.git/hooks/pre-commit`; `--hook` chooses `commit-msg` or `pre-push`
  instead. Existing files are refused unless `--yes` is passed (the
  prior content is backed up to `<name>.bak.<timestamp>` before
  overwrite). `--uninstall` removes the hook only when our embedded
  `# Generated by commitbrief install-hook` marker is present — a
  hand-written hook of the same name is never silently deleted. The
  uninstall path is idempotent on a missing file. 8 new i18n keys
  (EN + TR parity).
- **`--fail-on=<severity>` flag** — CI-actionable exit code gate. After
  rendering the review, the CLI compares the parsed findings against
  the requested threshold and exits 1 if any finding meets or exceeds
  it. Accepted values: `critical`, `high`, `medium`, `low`, `info`,
  `any` (one finding of any severity is enough), `none`/empty (off).
  Typos are rejected loudly. On graceful degrade (LLM produced
  unparseable output, no findings to evaluate) the check is skipped
  with a stderr notice — refusing to fail on missing data is the
  safer default for CI gates. The rendered review is printed *before*
  the exit code is decided, so the consumer can see which findings
  caused the failure.
- **Cost preflight** — before each fresh provider call (cache hits are
  skipped) the CLI computes the estimated cost from the prompt token
  count + the provider's per-model pricing. If the estimate exceeds the
  configured `cost.warn_threshold_usd` (default `$0.50`), the user is
  prompted on a TTY or the call aborts non-interactively. `--yes`
  bypasses with a one-line info notice; new `--no-cost-check` flag
  disables the preflight per-invocation. Output tokens are estimated
  conservatively (input/4, floored at 200, capped at 1500) so high-
  priced output models don't get systematically underestimated. 5 new
  i18n keys (EN + TR parity). Configurable end-to-end via
  `commitbrief config set cost.warn_threshold_usd <value>`.
- **Pre-send secret scanner** — before any LLM call, the diff is scanned
  for credential-shaped patterns and the user is prompted (or, in
  non-TTY contexts, the call aborts with a non-zero exit code). Eight
  patterns ship by default: AWS Access Key, GitHub Token, GitLab Token,
  OpenAI API Key, Anthropic API Key, JWT, Stripe Live Key, PEM Private
  Key. Only `+` prefixed (newly added) lines are scanned — context and
  removed lines never trigger a prompt. The warning surface lists line
  number + pattern name only; the matched substring is **never** echoed
  to stderr or any cached payload, so the scanner can't become a
  secondary leak vector via logs. New `--allow-secrets` flag bypasses
  with an info notice; `--yes` (the existing global) also bypasses.
  Disable entirely with `guard.secret_scan: false` in config. 6 new
  i18n keys (EN + TR parity).
- **`commitbrief doctor` subcommand** — single-command pipeline health
  check that runs ~7 checks against the resolved environment: git binary
  on PATH, config schema validity, COMMITBRIEF.md source, OUTPUT.md
  template validity (via `render.ValidateOutputTemplate`), active
  provider has credentials, cache directory writability, and the repo
  `.gitignore` includes `.commitbrief/`. Configured-with-key providers
  are pinged in parallel (5-second timeout each); ollama is only pinged
  when it's the active provider so the default `localhost:11434`
  base_url doesn't trigger a phantom probe. Per-check status uses
  green ✓ / yellow ⚠ / red ✗ glyphs; the trailing summary line counts
  OKs/warnings/failures. Exit code is 1 if any check fails (CI-safe).
  `--quiet` hides OK rows but always prints the summary. 12 new i18n
  keys (EN + TR parity).
- **`commitbrief list` config summary footer** — the command reference
  now ends with a `## Current configuration` section showing the active
  provider/model, the source of `COMMITBRIEF.md` and `OUTPUT.md` (path
  or "built-in default"), and the local cache footprint (entry count +
  total size). API keys are never printed here — `commitbrief providers
  list` is the path for that.
- **`--compact` flag** for one-line-per-finding rendering — useful when a
  review surfaces many findings and per-finding panels would dominate
  the terminal. Format per line: `[icon] SEVERITY • file:line — title`.
  Header/status/footer stay; description and snippet are omitted (the
  cost of density). Severity ordering (critical → info) matches the
  full panel layout so toggling the flag never re-shuffles findings.
  Empty case renders as a single `✓ No findings. Looks good.` line
  instead of the bordered success panel.

### Changed
- **Diff-colored snippets** — when a finding includes a code excerpt
  with `-` / `+` prefixed lines, the renderer now colors removals red
  (256-color 203, distinct from the critical-severity 196) and
  additions green (42). Context lines stay muted (244). Composes
  cleanly with the panel's severity-tinted background so the card
  still reads as one block.
- **High-contrast card text** — title and description text in finding
  panels (and the "No findings. Looks good." empty case) now use an
  adaptive foreground: near-white (256-color 255) on dark terminals,
  near-black (232) on light terminals. Fixes legibility against the
  severity-tinted backgrounds, which previously left the body text in
  the terminal default and could render as dark-on-dark or black-on-
  pastel depending on theme. Severity badge keeps its own color so
  urgency stays the eye's anchor.

## [0.7.0] - 2026-05-26

### Added
- **`commitbrief providers` subcommand** for multi-provider workflows
  without hand-editing YAML.
  - `providers list` — show every configured + registered provider, mark
    the active one, display the model and a masked API-key fingerprint
    (or the base URL for Ollama). Unknown / unconfigured providers stay
    listed so users can see what's available to set up.
  - `providers use <name>` — flip the active default provider. Touches
    only `provider:` in the config file; every API key, model, and base
    URL is preserved across the switch. `--local` writes to the repo
    config instead of the user-level one.
  - `providers test <name>` — call `TestConnection` against the named
    provider and report success + latency.
- **`commitbrief config` subcommand** for one-line edits and inspection.
  - `config show` — dump the merged config as YAML with API keys masked.
  - `config get <key>` — read a single field by dotted path (e.g.
    `providers.anthropic.model`, `cache.ttl_days`, `output.lang`).
  - `config set <key> <value>` — write a single field with type
    coercion and validation. Booleans accept `true/false/yes/no/1/0/on/off`
    (case-insensitive); integers are bounds-checked (no negatives for
    cache settings); `output.color` is enum-validated against
    `auto/always/never`; `provider` is validated against the registered
    factory list. `version` is rejected (managed by migrations).
- **i18n keys** for the new surface: `providers.list.*`,
  `providers.key.not_set`, `providers.use.*`, `providers.test.*`,
  `config.set.success` (EN+TR parity verified).

### Changed
- **Rich finding panels** — visual polish of the Cards Stage B layout
  introduced in v0.6.0:
  - **Rounded borders** (`╭ ╮ ╰ ╯`) replace the previous square corners
    for a softer, more card-like silhouette.
  - **Severity-tinted backgrounds** via `lipgloss.AdaptiveColor`: each
    panel gets a subtle background shade matching its border (darker on
    dark terminals, paler on light terminals) so the card reads as one
    block instead of disconnected text on the terminal default.
  - **Severity icons** prefix the badge — `‼` critical, `⚠` high,
    `▲` medium, `●` low, `ⓘ` info — colored to match the border. Text-
    variant Unicode rather than emoji so `NO_COLOR` users still get a
    visual anchor that's not dependent on hue.
  - **Bullet separator** (` • `) between the severity badge and
    `file:line` instead of the previous double-space, tightening the
    eye-grouping of "where + how severe".
- **`commitbrief --version` output** no longer double-prints
  "commitbrief version commitbrief X.Y.Z" — cobra's default version
  template was overridden so the line reads `commitbrief X.Y.Z (commit …, built …)`.

### Fixed
- **`commitbrief setup` no longer wipes previously-configured API keys.**
  Running setup a second time to add another provider (Anthropic → then
  OpenAI, say) used to overwrite the entire config file from defaults,
  silently destroying the first key. The wizard now loads the existing
  config at the target path (`--local` honoured) and layers the new
  provider's fields on top, leaving every other provider intact.
- **CI lint pass** — fixed 4 staticcheck/gofmt/errcheck issues that
  surfaced after the v0.6.0 push (no functional impact).

## [0.6.0] - 2026-05-26

### ⚠️ Breaking — OUTPUT.md semantics (ADR-0014)
- **`COMMITBRIEF.md` users are unaffected.** Project review rules remain
  the user-editable system prompt; no shape change there.
- **`OUTPUT.md` is now a Go `text/template` consumed locally**, not a
  format instruction embedded in the LLM prompt. The model produces
  structured findings JSON (`{ "findings": [...] }`) under a fixed
  schema; the renderer applies your OUTPUT.md to those findings for
  `--markdown` and `--output <file>.md`. Pre-0.6.0 OUTPUT.md files
  written as natural-language formatting instructions are invalid
  templates and will fail the pre-send validation guard.
- **Migration**: run `commitbrief init --yes` to overwrite your
  OUTPUT.md with the new embedded default, or rewrite it in
  `text/template` syntax. Available data: `.Findings` (typed
  `[]Finding{Severity, File, Line, Title, Description, Language,
  Snippet}`). Available helpers: `upper`, `lower`, `groupBySeverity`,
  `countFiles`. See `internal/rules/output.md` for the embedded
  default and `docs/json-schema.md` for the findings shape.
- **Severity vocabulary expanded to five levels** (was three):
  `critical`, `high`, `medium`, `low`, `info`. The renderer's `--json`
  output, the Cards layout's per-finding borders, and any `--fail-on`
  CI integration (v1.x roadmap) all consume this enum.
- **Old local cache entries are automatically invalidated** because the
  system prompt changed (cache key includes the system prompt SHA per
  ADR-0008). No migration code needed.

### Added
- **Structured findings JSON contract** between the LLM and the
  renderer. Every provider (Anthropic, OpenAI, Gemini, Ollama, mock)
  uses its native structured-output mechanism to enforce the schema:
  Anthropic via `tools` + `tool_choice`, OpenAI via
  `response_format: json_schema` (strict), Gemini via
  `ResponseMIMEType: application/json` + `ResponseSchema`, Ollama via
  `format: "json"`. See ADR-0014 §4.
- **Retry-once + graceful degrade** at the CLI layer. If the LLM emits
  unparseable output, the request is replayed once; if the retry also
  fails, the renderer degrades to a plain-text view with a stderr
  warning. Cache entries record the fallback mode (`format:
  "markdown-fallback"`) so replays stay silent.
- **Per-finding Cards layout** (Stage B of Phase 11). Each finding is
  rendered as a lipgloss-bordered panel coloured by severity
  (red/orange/yellow/blue/grey for critical/high/medium/low/info).
  Empty case shows a single green-checkmark "No findings. Looks good."
  panel. Footer summarises finding count + tokens + cost.
- **`render.ValidateOutputTemplate`** pre-send guard. User-supplied
  OUTPUT.md templates are parsed and executed against both an
  empty-findings and a two-element sample case before any provider
  call. A malformed template fails with a clear i18n'd error pointing
  at the file and suggesting `commitbrief init --yes` to reset.
- **i18n keys** `output.template.invalid` and `review.degraded`
  (EN+TR parity verified).
- **release-check.sh** now validates the embedded `internal/rules/output.md`
  template via the Go test suite before any release is cut.

### Changed
- `internal/render/json.go` populates `findings[]` from the parsed
  response. The `content` field is empty on the happy path and
  reserved for graceful-degrade output; slated for removal in v2.
- `internal/render/cards.go` body is replaced with per-finding panels;
  header/status/footer are unchanged from Stage A.
- `internal/render/markdown.go` runs OUTPUT.md as a `text/template`
  with helper functions; falls through to raw `Content` on graceful
  degrade or when no template is configured.

## [0.5.0] - 2026-05-26

Scope expansion. Every review scope advertised in `commitbrief list` now
works end-to-end; `--lang` becomes a first-class override with correct
source attribution; the JSON output is locked at schema v1 with a
documented contract and drift-guard golden test; user-facing action
messages flow through the i18n catalog (Turkish parity verified).

### Added
- **`--commit <hash>` review scope.** Backend (`internal/git.CommitDiff`)
  was wired in earlier phases; v0.5.0 finishes the surface with merge-
  commit handling (OQ-03, decision (b)): when the requested hash has
  two or more parents, the diff is taken against the first parent only
  and a stderr warning suggests `--pull-request <target>...<feature>`
  for full branch comparison. The `IsMerge` flag is set by both the
  go-git and CLI git backends; `runReview` and `dry-run` emit the same
  i18n-aware warning.
- **`--branch <target>` and `--pull-request <target>...<feature>`
  review scopes.** `BranchDiff` and `RangeDiff` were already wired in
  the dispatcher; this release adds the integration test coverage that
  was missing (`TestReviewBranchScope`, `TestReviewPullRequestScope`,
  `TestReviewCommitHappyPath`, `TestReviewCommitInvalidHash`,
  `TestReviewCommitMergeWarning`).
- **Mutually exclusive scope flags.** Passing two scope flags at once
  (e.g. `--staged --unstaged`) now fails before the pipeline runs, with
  cobra's mutex-group error message naming both offenders. Covers all
  six scopes: `--staged`, `--unstaged`, `--file`, `--commit`,
  `--pull-request`, `--branch`. `TestReviewMutuallyExclusiveScopes`
  guards it.
- **`lang.SourceCLIFlag`** — new Source enum value for D-21 chain step
  0 (CLI `--lang` flag wins over all other inputs). Previously the
  override was attributed to `SourceRepoConfig` in dry-run output,
  which was misleading; the new constant fixes the
  `Lang: tr (source: cli flag)` line. `TestDryRunLangFlagOverride`
  confirms end-to-end.
- **Drift-guard golden test for `--json` output.**
  `internal/render/testdata/json/v1.golden` is the byte-exact fixture;
  `TestJSONv1Golden` diffs each run's output against it. Any rename,
  type change, or removal trips the test. Use
  `go test ./internal/render -update` to regenerate intentionally.
- **`pickErrorCatalog()` helper** (`internal/cli/root.go`) — the
  top-level `Error:` prefix shown when a command fails before
  `appContext` is built now honors `--lang` and `LANG` env on a
  best-effort basis. Adds `common.error_prefix` key (EN/TR).
- **`.gitattributes`** — pins text files to LF in the working tree
  (`* text=auto eol=lf` + per-extension reinforcement) so byte-exact
  golden fixtures stop breaking on Windows CI with
  `core.autocrlf=true`.

### Changed
- **CLI user-facing strings routed through `i18n.Catalog.T()`.**
  Sixteen new keys cover the action paths: `init.exists`, `init.wrote`,
  `review.no_changes`, `review.cache_disabled`, `review.pr_format`,
  `compress.no_rules`, `compress.compressing`,
  `compress.aborted_larger`, `compress.wrote_out`,
  `compress.replace_prompt`, `compress.aborted_user`,
  `compress.backed_up`, `compress.wrote_compressed`,
  `setup.saved_local`, `setup.saved_global`, `common.error_prefix`.
  Turkish translations ship for every key; `TestKeyParity` (MustHave)
  stays green. Pragmatic scope: `%w` error wrappers (`stat %s: %w`,
  `mkdir %s: %w`, `provider %s: %w`) and tabular output (dry-run
  column labels, compress savings table) stay English by design.
- **`internal/render/json.go` `SchemaVersion` constant** carries the
  full versioning policy as a doc block: additive changes are not a
  version bump; renames, removals, or type changes require
  `SchemaVersion = 2` and a CHANGELOG entry. The shape itself is
  unchanged from v0.4.0: `{schema, content, findings, summary, meta}`
  with `findings` and `summary` reserved (always empty in v1).
- **`fetchDiff` signature** now takes the i18n catalog
  (`fetchDiff(repo, scope, cat)`) so the `--pull-request` format error
  can be translated. Internal-only API; no consumer impact.
- **`setup` command resolves `appContext` up front** even when
  `--local` isn't set, so the post-wizard `Configuration saved to …`
  line honors `--lang`. `resolveContext(false)` tolerates a missing
  repo, which is the normal case for global `commitbrief setup`.
- **OQ-03 (merge-commit handling) and OQ-24 (`--quiet` mode) marked
  ✅ RESOLVED** in the maintainer's open-questions log; both
  decisions are now reflected in code.

### Fixed
- **Windows golden-file test now passes** regardless of
  `core.autocrlf`. `TestJSONv1Golden` normalizes `\r\n` → `\n` in the
  on-disk fixture before comparison; `.gitattributes` (above) is the
  long-term hygiene.

## [0.4.0] - 2026-05-26

First publicly released version. Performance and savings: response
caching is fully wired, `commitbrief compress` lands as a feature, the
verbose footer surfaces token/cost/latency, and the release pipeline
(GitHub Releases + Homebrew + Scoop) goes live.

### Added
- **`commitbrief compress`** — full implementation (ADR-0010). Three
  embedded compression prompts (`light`, `balanced` default,
  `aggressive`) at `internal/compress/prompts/`. Pipeline: read
  `COMMITBRIEF.md` → wrap in `<user_rules>` (prompt-injection guard) →
  provider call → strip preamble/code-fence wrappers → display
  chars/tokens before/after + per-review savings + compression-call
  cost → ask `[y/N]` (or `--yes`) → backup to
  `.commitbrief/backups/COMMITBRIEF-<ISO-timestamp>.md` (Windows-safe,
  no colons) → atomic temp+rename. Refuses to apply when the result
  isn't smaller. `--out <path>` writes elsewhere without touching the
  original.

### Changed
- **Verbose footer** now labels cost differently on cache hits: `Cost:`
  becomes `Saved:` (no provider call was made; the figure is what would
  have been spent). The tokens line distinguishes the provider's own
  prompt cache (`provider cached: N`) from CommitBrief's local response
  cache (`local cache hit`).
- `commitbrief dry-run` now reports per-layer filter counts (`built-in
  ignore filtered: N`, `.commitbriefignore net filtered: M`) instead of
  a single aggregate. A negative `M` means a `!pattern` in
  `.commitbriefignore` reverted built-in exclusions.
- `commitbrief list` includes a "Filtering" section documenting the
  three-layer pipeline (built-in → `.commitbriefignore` →
  `COMMITBRIEF.md` semantic) with a worked `.commitbriefignore` example.
- **`README.md`** rewritten for the public release: Quick Start, install
  matrix (Homebrew/Scoop/`go install`/Releases), provider+pricing table,
  filtering pipeline, build-from-source.
- **Release pipeline goes live.** `.github/workflows/release.yml` drops
  `--skip=publish` from goreleaser; `.goreleaser.yaml`'s `brews` and
  `scoops` blocks reference `HOMEBREW_TAP_GITHUB_TOKEN` (PAT with `repo`
  scope) so cross-repo pushes to `CommitBrief/homebrew-tap` and
  `CommitBrief/scoop-bucket` succeed. The default `GITHUB_TOKEN` can
  only write to the source repo.

### Tests

Cumulative project test coverage raised from **64.7% → 77.8%** ahead of
the v0.4.0 cut. ~36 new tests landed across two focused passes; total
test count is now ~340.

- `scripts/smoke-test.sh` now exercises `.commitbriefignore` end-to-end:
  it stages a `go.sum`, confirms the built-in layer filters it, then adds
  `!go.sum` to `.commitbriefignore` and confirms the negative pattern
  reverts the built-in exclusion.
- 15 new **compress** tests (level parsing, embedded prompts non-empty,
  happy-path with fake provider, abort-when-larger, prompt-injection
  guard wrap, system-prompt selection, post-processing of preamble +
  code-fence wrappers, backup + atomic apply round-trip).
- 4 new **render** tests covering the verbose-footer cache-savings label
  (`Cost:` → `Saved:` switch on local cache hits).
- **21 new CLI integration tests** (`internal/cli` 14.7% → **64.3%**).
  Per-test isolated sandbox (`t.TempDir()` for HOME + repo, `t.Setenv`
  for `HOME`/`LANG`/`NO_COLOR`, mock provider registered once via
  `sync.Once`, package-level flag state reset, `git init -b main` +
  staged change), and cobra commands invoked via `SetArgs/SetOut/SetErr`.
  Covers: `init` writes both files / refuses overwrite / `--yes`
  override; `list` renders with Filtering section; `dry-run` full output
  + default scope; review happy path with mock provider; cache miss
  writes entry; cache hit on 2nd run shows `local cache hit`;
  `--no-cache` bypass; `--json` schema validates; `--markdown` no-ANSI;
  `--output <file>` redirects without polluting stdout; unknown
  provider returns wrapped `ErrUnknownProvider`; default-rules notice
  appears in dry-run; `--unstaged` scope; compress refuses without
  rules; unknown compress level rejected; `.commitbriefignore`
  exclusion + negative pattern revert; review outside git repo errors.
- **6 new provider streaming tests** — Anthropic SSE
  (`message_start` → `content_block_delta` ×3 → `message_delta` →
  `message_stop`), OpenAI ChatCompletion chunks (delta sequence +
  separate usage chunk via `include_usage`), Gemini
  `:streamGenerateContent` (cumulative `usageMetadata` per chunk).
  Each provider gets a happy-path delta-assembly test plus a
  cached-tokens-reported test. Anthropic 49.6% → **81.2%**, OpenAI
  52.0% → **79.0%**, Gemini 53.6% → **79.5%**.
- **10 new git tests** — every dispatcher CLI-fallback path
  (`UnstagedDiff`, `FileDiff` → CLI), every go-git happy path through
  the dispatcher (`CommitDiff`, `RangeDiff`, `BranchDiff`), every
  remaining `CLIRepo` direct method, plus argument validation for the
  empty-string cases. `internal/git` 61.1% → **73.7%**.

#### Refactor for testability

- `internal/cli/list.go` and `internal/cli/review.go` (`renderResult`,
  `openOutput`) switched from hardcoded `os.Stdout` to
  `cmd.OutOrStdout()`. `runReview`'s signature now takes
  `*cobra.Command` (instead of `context.Context`) so the writer is
  propagated through the call chain. No behavior change; tests can now
  capture output via `cmd.SetOut(&buf)`. Same approach applied to
  `cmd.OutOrStdout()` in `compress` and `dry-run` (already done in
  earlier phases).

## [0.2.0] - 2026-05-26

Provider matrix. The CLI now talks to OpenAI, Google Gemini, and Ollama
in addition to Anthropic; `commitbrief setup` cycles through all four
during the wizard. Private repository; no public artifacts.

### Added
- **OpenAI provider** (`internal/provider/openai`) — `gpt-4o`, `gpt-4o-mini`
  via the official `github.com/openai/openai-go` SDK; honors automatic
  prompt caching (cached input tokens reported under
  `usage.prompt_tokens_details.cached_tokens`).
- **Google Gemini provider** (`internal/provider/gemini`) — `gemini-2.5-pro`
  (2M context!), `gemini-2.5-flash`, `gemini-1.5-flash` via the unified
  `google.golang.org/genai` SDK; `cachedContentTokenCount` surfaced for
  future context-cache integration.
- **Ollama provider** (`internal/provider/ollama`) — local-only HTTP
  client against `/api/chat` + `/api/tags` model discovery; no SDK, no
  API key; `TestConnection` pings `/api/tags` rather than spending
  inference time on a real completion.
- Setup wizard's `DefaultSpecs` now lists current model IDs for Gemini
  (2.5 family) and registers all four providers; running `commitbrief
  setup` cycles through anthropic / openai / gemini / ollama choices.

### Changed
- `internal/config.Default()` updated Gemini's default model from
  `gemini-1.5-pro` to `gemini-2.5-pro`.

## [0.1.0] - 2026-05-26

First tagged build. **Private repository**; no public artifacts. The
release exists to lock in the walking-skeleton contract: `commitbrief
setup` → `commitbrief --staged` produces a real LLM review with the
Anthropic provider.

### Added

#### Commands
- `commitbrief init` — write the team-shared `COMMITBRIEF.md` (repo root)
  *and* the per-user `.commitbrief/OUTPUT.md` template (gitignored). Both
  fall back to embedded defaults at runtime; running `init` is only
  necessary when you want to customize the prompt.
- `commitbrief setup [--local]` — interactive provider + API key wizard
  (`huh` form). `--local` saves under `./.commitbrief/config.yml` and
  auto-adds `.commitbrief/` to `.gitignore`.
- `commitbrief --staged` / `-s` (default with no subcommand), `--unstaged` / `-u`,
  `--file` / `-f <path>`, `--commit` / `-c <hash>`,
  `--pull-request <a>...<b>`, `--branch` / `-b <target>` — review flows.
- `commitbrief dry-run` — walk the full pipeline (diff fetch + filter + rules +
  prompt + cache-key compute) without an API call.
- `commitbrief list` — markdown command reference (rendered via `glamour`
  when stdout is a TTY).
- `commitbrief compress` — stub; full implementation in v0.3.0.

#### Core modules
- **`internal/git`** — hybrid `go-git` + `git` CLI access (ADR-0002).
  Commit-based operations go through `go-git`; working-tree operations
  fall back to the CLI.
- **`internal/diff`** — unified-diff parser, file/hunk/line-kind model,
  chars/4 token estimator, round-trip `String()` formatter.
- **`internal/ignore`** — three-layer matcher (built-in defaults +
  `.commitbriefignore` + future LLM-side filter). Last-wins semantics via
  `go-git`'s gitignore pattern engine; negative patterns can revert
  built-in exclusions.
- **`internal/guard`** — pre-send guard (ADR-0007) that prompts before
  shipping a diff containing `.commitbrief/*` files.
- **`internal/provider`** — `Provider` interface + factory registry
  (ADR-0001); `internal/provider/{anthropic,mock}` implementations
  registered via `init()`.
- **`internal/provider/anthropic`** — official `anthropic-sdk-go`
  integration covering Opus 4.7, Sonnet 4.6, Haiku 4.5; ephemeral prompt
  caching enabled with a 5-minute TTL; per-model context-window and
  pricing tables.
- **`internal/cache`** — local response cache at `./.commitbrief/cache/`
  (ADR-0008). SHA-256 key combining diff, system prompt, provider, model,
  language, and schema version; atomic temp+rename writes; corrupt-entry
  auto-delete; 7-day default TTL.
- **`internal/config`** — two-tier YAML config with field-level merge
  (ADR-0005); ENV-variable overrides for API keys and provider/model.
- **`internal/lang`** — D-21 fallback chain (repo config → global config →
  `LANG` env → English).
- **`internal/rules`** — `Load(repoRoot)` returns the on-disk
  `COMMITBRIEF.md` or the binary-embedded default. `LoadOutput(repoRoot,
  userHome)` resolves the output-format template through a three-tier
  fallback (repo `.commitbrief/OUTPUT.md` → `~/.commitbrief/OUTPUT.md` →
  embedded `output.md`); output format is a per-user preference (ADR-0004
  Update 2026-05-26), separated from the team-shared review content.
  `Build` wraps both layers in distinct XML blocks
  (`<project_rules>`, `<output_format>`) with a prompt-injection guard
  naming both.
- **`internal/i18n`** — English and Turkish CLI catalogs; `MustHave`
  helper enforces key parity.
- **`internal/render`** — three output formats (`glamour` terminal,
  plain markdown, JSON schema v1 — `findings` empty until v0.5.0).
- **`internal/setup`** — wizard primitives (provider specs, Apply,
  TestConnection via registry, Ollama `/api/tags` discovery, local +
  global config writers with `0600` perms).
- **`internal/ui`** — TTY-aware color/spinner/prompt; Windows ANSI VT
  mode handling under a `//go:build windows` tag.
- **`internal/version`** — ldflags injection point (`Version`, `Commit`,
  `Date`); honored by `make build` and CI release pipeline.

#### Infrastructure
- Go module targeting **Go 1.25** (supersedes ADR-0013 §2's original
  1.23 target; bump driven by upstream `go-git` and `golang.org/x/net`).
- CI matrix on Ubuntu, macOS, Windows; `golangci-lint v2` lint job;
  CodeQL job guarded for private-repo visibility.
- Helper scripts: `release-check.sh` (default-prompt TBD guard + i18n
  parity), `license-check.sh` (GPL-3.0 compatibility audit),
  `manpage.sh` (cobra → man), `smoke-test.sh` (pipeline + cache-key
  invalidation; runs without an API key).
- `Makefile` targets: `build`, `test`, `test-live`, `lint`, `fmt`,
  `tidy`, `clean`, `release-check`, `license-check`, `manpage`, `smoke`.

### Known limitations
- Only the **Anthropic** provider is implemented; OpenAI, Gemini, and
  Ollama land in v0.2.0.
- `commitbrief compress` is a stub; full implementation in v0.3.0.
- Reviews are returned **non-streaming**; provider streaming arrives in
  v0.4.0 alongside the `--verbose` footer's cache-saved-cost display.
- Repository is private through v0.3.x; `go install`,
  `brew install commitbrief`, and `scoop install commitbrief` are not
  yet usable. First public release is v0.4.0.
- Initial-commit `CommitDiff` via `go-git` returns `ErrUnsupported` and
  is handled by the CLI fallback (ADR-0002 mitigation).

[Unreleased]: https://github.com/CommitBrief/commitbrief/compare/v0.5.0...HEAD
[0.5.0]: https://github.com/CommitBrief/commitbrief/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/CommitBrief/commitbrief/compare/v0.2.0...v0.4.0
[0.2.0]: https://github.com/CommitBrief/commitbrief/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/CommitBrief/commitbrief/releases/tag/v0.1.0
