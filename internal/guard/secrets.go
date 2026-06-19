// SPDX-License-Identifier: GPL-3.0-or-later

package guard

import (
	"fmt"
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

// UserSecretPattern is one user-supplied credential spec (ADR-0024): a
// human-readable Name and a Regex source string. It is the input shape
// for CompileUserPatterns and mirrors config.SecretPatternConfig without
// importing the config package (guard stays leaf-level). The compiled
// form (secretPattern) is unexported; callers pass the result of
// CompileUserPatterns to ScanForSecretsWith / ScanTextWith.
type UserSecretPattern struct {
	Name  string
	Regex string
}

// CompileUserPatterns turns user-supplied {Name, Regex} specs into the
// internal compiled form so they can be merged with the built-ins
// (ADR-0024). It is purely additive — the built-ins always run; this only
// supplies extras. A spec with an empty name, an empty regex, or an
// invalid regex returns a nil slice and an error naming the offending
// pattern, so the review can fail fast with an actionable message instead
// of silently skipping the pattern. A nil/empty input returns (nil, nil)
// so the common "no user patterns" path stays allocation-free.
func CompileUserPatterns(specs []UserSecretPattern) ([]secretPattern, error) {
	if len(specs) == 0 {
		return nil, nil
	}
	out := make([]secretPattern, 0, len(specs))
	for _, s := range specs {
		name := strings.TrimSpace(s.Name)
		if name == "" {
			return nil, fmt.Errorf("secret pattern has an empty name")
		}
		if strings.TrimSpace(s.Regex) == "" {
			return nil, fmt.Errorf("secret pattern %q has an empty regex", name)
		}
		re, err := regexp.Compile(s.Regex)
		if err != nil {
			return nil, fmt.Errorf("secret pattern %q: invalid regex: %w", name, err)
		}
		out = append(out, secretPattern{name: name, regex: re})
	}
	return out, nil
}

// ScanForSecrets walks the diff and reports any added line (prefixed
// with a single `+`, excluding the `+++ b/path` header) that matches one
// or more of the built-in credential patterns. Thin wrapper over
// ScanForSecretsWith(diff, nil) for callers with no user patterns.
//
// Returns a slice of matches sorted by line number. An empty diff or a
// diff with no `+` lines returns nil — callers can rely on `len(out) == 0`
// as the "all clear" signal.
func ScanForSecrets(diff string) []SecretMatch {
	return ScanForSecretsWith(diff, nil)
}

// ScanForSecretsWith is ScanForSecrets plus extra user-supplied patterns
// (ADR-0024). The built-ins always run; extra is appended (de-duped by
// name, built-in wins) so a user pattern can never silence a built-in.
// Removed and context lines are skipped — the goal is to catch *new*
// leaks, not to re-flag historical content already on disk.
func ScanForSecretsWith(diff string, extra []secretPattern) []SecretMatch {
	if diff == "" {
		return nil
	}
	return scanLines(diff, mergePatterns(extra), func(line string) (string, bool) {
		if !strings.HasPrefix(line, "+") || strings.HasPrefix(line, "+++") {
			return "", false
		}
		return strings.TrimPrefix(line, "+"), true
	})
}

// ScanText runs the built-in credential patterns against arbitrary text
// (no diff prefixes). Thin wrapper over ScanTextWith(content, nil).
// Used to scan rules content like COMMITBRIEF.md and the output template
// before they get embedded into the system prompt and shipped to the
// provider. UC-05 in PATCH_ROADMAP. Empty input returns nil so callers
// can rely on len(out)==0 as the "all clear" signal.
func ScanText(content string) []SecretMatch {
	return ScanTextWith(content, nil)
}

// ScanTextWith is ScanText plus extra user-supplied patterns (ADR-0024).
// Same merge semantics as ScanForSecretsWith.
func ScanTextWith(content string, extra []secretPattern) []SecretMatch {
	if content == "" {
		return nil
	}
	return scanLines(content, mergePatterns(extra), func(line string) (string, bool) {
		return line, true
	})
}

// mergePatterns returns the built-ins followed by any extra user patterns,
// de-duping by name with the built-in winning so a user pattern can never
// shadow or silence a built-in (the feature is additive only, ADR-0024).
// extra == nil short-circuits to the package slice so the common no-user-
// patterns path allocates nothing.
func mergePatterns(extra []secretPattern) []secretPattern {
	if len(extra) == 0 {
		return secretPatterns
	}
	seen := make(map[string]struct{}, len(secretPatterns)+len(extra))
	merged := make([]secretPattern, 0, len(secretPatterns)+len(extra))
	for _, p := range secretPatterns {
		seen[p.name] = struct{}{}
		merged = append(merged, p)
	}
	for _, p := range extra {
		if _, dup := seen[p.name]; dup {
			continue
		}
		seen[p.name] = struct{}{}
		merged = append(merged, p)
	}
	return merged
}

// scanLines is the shared engine behind the Scan* functions. patterns is
// the effective set (built-ins + any user extras). pickBody returns the
// substring to match against (or "", false to skip the line entirely).
// Line numbers are 1-based and reflect the caller's input string verbatim
// — skipped lines still count toward the index so reported numbers line
// up with the source.
func scanLines(content string, patterns []secretPattern, pickBody func(string) (string, bool)) []SecretMatch {
	lineToPatterns := map[int]map[string]struct{}{}
	for i, line := range strings.Split(content, "\n") {
		body, ok := pickBody(line)
		if !ok {
			continue
		}
		for _, p := range patterns {
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

// SecretPatternNames returns the labels of every built-in pattern the
// scanner knows about, sorted alphabetically. Used by docs/tests as the
// authoritative list — keeps drift between the table here and the
// CHANGELOG/README description detectable. User patterns are not included
// (use AllPatternNames for the effective set).
func SecretPatternNames() []string {
	return AllPatternNames(nil)
}

// AllPatternNames returns the labels of every pattern in the effective
// set (built-ins + extra user patterns), de-duped and sorted
// alphabetically. extra == nil yields exactly the built-in list, so
// SecretPatternNames delegates here.
func AllPatternNames(extra []secretPattern) []string {
	patterns := mergePatterns(extra)
	names := make([]string, len(patterns))
	for i, p := range patterns {
		names[i] = p.name
	}
	sort.Strings(names)
	return names
}
