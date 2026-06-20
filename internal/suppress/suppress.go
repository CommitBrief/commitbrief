// SPDX-License-Identifier: GPL-3.0-or-later

// Package suppress implements signal control SC2 (ADR-0027): inline,
// in-source suppression of a single finding via a visible, reasoned comment
// marker on (or directly above) the offending line.
//
// The marker is `commitbrief-ignore: <reason>` or
// `commitbrief-ignore[<severity>]: <reason>`. A scoped marker silences only
// that severity on the line; an unscoped marker silences any. The reason is
// everything after the colon and is required only insofar as the marker must
// parse — an empty reason still suppresses, but the syntax is documented as
// "give a reason".
//
// Unlike the user-private baseline, a suppression marker lives in committed
// source: a reviewer sees it in the diff, so it cannot be used to silently
// hide a real bug from a senior. That is why suppression carries none of the
// baseline's team-shared hide-vector risk.
//
// Markers are read from the ADDED lines of the diff (the new content of the
// change under review). The comment prefix is not parsed — the
// `commitbrief-ignore` token is matched language-independently, so `//`,
// `#`, `--`, and `/* ... */` comments all work without a per-language table.
package suppress

import (
	"regexp"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// markerRe matches a commitbrief-ignore directive anywhere on a line,
// regardless of the surrounding comment syntax. Group 1 is the optional
// bracketed severity; group 2 is the reason (everything after the colon).
// The token match is case-insensitive on the keyword but the captured
// severity is lowercased by the caller before comparison.
//
//	commitbrief-ignore: reason
//	commitbrief-ignore[high]: reason
//	// commitbrief-ignore[critical]: SQL is parameterized, false positive
//	#  commitbrief-ignore : trailing-space tolerant
var markerRe = regexp.MustCompile(`(?i)commitbrief-ignore\s*(?:\[\s*([a-z]+)\s*\])?\s*:\s*(.*)`)

// Rule is one parsed suppression directive bound to a new-file line number.
// Severity is "" for an unscoped marker (suppresses any finding on the line)
// or one of the five canonical levels for a scoped marker. Reason is the
// free text after the colon, trimmed; it is retained so a future surface
// (or audit log) can echo why a finding was silenced.
type Rule struct {
	Severity render.Severity
	Reason   string
}

// Suppressions maps a new-file line number to the directive found on that
// line. Only one directive per line is retained (the first match wins; a
// line with two markers is pathological and not worth modeling).
type Suppressions map[int]Rule

// ParseSuppressions scans the ADDED lines of d for commitbrief-ignore
// markers and returns them keyed by their new-file line number — the same
// 1-based numbering a finding's Line field uses (see diff.NumberedString).
// Context and removed lines are ignored: a suppression must be part of the
// change under review, not pre-existing untouched code. Returns an empty
// (non-nil) map when there are no markers.
func ParseSuppressions(d diff.Diff) Suppressions {
	out := make(Suppressions)
	for _, f := range d.Files {
		for _, h := range f.Hunks {
			newNo := h.NewStart
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.LineAdd:
					if r, ok := parseLine(l.Text); ok {
						if _, exists := out[newNo]; !exists {
							out[newNo] = r
						}
					}
					newNo++
				case diff.LineContext:
					newNo++
				case diff.LineDel:
					// Removed lines have no new-file number; skip.
				}
			}
		}
	}
	return out
}

// parseLine extracts a Rule from a single source line, or (zero, false) if
// the line carries no marker. The severity, when present and valid, is
// stored canonically; an unknown bracketed severity (e.g. `[bogus]`) is
// treated as unscoped so a typo fails open (suppresses) rather than silently
// doing nothing the user can't see — the marker is visible in the diff
// either way.
func parseLine(text string) (Rule, bool) {
	m := markerRe.FindStringSubmatch(text)
	if m == nil {
		return Rule{}, false
	}
	sev := render.Severity(strings.ToLower(strings.TrimSpace(m[1])))
	if !sev.IsValid() {
		sev = ""
	}
	return Rule{Severity: sev, Reason: strings.TrimSpace(m[2])}, true
}

// matches reports whether rule r suppresses a finding of severity sev. An
// unscoped rule (Severity == "") matches any severity; a scoped rule matches
// only its own severity.
func (r Rule) matches(sev render.Severity) bool {
	return r.Severity == "" || r.Severity == sev
}

// Filter splits findings into the ones to KEEP and a count of how many were
// dropped by an inline suppression. A finding is dropped when a matching
// marker sits on its own line OR on the line directly above it (line-1) — the
// "above" case is the idiomatic placement when the offending statement is
// long or the marker would clutter the line. This is a TRUE removal
// (ADR-0027): the kept slice feeds fail-on, JSON findings[], and display
// alike. An empty suppression map keeps everything. The kept slice is always
// non-nil so the nil-means-degrade invariant downstream is preserved.
func Filter(findings []render.Finding, sup Suppressions) (kept []render.Finding, suppressed int) {
	if len(sup) == 0 {
		return findings, 0
	}
	kept = make([]render.Finding, 0, len(findings))
	for _, f := range findings {
		if isSuppressed(f, sup) {
			suppressed++
			continue
		}
		kept = append(kept, f)
	}
	return kept, suppressed
}

// isSuppressed reports whether finding f is silenced by a marker on its line
// or the line directly above. A finding with Line <= 0 (no anchor) can't be
// matched to a source line, so it is never suppressed.
func isSuppressed(f render.Finding, sup Suppressions) bool {
	if f.Line <= 0 {
		return false
	}
	if r, ok := sup[f.Line]; ok && r.matches(f.Severity) {
		return true
	}
	if r, ok := sup[f.Line-1]; ok && r.matches(f.Severity) {
		return true
	}
	return false
}
