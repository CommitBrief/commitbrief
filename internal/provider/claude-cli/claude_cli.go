// SPDX-License-Identifier: GPL-3.0-or-later

// Package claudecli registers a CLI-tool-backed provider that drives
// Anthropic's Claude Code (`claude`) binary as the review engine.
// The user-facing name is `claude-cli` (selectable with `--cli claude`
// or `--provider claude-cli`); the underlying transport is a
// subprocess of the host CLI rather than an HTTPS API call, so no
// API key is required when `claude` is already authenticated locally.
//
// Layout naming: the `-cli` directory suffix is purely a developer
// signal that this is the CLI-backed sibling of the existing
// internal/provider/anthropic/ package (which talks to api.anthropic.com).
// Mixing them up would be easy without the suffix.
package claudecli

import (
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/clireview"
)

// Name is what users type. Matches the directory suffix convention.
const Name = "claude-cli"

func init() {
	provider.Register(Name, func(_ config.ProviderConfig) (provider.Provider, error) {
		return clireview.New(clireview.Spec{
			Name:   Name,
			Binary: "claude",
			// `claude -p "<prompt>" --output-format text` is the
			// documented one-shot, non-interactive invocation for
			// Claude Code. `--output-format text` keeps the response
			// clean (no JSON envelope) so we can pass it through
			// verbatim to the user; `--print` (`-p`) bypasses the
			// interactive REPL.
			PromptArgs: func(prompt string) []string {
				return []string{"-p", prompt, "--output-format", "text"}
			},
			VersionArgs: []string{"--version"},
			Timeout:     5 * time.Minute,
		}), nil
	})
}
