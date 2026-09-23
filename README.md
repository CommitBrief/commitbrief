# CommitBrief

LLM-powered local code review for git diffs. Run a "second pair of eyes"
review on your staged changes, a specific file, a single commit, or a
PR-style three-dot range — without leaving the terminal.

```sh
commitbrief                                # review your staged changes
commitbrief diff HEAD                      # review working tree vs HEAD
commitbrief diff main...feature/x          # review a PR
commitbrief --unstaged --dir app/Models    # narrow any scope to a directory
```

Output is rendered as colored markdown in the terminal, plain markdown to a
file, or strict JSON for tooling — your choice.

## Why

A real reviewer is the gold standard, but they aren't always available the
moment you stage a change. CommitBrief gives you a quick, structured read on
your diff before another human (or your future self) sees it — local-first
(review output stays on your machine; the diff goes only to the provider
you pick, or nowhere at all with Ollama), provider-agnostic, cache aware
on unchanged diffs, and steerable with a repo's own `COMMITBRIEF.md`
review rules.

## Install

```sh
brew install CommitBrief/tap/commitbrief                          # macOS / Linux
scoop bucket add commitbrief https://github.com/CommitBrief/scoop-bucket
scoop install commitbrief                                         # Windows
go install github.com/CommitBrief/commitbrief/cmd/commitbrief@latest
```

Pre-built binaries are attached to each tagged release. Full install paths,
package-manager setup, and `commitbrief upgrade`: see
[Installation](https://commitbrief.com/docs/1.x/installation).

## Quick start

```sh
commitbrief setup   # pick a provider, paste your API key, run a ping
commitbrief init    # write a starter COMMITBRIEF.md
commitbrief         # review your staged changes
```

Walkthrough with sample output: [Run your first
review](https://commitbrief.com/docs/1.x/first-review).

## Providers

Shipped providers: `anthropic`, `openai`, `gemini`, `ollama` (native
API/local), `deepseek`, `mistral`, `cohere` (OpenAI-compatible), and
`claude-cli`, `gemini-cli`, `codex-cli` (reuse a local CLI subscription, no
extra API key). Models, context windows, and per-token pricing:
[Providers](https://commitbrief.com/docs/1.x/providers).

## Docs

The [full documentation](https://commitbrief.com/docs) is the source of
truth for everything below the front door:

- [Which command do I need?](https://commitbrief.com/docs/1.x/which-command) — decision table across the whole command surface
- [Output formats](https://commitbrief.com/docs/1.x/output-formats) — Cards, JSON schema, Markdown
- [Review pull requests](https://commitbrief.com/docs/1.x/pr-review) and [Gate CI on findings](https://commitbrief.com/docs/1.x/ci-gate)
- [MCP server](https://commitbrief.com/docs/1.x/mcp-server) — let an AI agent self-review
- [Policy gate](https://commitbrief.com/docs/1.x/policy-gate) — declarative `.commitbrief/policy.yml`
- [Configuration](https://commitbrief.com/docs/1.x/configuration) and [Filtering scope](https://commitbrief.com/docs/1.x/review-scopes)
- [Credential audit — leaks](https://commitbrief.com/docs/1.x/leaks)
- [FAQ / Troubleshooting](https://commitbrief.com/docs/1.x/troubleshooting)

## Stability

The stable line is an **API freeze**: CLI flag surface, the JSON schema
(`--json`), `COMMITBRIEF.md`/`OUTPUT.md` formats, and the public config keys
follow strict semver — breaking changes only ship in the next major line.
Details: [Compatibility](https://commitbrief.com/docs/1.x/upgrade#compatibility).
Upgrading across a major version? See the migration notes in
[CHANGELOG.md](CHANGELOG.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for the build/test flow, and the
[org-wide guide](https://github.com/CommitBrief/.github/blob/main/CONTRIBUTING.md)
for licensing and PR conventions. Bug reports and questions are welcome in
[Issues](https://github.com/CommitBrief/commitbrief/issues) and
[Discussions](https://github.com/CommitBrief/commitbrief/discussions).

## License

[GPL-3.0-or-later](LICENSE). Provider SDK dependencies are Apache-2.0 or
MIT; the full audit is `make license-check`.
