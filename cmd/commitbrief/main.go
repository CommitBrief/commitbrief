package main

import (
	"github.com/CommitBrief/commitbrief/internal/cli"

	// Side-effect import: registers the Anthropic provider with the
	// internal/provider registry at init().
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
)

func main() {
	cli.Execute()
}
