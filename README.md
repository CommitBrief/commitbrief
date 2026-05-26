# CommitBrief

LLM-powered local code review for git diffs. Run a "second pair of eyes"
review on your staged changes, a single commit, or a PR-style three-dot
range — without leaving the terminal.

Pick a provider once, then:

```sh
commitbrief                  # review your staged changes
commitbrief --commit HEAD    # review the latest commit
commitbrief --pull-request main...feature/x   # review a PR
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

> v0.4.0 is the first public release. Earlier tags were private.

### Homebrew (macOS / Linux)

```sh
brew tap CommitBrief/tap
brew install commitbrief
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

## Command surface

```sh
commitbrief                       # = --staged (default scope)
commitbrief --unstaged            # working-tree changes
commitbrief --file <path>         # changes in a single file vs HEAD
commitbrief --commit <hash>       # one commit
commitbrief --pull-request a...b  # three-dot PR-style range
commitbrief --branch <target>     # current branch vs target ref

commitbrief setup [--local]       # provider + API key wizard
commitbrief init                  # write COMMITBRIEF.md + OUTPUT.md template
commitbrief compress [--level=balanced]  # shrink COMMITBRIEF.md
commitbrief dry-run               # pipeline preview; no API call
commitbrief list                  # command reference
```

Global flags include `--json`, `--markdown`, `--output <file>`,
`--no-cache`, `--yes`, `--verbose`, `--quiet`, `--lang`, `--provider`,
`--model`, `--color`. See `commitbrief --help`.

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
  (gitignored)

Plus environment variables for credentials (`ANTHROPIC_API_KEY`,
`OPENAI_API_KEY`, `GEMINI_API_KEY`, etc.) and CLI flags for one-off
overrides (`--provider gemini --model gemini-2.5-flash`).

Review content lives in two files:

- **`COMMITBRIEF.md`** at the repo root — team-shared review rules,
  perspectives, project context. Committed to git.
- **`.commitbrief/OUTPUT.md`** (or `~/.commitbrief/OUTPUT.md`) —
  per-user output format (severity scale, finding structure). Gitignored.

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
make build      # → ./commitbrief
make test
make lint
make smoke      # end-to-end pipeline check; no API key needed
```

`make test-live` runs provider tests against real APIs (requires keys;
skipped in CI by default).

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
