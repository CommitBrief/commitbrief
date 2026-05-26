# CommitBrief

LLM-powered local code review CLI. Run a "second pair of eyes" review on your
staged changes, branches, or PR diffs without leaving the terminal.

> **Status:** Pre-release scaffolding. The CLI is being built toward v0.1.0
> ("walking skeleton"). The repository is **private through v0.3.x** and
> flips public at **v0.4.0**.

## What it will do (planned v1 surface)

```sh
commitbrief                       # review staged changes (= --staged)
commitbrief --unstaged            # review unstaged changes
commitbrief --file <path>         # review changes in a single file
commitbrief --commit <hash>       # review a specific commit
commitbrief --pull-request a...b  # review a PR-style diff
commitbrief --branch <target>     # review current branch vs target
commitbrief init                  # write COMMITBRIEF.md to the repo
commitbrief setup                 # interactive provider + API key wizard
commitbrief compress              # shrink COMMITBRIEF.md losslessly
commitbrief dry-run               # show what would be sent — no API call
```

## Providers

Anthropic, OpenAI, Google Gemini, and Ollama. Adding a provider is one
package under `internal/provider/<name>/` implementing the `Provider`
interface.

## Configuration

User-level (`~/.commitbrief/config.yml`) plus repo-level
(`./.commitbrief/config.yml`, gitignored) with field-by-field override.
Review rules live in plain-prose markdown at `./COMMITBRIEF.md` and are sent
as the system prompt; if absent, an embedded default is used.

## Building from source

```sh
git clone https://github.com/CommitBrief/commitbrief
cd commitbrief
make build      # → ./commitbrief
make test
make lint
```

Requires **Go 1.23+**. After v0.4.0, installation will also be available via
`go install`, Homebrew, Scoop, and GitHub Releases.

## License

[GPL-3.0-or-later](LICENSE).

## Contributing

See [`CONTRIBUTING.md`](CONTRIBUTING.md) for the project-specific build and
test flow, and the
[org-wide CONTRIBUTING guide](https://github.com/CommitBrief/.github/blob/main/CONTRIBUTING.md)
for the inbound-equals-outbound licensing policy and PR conventions.
