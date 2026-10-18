// SPDX-License-Identifier: GPL-3.0-or-later

// Package suppress implements signal control SC2 (ADR-0027): inline,
// in-source suppression of a single finding via a visible, reasoned comment
// marker on (or directly above) the offending line.
//
// The marker is `commitbrief-ignore: <reason>`,
// `commitbrief-ignore[<severity>]: <reason>` or
// `commitbrief-ignore[<rule_id>]: <reason>`. A severity-scoped marker silences
// only that severity on the line, a rule-scoped marker only findings of that
// rule (`sec.injection`) or rule category (`sec.*`); an unscoped marker
// silences any. A bracket token containing a dot is a rule scope, a dotless
// one is a severity scope (severity names never contain a dot, rule ids
// always do), so the two never collide. The reason is everything after the
// colon and is required only insofar as the marker must parse — an empty
// reason still suppresses, but the syntax is documented as "give a reason".
//
// A marker is bound to the file it appears in: it is keyed by (path, line),
// so a marker in one file never silences a finding on the same line number of
// another file.
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
	"sort"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// markerRe matches a commitbrief-ignore directive anywhere on a line,
// regardless of the surrounding comment syntax. Group 1 is the optional
// bracketed scope (a severity or a rule id / category wildcard); group 2 is
// the reason (everything after the colon). The token match is case-insensitive
// on the keyword but the captured scope is lowercased by the caller before
// comparison.
//
//	commitbrief-ignore: reason
//	commitbrief-ignore[high]: reason
//	commitbrief-ignore[sec.injection]: reason
//	commitbrief-ignore[sec.*]: reason
//	// commitbrief-ignore[critical]: SQL is parameterized, false positive
//	#  commitbrief-ignore : trailing-space tolerant
var markerRe = regexp.MustCompile(`(?i)commitbrief-ignore\s*(?:\[\s*([a-z0-9._*-]+)\s*\])?\s*:\s*(.*)`)

// Rule is one parsed suppression directive bound to a new-file line number.
// At most one of Severity and RuleID is set: Severity is one of the five
// canonical levels for a severity-scoped marker, RuleID is a canonical
// taxonomy id (`sec.injection`) or a category wildcard (`sec.*`) for a
// rule-scoped one, and both are empty for an unscoped marker (suppresses any
// finding on the line). Reason is the free text after the colon, trimmed; it
// is retained so a future surface (or audit log) can echo why a finding was
// silenced.
type Rule struct {
	Severity render.Severity
	RuleID   string
	Reason   string
}

// Key addresses a marker: the canonical path of the file it sits in and its
// new-file line number. Keying by line alone (the pre-1.19 behaviour) let a
// marker in one file silence a finding on the same line of another.
type Key struct {
	Path string
	Line int
}

// Warning reports a marker whose bracket scope could not be understood. The
// marker still suppresses (fail-open, ADR-0027: it is visible in the diff),
// but as an unscoped one, so the typo is surfaced rather than silently
// widening or narrowing what is hidden.
type Warning struct {
	Path string
	Line int
	// Token is the bracket content as written (lowercased).
	Token string
	// UnknownRule is true when the token looked like a rule id (it contains a
	// dot) but is not in the taxonomy; false when it is neither a severity nor
	// a rule-shaped token.
	UnknownRule bool
}

// Suppressions holds the markers found in a diff, keyed by (path, line). Only
// one directive per line is retained (the first match wins; a line with two
// markers is pathological and not worth modeling). The zero value is an empty
// set and is safe to use.
type Suppressions struct {
	rules    map[Key]Rule
	warnings []Warning
}

// Len returns the number of markers. Safe on the zero value.
func (s Suppressions) Len() int { return len(s.rules) }

// At returns the marker at (path, line), if any. The path is canonicalised the
// same way finding fingerprints are, so "./a.go" and "a.go" address the same
// file.
func (s Suppressions) At(path string, line int) (Rule, bool) {
	r, ok := s.rules[Key{Path: diff.CanonicalPath(path), Line: line}]
	return r, ok
}

