// SPDX-License-Identifier: GPL-3.0-or-later

package prompt

import (
	"fmt"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// Prompt is the assembled (system, user) pair ready to hand to a Provider.
// Use Build to construct it.
type Prompt struct {
	System string
	User   string
}

// Build assembles the system prompt (project rules + severity rubric + JSON
// response contract + lang directive + prompt-injection guard) and the user
// prompt (diff fenced block). OUTPUT.md is no longer part of prompt
// construction — under ADR-0014 it is a client-side renderer template
// consumed only by the local Go runtime.
func Build(rulesLoaded rules.Loaded, langRes lang.Resolution, diffText string) Prompt {
	system, userTpl := rules.Build(rulesLoaded, langRes)
	return Prompt{
		System: system,
		User:   fmt.Sprintf(userTpl, diffText),
	}
}

// EstimatedTokens uses the chars/4 heuristic shared with internal/diff.
// Provider-side token counts override this; the value is intended for
// pre-flight checks and dry-run reporting.
func (p Prompt) EstimatedTokens() int {
	return diff.EstimateTokens(p.System) + diff.EstimateTokens(p.User)
}

// ExceedsContext reports whether the estimated token count is larger than
// the provider's reported context window. Callers should branch on this
// before sending a request so users get a friendlier error than a
// provider-side 400.
func (p Prompt) ExceedsContext(contextWindow int) bool {
	if contextWindow <= 0 {
		return false
	}
	return p.EstimatedTokens() > contextWindow
}
