package render

import (
	"fmt"
	"strings"
)

// CopyText returns a plain-text representation of f suitable for
// pasting into chat, an issue tracker, or a code editor. Format
// matches the maintainer's secguard prototype verbatim:
//
//	[💥 CRITICAL] internal/auth/session.go:142
//	SQL fragment built from request input
//
//	String concatenation feeds db.Query directly — bypasses the
//	prepared statement path used elsewhere in this package.
//
// The diff snippet is deliberately omitted — chat clients tend to
// mangle multi-line code blocks, and the path:line already gives
// the recipient enough to jump straight to the source. The
// description is flattened to a single line (collapse internal
// whitespace) so it survives Slack/Discord rendering.
func CopyText(f Finding) string {
	t, ok := severityThemes[f.Severity]
	if !ok {
		t = severityThemes[SeverityCritical]
	}
	path := f.File
	if f.Line > 0 {
		path = fmt.Sprintf("%s:%d", f.File, f.Line)
	}
	desc := strings.Join(strings.Fields(f.Description), " ")

	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", t.label, path)
	fmt.Fprintf(&b, "%s\n\n", f.Title)
	fmt.Fprintf(&b, "%s\n", desc)
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