// Warnings returns the markers with an unrecognised scope, ordered by path
// then line so the output is deterministic.
func (s Suppressions) Warnings() []Warning {
	out := make([]Warning, len(s.warnings))
	copy(out, s.warnings)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// ParseSuppressions scans the ADDED lines of d for commitbrief-ignore
// markers and returns them keyed by (path, new-file line number) — the same
// 1-based numbering a finding's Line field uses (see diff.NumberedString).
// Context and removed lines are ignored: a suppression must be part of the
// change under review, not pre-existing untouched code. Returns an empty set
// when there are no markers.
func ParseSuppressions(d diff.Diff) Suppressions {
	out := Suppressions{rules: make(map[Key]Rule)}
	for _, f := range d.Files {
		path := diff.CanonicalPath(f.Path)
		for _, h := range f.Hunks {
			newNo := h.NewStart
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.LineAdd:
					if r, w, ok := parseLine(l.Text); ok {
						k := Key{Path: path, Line: newNo}
						if _, exists := out.rules[k]; !exists {
							out.rules[k] = r
							if w != nil {
								w.Path, w.Line = path, newNo
								out.warnings = append(out.warnings, *w)
							}
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

// parseLine extracts a Rule from a single source line, or (zero, nil, false)
// if the line carries no marker. The bracket token is classified
// deterministically:
//
//  1. it contains a dot: a rule scope. A canonical taxonomy id, or a
//     `<category>.*` wildcard over a known category, scopes the marker; a
//     rule-shaped token that is neither falls open to unscoped with a Warning.
//  2. otherwise, one of the five severities: a severity scope.
//  3. anything else falls open to unscoped with a Warning.
//
// Failing open (rather than ignoring the marker) matches the pre-existing
// behaviour for a mistyped severity: the marker is visible in the diff either
// way, and the warning makes the typo visible too.
func parseLine(text string) (Rule, *Warning, bool) {
	m := markerRe.FindStringSubmatch(text)
	if m == nil {
		return Rule{}, nil, false
	}
	r := Rule{Reason: strings.TrimSpace(m[2])}
	token := strings.ToLower(strings.TrimSpace(m[1]))
	switch {
	case token == "":
		// unscoped
	case strings.Contains(token, "."):
		if id, ok := resolveRuleScope(token); ok {
			r.RuleID = id
		} else {
			return r, &Warning{Token: token, UnknownRule: true}, true
		}
	case render.Severity(token).IsValid():
		r.Severity = render.Severity(token)
	default:
		return r, &Warning{Token: token}, true
	}
	return r, nil, true
}

// resolveRuleScope validates a dotted token against the closed taxonomy and
// returns its canonical form: a canonical id (aliases resolved) or a
// `<category>.*` wildcard for a category that exists.
func resolveRuleScope(token string) (string, bool) {
	if cat, ok := strings.CutSuffix(token, ".*"); ok {
		for _, r := range rules.Taxonomy() {
			if r.Category() == cat {
				return token, true
			}
		}
		return "", false
	}
	id, fallback := rules.NormaliseRuleID(token)
	if fallback {
		return "", false
	}
	return id, true
}

// matches reports whether rule r suppresses finding f. An unscoped rule
// matches anything; a severity-scoped rule matches only its own severity; a
// rule-scoped rule is compared against the finding's NORMALISED rule id (an
// unstamped or unknown id counts as general.other, like everywhere else), with
// `<category>.*` matching every id of that category.
func (r Rule) matches(f render.Finding) bool {
	switch {
	case r.Severity != "":
		return r.Severity == f.Severity
	case r.RuleID != "":
		id, _ := rules.NormaliseRuleID(f.RuleID)
		if cat, ok := strings.CutSuffix(r.RuleID, ".*"); ok {
			return strings.HasPrefix(id, cat+".")
		}
		return id == r.RuleID
	default:
		return true
	}
}

// Filter splits findings into the ones to KEEP and a count of how many were
// dropped by an inline suppression. A finding is dropped when a matching
// marker sits on its own line OR on the line directly above it (line-1) of the
// SAME file — the "above" case is the idiomatic placement when the offending
// statement is long or the marker would clutter the line. This is a TRUE
// removal (ADR-0027): the kept slice feeds fail-on, JSON findings[], and
// display alike. An empty suppression set keeps everything. The kept slice is
// always non-nil so the nil-means-degrade invariant downstream is preserved.
func Filter(findings []render.Finding, sup Suppressions) (kept []render.Finding, suppressed int) {
	if sup.Len() == 0 {
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

// isSuppressed reports whether finding f is silenced by a marker in its own
// file, on its line or the line directly above. A finding with Line <= 0 (no
// anchor) can't be matched to a source line, so it is never suppressed.
func isSuppressed(f render.Finding, sup Suppressions) bool {
	if f.Line <= 0 {
		return false
	}
	if r, ok := sup.At(f.File, f.Line); ok && r.matches(f) {
		return true
	}
	if r, ok := sup.At(f.File, f.Line-1); ok && r.matches(f) {
		return true
	}
	return false
}
