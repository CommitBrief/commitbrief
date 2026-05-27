// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"strings"
	"testing"
)

func TestCopyTextFormatMatchesSecguardPrototype(t *testing.T) {
	// Per the secguard prototype, the body is:
	//   [<severity label>] <path>[:line]
	//   <title>
	//
	//   <flattened description>
	// (trailing newline)
	f := Finding{
		Severity:    SeverityCritical,
		File:        "internal/auth/session.go",
		Line:        142,
		Title:       "SQL fragment built from request input",
		Description: "String concatenation feeds db.Query directly —\n    bypasses the prepared statement path.",
	}
	got := CopyText(f)
	want := "[💥 CRITICAL] internal/auth/session.go:142\n" +
		"SQL fragment built from request input\n\n" +
		"String concatenation feeds db.Query directly — bypasses the prepared statement path.\n"
	if got != want {
		t.Errorf("CopyText mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestCopyTextOmitsLineWhenZero(t *testing.T) {
	// Some findings (e.g. high-level architectural notes) have no line
	// number. We render the path bare instead of "path:0" which would
	// confuse anyone clicking through.
	f := Finding{
		Severity:    SeverityHigh,
		File:        "go.mod",
		Title:       "Outdated dependency",
		Description: "Bump to v2.1.0.",
	}
	got := CopyText(f)
	if !strings.Contains(got, "[🚨 HIGH] go.mod\n") {
		t.Errorf("expected bare path without :0 suffix; got:\n%s", got)
	}
	if strings.Contains(got, ":0") {
		t.Errorf("path should not include :0 suffix; got:\n%s", got)
	}
}

func TestCopyTextFlattensMultilineDescription(t *testing.T) {
	// Chat clients (Slack, Discord) mangle multi-line code blocks and
	// often double-newline-collapse anyway. CopyText flattens internal
	// whitespace so the description survives paste.
	f := Finding{
		Severity:    SeverityMedium,
		File:        "x.go",
		Line:        1,
		Title:       "t",
		Description: "first line\n\n  second line  with   weird spacing\n\tthird",
	}
	got := CopyText(f)
	if !strings.Contains(got, "first line second line with weird spacing third\n") {
		t.Errorf("description not flattened; got:\n%s", got)
	}
}

func TestCopyTextUnknownSeverityFallsBackToCritical(t *testing.T) {
	// Defensive: ParseFindings already rejects unknown severities, but
	// CopyText must not panic if a hand-constructed Finding slips
	// through. The fallback matches Render() to keep behaviour aligned.
	f := Finding{
		Severity:    Severity("bogus"),
		File:        "x.go",
		Line:        1,
		Title:       "t",
		Description: "d",
	}
	got := CopyText(f)
	if !strings.HasPrefix(got, "[💥 CRITICAL]") {
		t.Errorf("unknown severity should fall back to 💥 CRITICAL chip; got:\n%s", got)
	}
}

func TestBuildCopyPayloadJoinsWithHorizontalRule(t *testing.T) {
	findings := []Finding{
		{Severity: SeverityCritical, File: "a.go", Line: 1, Title: "A", Description: "a"},
		{Severity: SeverityHigh, File: "b.go", Line: 2, Title: "B", Description: "b"},
	}
	got := BuildCopyPayload(findings)
	if !strings.Contains(got, "\n---\n\n") {
		t.Errorf("payload should separate findings with horizontal rule; got:\n%s", got)
	}
	if !strings.Contains(got, "[💥 CRITICAL] a.go:1") {
		t.Errorf("missing first finding header; got:\n%s", got)
	}
	if !strings.Contains(got, "[🚨 HIGH] b.go:2") {
		t.Errorf("missing second finding header; got:\n%s", got)
	}
}

func TestBuildCopyPayloadEmptyReturnsEmpty(t *testing.T) {
	if got := BuildCopyPayload(nil); got != "" {
		t.Errorf("empty findings should return \"\"; got %q", got)
	}
	if got := BuildCopyPayload([]Finding{}); got != "" {
		t.Errorf("empty slice should return \"\"; got %q", got)
	}
}

func TestCopyTextRendersLineRange(t *testing.T) {
	// Multi-line finding (LineEnd > Line) should show "path:start-end"
	// in the header so the recipient can see the span at a glance.
	f := Finding{
		Severity:    SeverityHigh,
		File:        "internal/db/migrate.go",
		Line:        73,
		LineEnd:     91,
		Title:       "NOT NULL added without default",
		Description: "Migration fails on populated tables.",
	}
	got := CopyText(f)
	if !strings.Contains(got, "[🚨 HIGH] internal/db/migrate.go:73-91\n") {
		t.Errorf("expected range path 'file:73-91'; got:\n%s", got)
	}
}

func TestCopyTextIncludesSuggestionWithChevron(t *testing.T) {
	// Clipboard payload mirrors the card visual: chevron-prefixed
	// suggestion line at the end so a paste-into-Slack carries the
	// actionable bit, not just the diagnostic.
	f := Finding{
		Severity:    SeverityCritical,
		File:        "internal/auth/session.go",
		Line:        142,
		Title:       "SQL fragment built from request input",
		Description: "String concatenation feeds db.Query directly.",
		Suggestion:  "Switch to a prepared statement with bound parameters.",
	}
	got := CopyText(f)
	if !strings.Contains(got, "→ Switch to a prepared statement with bound parameters.") {
		t.Errorf("copy payload missing chevron-prefixed suggestion; got:\n%s", got)
	}
	// Suggestion sits after description, on its own paragraph.
	if !strings.Contains(got, "directly.\n\n→ Switch") {
		t.Errorf("suggestion should appear in its own paragraph below description; got:\n%s", got)
	}
}

func TestCopyTextSkipsSuggestionWhenEmpty(t *testing.T) {
	// Defensive: an empty Suggestion (e.g. from a hand-constructed
	// Finding in tests) must NOT emit a bare "→" line.
	f := Finding{
		Severity: SeverityLow, File: "x.go", Line: 1, Title: "t", Description: "d",
	}
	got := CopyText(f)
	if strings.Contains(got, "→") {
		t.Errorf("empty suggestion should not emit a chevron; got:\n%s", got)
	}
}

func TestCopyTextFlattensMultilineSuggestion(t *testing.T) {
	// Same flattening rule the description uses — chat clients
	// occasionally render multi-line text inconsistently.
	f := Finding{
		Severity:    SeverityMedium,
		File:        "x.go",
		Line:        1,
		Title:       "t",
		Description: "d",
		Suggestion:  "Refactor the loop to use\nbatched updates.\nAvoid the per-row commit.",
	}
	got := CopyText(f)
	if !strings.Contains(got, "→ Refactor the loop to use batched updates. Avoid the per-row commit.") {
		t.Errorf("multi-line suggestion should be flattened; got:\n%s", got)
	}
}

func TestBuildCopyPayloadSingleFindingNoSeparator(t *testing.T) {
	// One finding → no separator (the rule is a *between*-block
	// concern). Verifies we don't accidentally pre/append the divider.
	got := BuildCopyPayload([]Finding{
		{Severity: SeverityInfo, File: "x.go", Line: 1, Title: "t", Description: "d"},
	})
	if strings.Contains(got, "---") {
		t.Errorf("single finding should not include separator; got:\n%s", got)
	}
}
