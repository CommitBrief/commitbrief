// SPDX-License-Identifier: GPL-3.0-or-later

// Package geminicli registers a CLI-tool-backed provider that drives
// Google's Gemini CLI (`gemini`) binary as the review engine. The
// user-facing name is `gemini-cli` (selectable with `--cli gemini` or
// `--provider gemini-cli`); the underlying transport is a subprocess
// of the host CLI rather than an HTTPS API call, so no API key is
// required when `gemini` is already authenticated locally.
//
// Layout naming: the `-cli` directory suffix is purely a developer
// signal that this is the CLI-backed sibling of the existing
// internal/provider/gemini/ package (which talks to
// generativelanguage.googleapis.com).
package geminicli

import (
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/clireview"
)

// Name is what users type. Matches the directory suffix convention.
const Name = "gemini-cli"

func init() {
	provider.Register(Name, func(_ config.ProviderConfig) (provider.Provider, error) {
		return clireview.New(clireview.Spec{
			Name:   Name,
			Binary: "gemini",
			// `gemini -p "<prompt>"` is Gemini CLI's documented one-shot
			// invocation. The output is plain text by default; no
			// extra flag needed.
			//
			// UC-24 note: gemini-cli does not yet expose a documented
			// `-p -` stdin shorthand the way Claude Code does, so we
			// stay on argv until upstream confirms a stable stdin
			// transport. Users hitting ARG_MAX on huge diffs should
			// prefer claude-cli for now.
			PromptArgs: func(prompt string) []string {
				return []string{"-p", prompt}
			},
			VersionArgs: []string{"--version"},
			Timeout:     5 * time.Minute,
		}), nil
	})
}
