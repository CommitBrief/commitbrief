package main

import (
	"github.com/CommitBrief/commitbrief/internal/cli"

	// Side-effect imports: each provider's init() registers itself in the
	// global provider registry.
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini"
	_ "github.com/CommitBrief/commitbrief/internal/provider/openai"
)

func main() {
	cli.Execute()
}
