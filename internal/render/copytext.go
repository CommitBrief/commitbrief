// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"fmt"
	"strings"
)

// CopyText returns a plain-text representation of f suitable for
// pasting into chat, an issue tracker, or a code editor. Format:
//
//	[💥 CRITICAL] internal/auth/session.go:142
//	SQL fragment built from request input
//
//	String concatenation feeds db.Query directly — bypasses the
//	prepared statement path used elsewhere in this package.
//
//	→ Switch to a prepared statement with bound parameters here so
//	user input never reaches the SQL string concatenation path.
//
// The diff snippet is deliberately omitted — chat clients tend to
// mangle multi-line code blocks, and the path:line already gives
// the recipient enough to jump straight to the source. The
// description and suggestion are each flattened to a single
// paragraph (collapse internal whitespace) so they survive
// Slack/Discord rendering.
func CopyText(f Finding) string {
	t, ok := severityThemes[f.Severity]
	if !ok {
		t = severityThemes[SeverityCritical]
	}
	desc := strings.Join(strings.Fields(f.Description), " ")

	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", t.label, f.PathRef())
	fmt.Fprintf(&b, "%s\n\n", f.Title)
	fmt.Fprintf(&b, "%s\n", desc)
	if f.Suggestion != "" {
		// Chevron prefix matches the card visual; the body is
		// flattened the same way the description is so it survives
		// chat-client paste.
		suggestion := strings.Join(strings.Fields(f.Suggestion), " ")
		fmt.Fprintf(&b, "\n→ %s\n", suggestion)
	}
	return b.String()
}

// BuildCopyPayload joins per-finding CopyText blocks with a markdown
// horizontal-rule separator (`\n---\n\n`). Order is preserved from
// the input slice — callers that want severity-ordered output should
// sort beforehand (GroupBySeverity / severityOrder).
//
// Returns "" for an empty slice; callers should branch on the empty
// case and skip the clipboard call entirely so the user doesn't get
// a hint line about copying zero findings.
func BuildCopyPayload(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, CopyText(f))
	}
	return strings.Join(parts, "\n---\n\n")
}
