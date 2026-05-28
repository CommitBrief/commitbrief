// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"github.com/CommitBrief/commitbrief/internal/cli"
	"github.com/CommitBrief/commitbrief/internal/version"

	// Side-effect imports: each provider's init() registers itself in the
	// global provider registry. API providers first, then CLI-backed
	// providers (claude-cli, gemini-cli) — the `-cli` suffix mirrors the
	// directory naming and is the deliberate cue that these go through
	// a local subprocess rather than an HTTPS API.
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	_ "github.com/CommitBrief/commitbrief/internal/provider/claude-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/cohere"
	_ "github.com/CommitBrief/commitbrief/internal/provider/deepseek"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/mistral"
	_ "github.com/CommitBrief/commitbrief/internal/provider/ollama"
	_ "github.com/CommitBrief/commitbrief/internal/provider/openai"
)

func main() {
	// Backfill Version/Commit/Date from BuildInfo when ldflags didn't
	// inject (notably `go install path@vX.Y.Z`). No-op when goreleaser
	// or `make build` has already populated them.
	version.Resolve()
	cli.Execute()
}
