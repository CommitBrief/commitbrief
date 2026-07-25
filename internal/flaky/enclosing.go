// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"os"
	"regexp"
	"strings"
)

// testNamePatterns capture the NAME of a test function header, one regexp per
// language family, tried in order. This is the name-capturing sibling of
// reTestFuncHeader (which only detects the shape, for over-mock scoping); the
// two stay in this package so test-shape knowledge lives in exactly one place.
var testNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bfunc\s+(?:\([^)]*\)\s*)?(Test[A-Za-z0-9_]*)`), // Go
	regexp.MustCompile(`\bdef\s+(test_?\w*)\s*\(`),                      // Python
	regexp.MustCompile("\\b(?:it|test)\\s*\\(\\s*[\"'`]([^\"'`]+)"),     // JS/TS BDD
	regexp.MustCompile(`\bpublic\s+function\s+(test\w*)\s*\(`),          // PHP / PHPUnit
	regexp.MustCompile(`(?:void|async\s+\w+)\s+(\w*[Tt]est\w*)\s*\(`),   // Java / C#-ish
}

// EnclosingTest resolves the name of the test function containing line (1-indexed)
// in the file at path, by scanning upward for the nearest test-function header.
//
// It reads the WORKING TREE, not the staged snapshot (ADR-0033): the command a
// caller binds will run against the worktree too, so resolving against the same
// bytes keeps the name and the execution consistent.
//
// Returns ok=false — never a guess — when the file cannot be read, the line is
// out of range, or no header is found above it. A wrong name would run the wrong
// test and yield a confident, wrong verdict, which is worse than no verdict.
func EnclosingTest(path string, line int) (string, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the diff being reviewed
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) {
		return "", false
	}
	for i := line - 1; i >= 0; i-- {
		for _, re := range testNamePatterns {
			if m := re.FindStringSubmatch(lines[i]); m != nil && m[1] != "" {
				return m[1], true
			}
		}
	}
	return "", false
}
