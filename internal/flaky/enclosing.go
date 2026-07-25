// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"os"
	"regexp"
	"strings"
)

// identChar is the Unicode-aware identifier-tail class used by every pattern
// below. Go's regexp (RE2) defines \w and hand-rolled classes like
// [A-Za-z0-9_] as ASCII-only, which silently truncates any non-ASCII
// identifier at the first non-ASCII rune -- routine input, not an edge case,
// since source identifiers are frequently written in the developer's own
// language. A truncated name is worse than useless here: rendered into a
// sandbox-rerun template it becomes a pattern that matches zero tests, which
// `go test` (and equivalents) report as a successful run -- a confident,
// wrong "the flake did not reproduce" verdict (ADR-0033 §10).
const identChar = `[\p{L}\p{N}_]`

// testNamePatterns capture the NAME of a test function header, one regexp per
// language family, tried in order. This is the name-capturing sibling of
// reTestFuncHeader (which only detects the shape, for over-mock scoping); the
// two stay in this package so test-shape knowledge lives in exactly one place.
var testNamePatterns = []*regexp.Regexp{
	regexp.MustCompile(`\bfunc\s+(?:\([^)]*\)\s*)?(Test` + identChar + `*)`),                                       // Go
	regexp.MustCompile(`\bdef\s+(test_?` + identChar + `*)\s*\(`),                                                  // Python
	regexp.MustCompile("\\b(?:it|test)\\s*\\(\\s*[\"'`]([^\"'`]+)"),                                                // JS/TS BDD
	regexp.MustCompile(`\bpublic\s+function\s+(test` + identChar + `*)\s*\(`),                                      // PHP / PHPUnit
	regexp.MustCompile(`(?:void|async\s+` + identChar + `+)\s+(` + identChar + `*[Tt]est` + identChar + `*)\s*\(`), // Java / C#-ish
}

// matchTestName returns the captured test name from the first pattern in
// testNamePatterns that matches text, or ("", false) when none does.
func matchTestName(text string) (string, bool) {
	for _, re := range testNamePatterns {
		if m := re.FindStringSubmatch(text); m != nil && m[1] != "" {
			return m[1], true
		}
	}
	return "", false
}

// EnclosingTest resolves the name of the test function containing line (1-indexed)
// in the file at path, by scanning the file for the test-function scope that
// genuinely contains line -- not merely the nearest header above it.
//
// It reads the WORKING TREE, not the staged snapshot (ADR-0033): the command a
// caller binds will run against the worktree too, so resolving against the same
// bytes keeps the name and the execution consistent.
//
// Returns ok=false — never a guess — when the file cannot be read, the line is
// out of range, or no test scope is found to genuinely enclose it. A wrong
// name would run the wrong test and yield a confident, wrong verdict, which is
// worse than no verdict (ADR-0033 §10) -- so a header merely appearing above
// line is not enough: the scope it opens must not have already closed by the
// time line is reached.
func EnclosingTest(path string, line int) (string, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the diff being reviewed
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(data), "\n")
	if line < 1 || line > len(lines) {
		return "", false
	}
	if detectLang(path) == "python" {
		return enclosingTestIndented(lines, line)
	}
	return enclosingTestBraced(lines, line)
}

// braceScope is one open test-function scope in enclosingTestBraced: name is
// the captured test name, depth is the brace-nesting depth immediately after
// the scope's opening '{' (the depth value while inside its body).
type braceScope struct {
	name  string
	depth int
}

// enclosingTestBraced resolves the test enclosing line in a brace-delimited
// language (Go, Java/C#, JS/TS, PHP) with a single forward pass that tracks
// brace-nesting depth. A test header attaches only to the *next* opening
// brace, and its scope is popped the instant nesting returns to the depth it
// was opened at -- so a helper declared after a test's closing brace is never
// mistaken for still being inside it, unlike a purely-upward regex scan.
func enclosingTestBraced(lines []string, line int) (string, bool) {
	depth := 0
	var stack []braceScope
	pending := ""

	for i, text := range lines {
		if pending == "" {
			if name, ok := matchTestName(text); ok {
				pending = name
			}
		}
		for _, ch := range text {
			switch ch {
			case '{':
				depth++
				if pending != "" {
					stack = append(stack, braceScope{name: pending, depth: depth})
					pending = ""
				}
			case '}':
				if len(stack) > 0 && depth == stack[len(stack)-1].depth {
					stack = stack[:len(stack)-1]
				}
				depth--
			}
		}
		if i+1 == line {
			if len(stack) == 0 {
				return "", false
			}
			return stack[len(stack)-1].name, true
		}
	}
	return "", false
}

// indentScope is one open test-function scope in enclosingTestIndented: name
// is the captured test name, indent is the leading-whitespace width of the
// "def" line itself.
type indentScope struct {
	name   string
	indent int
}

// enclosingTestIndented resolves the test enclosing line in Python with a
// single forward pass that tracks indentation instead of braces. Per
// Python's own dedent rule, a scope closes the moment a later non-blank
// line's indentation drops back to (or below) the width of the def line that
// opened it -- so a helper declared after a test at the same or a shallower
// indent is never mistaken for still being inside it. Blank lines never
// affect scope: their own indentation (often zero) is not meaningful.
func enclosingTestIndented(lines []string, line int) (string, bool) {
	var stack []indentScope

	for i, text := range lines {
		if strings.TrimSpace(text) != "" {
			indent := leadingWhitespace(text)
			for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
				stack = stack[:len(stack)-1]
			}
			if name, ok := matchTestName(text); ok {
				stack = append(stack, indentScope{name: name, indent: indent})
			}
		}
		if i+1 == line {
			if len(stack) == 0 {
				return "", false
			}
			return stack[len(stack)-1].name, true
		}
	}
	return "", false
}

// leadingWhitespace returns the count of leading space/tab runes in s.
func leadingWhitespace(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}
