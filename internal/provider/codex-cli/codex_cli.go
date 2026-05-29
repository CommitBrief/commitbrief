// SPDX-License-Identifier: GPL-3.0-or-later

// Package codexcli registers a CLI-tool-backed provider that drives
// OpenAI's Codex CLI (`codex`) binary as the review engine. The
// user-facing name is `codex-cli` (selectable with `--cli codex` or
// `--provider codex-cli`); the underlying transport is a subprocess of
// the host CLI rather than an HTTPS API call, so no API key is required
// when `codex` is already authenticated locally (ChatGPT sign-in or
// OPENAI_API_KEY in the host CLI's own environment).
//
// Layout naming: the `-cli` directory suffix is purely a developer
// signal that this is the CLI-backed sibling of the existing
// internal/provider/openai/ package (which talks to api.openai.com).
//
// Codex is more agentic than Claude Code's `-p` or Gemini CLI's `-p`
// one-shot modes, so we drive its non-interactive `exec` subcommand and
// pin a read-only sandbox: a review must never let the agent modify the
// working tree or run write commands. Like the other CLI providers this
// is a PlainTextEmitter — the host CLI's output streams through verbatim,
// with no JSON-findings contract, so `remote pr` and `--json` do not
// apply.
package codexcli

import (
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/clireview"
)

// Name is what users type. Matches the directory suffix convention.
const Name = "codex-cli"

func init() {
	provider.Register(Name, func(_ config.ProviderConfig) (provider.Provider, error) {
		return clireview.New(clireview.Spec{
			Name:   Name,
			Binary: "codex",
			// `codex exec "<prompt>"` is Codex CLI's non-interactive
			// (headless) invocation: it runs the prompt to completion and
			// prints the result to stdout, no REPL.
			//
			//   --sandbox read-only      — a review must never mutate the
			//                              working tree or run write/network
			//                              commands; the agent may read code
			//                              to ground its answer, nothing more.
			//   --skip-git-repo-check    — codex exec otherwise refuses to
			//                              run outside a git/"trusted" dir
			//                              ("Not inside a trusted directory…").
			//                              We don't need its repo guard — the
			//                              diff is already in the prompt — so
			//                              we skip it for portability.
			//
			// Color is left to the CLI's own non-TTY auto-detection (stdout
			// here is a pipe), matching the claude-cli / gemini-cli adapters.
			//
			// UC-24 note: like gemini-cli, the prompt rides argv rather
			// than stdin until a stdin transport for `codex exec` is
			// confirmed stable; users hitting ARG_MAX on very large diffs
			// should prefer claude-cli for now.
			PromptArgs: func(prompt string) []string {
				return []string{"exec", "--sandbox", "read-only", "--skip-git-repo-check", prompt}
			},
			VersionArgs: []string{"--version"},
			Timeout:     5 * time.Minute,
		}), nil
	})
}
