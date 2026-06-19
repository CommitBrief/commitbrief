// SPDX-License-Identifier: GPL-3.0-or-later

package guard

import (
	"regexp"
	"sort"
	"strings"
)

// InjectionMatch describes a single line in a user-authored rules file
// (a non-default COMMITBRIEF.md or OUTPUT.md template) whose text looks
// like an attempt to override the system prompt. Like SecretMatch it
// records only the 1-based line number and the matched category labels —
// never the raw line — so the warning output can't itself become noise or
// a copy of whatever the user wrote. ADR-0025.
type InjectionMatch struct {
	Line     int      // 1-based line number within the scanned content
	Patterns []string // alphabetised category labels that matched this line
}

// injectionPattern pairs a human-readable category label with a
// case-insensitive regex. The labels are deliberately coarse (a category,
// not the exact phrase) so the warning is informative without echoing the
// user's content. Patterns target the common prompt-injection idioms; they
// are intentionally loose because this is a non-blocking *warning* on the
// user's own file, not a hard guard — a missed phrase costs nothing and a
// false positive only prints one extra advisory line.
type injectionPattern struct {
	label string
	regex *regexp.Regexp
}

// injectionPatterns is the built-in catalogue. `(?i)` makes every match
// case-insensitive. Kept package-global and compiled once at init, mirroring
// secretPatterns.
var injectionPatterns = []injectionPattern{
	{"ignore-instructions", regexp.MustCompile(`(?i)ignore\s+(all\s+)?(the\s+)?(previous|prior|above|preceding|earlier)\s+(instructions|directions|prompts?|rules?)`)},
	{"disregard-instructions", regexp.MustCompile(`(?i)disregard\s+(all\s+)?(the\s+)?(previous|prior|above|preceding|earlier|foregoing)`)},
	{"forget-instructions", regexp.MustCompile(`(?i)forget\s+(everything|all|the\s+(previous|prior|above))`)},
	{"role-override", regexp.MustCompile(`(?i)you\s+are\s+now\b`)},
	{"system-prompt-reference", regexp.MustCompile(`(?i)system\s+prompt`)},
	{"new-instructions", regexp.MustCompile(`(?i)new\s+instructions\s*:`)},
	{"override-directive", regexp.MustCompile(`(?i)(override|overrule|bypass)\s+(the\s+)?(system|previous|prior|above|your)\s+(instructions?|prompt|rules?)`)},
}

// ScanForInjection walks arbitrary text (a user's rules content) and
// reports any line that matches one or more prompt-injection patterns,
// case-insensitively. It is intended for a NON-DEFAULT COMMITBRIEF.md and
// the user's OUTPUT.md template — the caller is responsible for skipping
// the trusted embedded defaults (ADR-0025).
//
// Returns matches sorted by line number; empty/clean input returns nil so
// callers can rely on len(out)==0 as the "nothing to warn about" signal.
// Line numbers are 1-based and count every line in the input verbatim.
func ScanForInjection(content string) []InjectionMatch {
	if content == "" {
		return nil
	}
	lineToLabels := map[int]map[string]struct{}{}
	for i, line := range strings.Split(content, "\n") {
		for _, p := range injectionPatterns {
			if p.regex.MatchString(line) {
				if lineToLabels[i+1] == nil {
					lineToLabels[i+1] = map[string]struct{}{}
				}
				lineToLabels[i+1][p.label] = struct{}{}
			}
		}
	}
	if len(lineToLabels) == 0 {
		return nil
	}
	lines := make([]int, 0, len(lineToLabels))
	for l := range lineToLabels {
		lines = append(lines, l)
	}
	sort.Ints(lines)
	out := make([]InjectionMatch, 0, len(lines))
	for _, l := range lines {
		labels := make([]string, 0, len(lineToLabels[l]))
		for label := range lineToLabels[l] {
			labels = append(labels, label)
		}
		sort.Strings(labels)
		out = append(out, InjectionMatch{Line: l, Patterns: labels})
	}
	return out
}

// InjectionLines flattens a slice of InjectionMatch into a sorted,
// comma-free list of the 1-based line numbers that matched. Convenience for
// the CLI's single-line warning ("lines: 4, 9") so the formatting logic
// doesn't have to live in the i18n call site.
func InjectionLines(matches []InjectionMatch) []int {
	out := make([]int, 0, len(matches))
	for _, m := range matches {
		out = append(out, m.Line)
	}
	return out
}
