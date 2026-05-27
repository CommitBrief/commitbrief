// SPDX-License-Identifier: GPL-3.0-or-later

package guard

import (
	"regexp"
	"sort"
	"strings"
)

// SecretMatch describes a single line in the diff that looks like it
// might contain a credential the user shouldn't ship to an LLM. Only the
// line number and the matched-pattern names are recorded — never the
// matched substring itself, so the scanner's own output can't become a
// secondary leak vector via logs, stderr, or cache files.
type SecretMatch struct {
	Line     int      // 1-based line number within the diff string
	Patterns []string // alphabetised pattern names that matched this line
}

// secretPattern pairs a human-readable label with a compiled regex.
// Order in the slice is presentation order only — the scanner runs every
// pattern against every candidate line. Patterns are intentionally tight
// (length floors + format prefixes) so common false positives like a
// random `sk-something-small` string don't generate noise.
type secretPattern struct {
	name  string
	regex *regexp.Regexp
}

var secretPatterns = []secretPattern{
	{"AWS Access Key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"GitHub Token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{36,}`)},
	{"GitLab Token", regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`)},
	{"Anthropic API Key", regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{40,}`)},
	{"OpenAI API Key", regexp.MustCompile(`sk-(?:proj-|live-)?[A-Za-z0-9]{40,}`)},
	{"JWT", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)},
	{"Stripe Live Key", regexp.MustCompile(`sk_live_[A-Za-z0-9]{24,}`)},
	{"PEM Private Key", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
}

// ScanForSecrets walks the diff and reports any added line (prefixed
// with a single `+`, excluding the `+++ b/path` header) that matches one
// or more of the credential patterns. Removed and context lines are
// skipped — the goal is to catch *new* leaks, not to re-flag historical
// content that's already on disk somewhere.
//
// Returns a slice of matches sorted by line number. An empty diff or a
// diff with no `+` lines returns nil — callers can rely on `len(out) == 0`
// as the "all clear" signal.
func ScanForSecrets(diff string) []SecretMatch {
	if diff == "" {
		return nil
	}
	return scanLines(diff, func(line string) (string, bool) {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			return "", false
		}
		return strings.TrimPrefix(line, "+"), true
	})
}

// ScanText runs the same credential patterns against arbitrary text
// (no diff prefixes). Used to scan rules content like COMMITBRIEF.md
// and the output template before they get embedded into the system
// prompt and shipped to the provider. UC-05 in PATCH_ROADMAP. Empty
// input returns nil so callers can rely on len(out)==0 as the "all
// clear" signal.
func ScanText(content string) []SecretMatch {
	if content == "" {
		return nil
	}
	return scanLines(content, func(line string) (string, bool) {
		return line, true
	})
}

// scanLines is the shared engine behind ScanForSecrets and ScanText.
// pickBody returns the substring to match against (or "", false to
// skip the line entirely). Line numbers are 1-based and reflect the
// caller's input string verbatim — skipped lines still count toward
// the index so reported numbers line up with the source.
func scanLines(content string, pickBody func(string) (string, bool)) []SecretMatch {
	lineToPatterns := map[int]map[string]struct{}{}
	for i, line := range strings.Split(content, "\n") {
		body, ok := pickBody(line)
		if !ok {
			continue
		}
		for _, p := range secretPatterns {
			if p.regex.MatchString(body) {
				if lineToPatterns[i+1] == nil {
					lineToPatterns[i+1] = map[string]struct{}{}
				}
				lineToPatterns[i+1][p.name] = struct{}{}
			}
		}
	}
	if len(lineToPatterns) == 0 {
		return nil
	}
	lines := make([]int, 0, len(lineToPatterns))
	for l := range lineToPatterns {
		lines = append(lines, l)
	}
	sort.Ints(lines)
	out := make([]SecretMatch, 0, len(lines))
	for _, l := range lines {
		names := make([]string, 0, len(lineToPatterns[l]))
		for n := range lineToPatterns[l] {
			names = append(names, n)
		}
		sort.Strings(names)
		out = append(out, SecretMatch{Line: l, Patterns: names})
	}
	return out
}

// SecretPatternNames returns the labels of every pattern the scanner
// knows about, sorted alphabetically. Used by docs/tests as the
// authoritative list — keeps drift between the table here and the
// CHANGELOG/README description detectable.
func SecretPatternNames() []string {
	names := make([]string, len(secretPatterns))
	for i, p := range secretPatterns {
		names[i] = p.name
	}
	sort.Strings(names)
	return names
}
