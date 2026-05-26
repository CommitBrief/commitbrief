# Changelog

All notable changes to **CommitBrief** are documented in this file.

The format follows [Keep a Changelog 1.1.0](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

> Tags prior to **v0.4.0** were cut in the private repository and produced no
> public artifacts; the first publicly released version is v0.4.0.

## [Unreleased]

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

### Tests
- `scripts/smoke-test.sh` now exercises `.commitbriefignore` end-to-end:
  it stages a `go.sum`, confirms the built-in layer filters it, then adds
  `!go.sum` to `.commitbriefignore` and confirms the negative pattern
  reverts the built-in exclusion.
- 15 new compress tests (level parsing, embedded prompts non-empty,
  happy-path with fake provider, abort-when-larger, prompt-injection
  guard wrap, system-prompt selection, post-processing of preamble +
  code-fence wrappers, backup + atomic apply round-trip).
- 4 new render tests covering the verbose-footer cache-savings label.

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

[Unreleased]: https://github.com/CommitBrief/commitbrief/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/CommitBrief/commitbrief/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/CommitBrief/commitbrief/releases/tag/v0.1.0
