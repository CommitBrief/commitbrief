package main

import (
	"github.com/CommitBrief/commitbrief/internal/cli"
	"github.com/CommitBrief/commitbrief/internal/version"

	// Side-effect imports: each provider's init() registers itself in the
	// global provider registry.
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini"
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
