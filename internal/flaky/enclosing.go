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

// testNamePattern pairs a language's header pattern with which stripped view
// of the line it should be matched against. Every pattern except the JS/TS
// one runs against the fully-stripped view (comments and every string/rune
// literal blanked): a real "func TestX(", "def test_x(", etc. header is
// never legitimately written inside a literal, so matching there is only
// ever a false positive -- e.g. log.Println("func TestFake(t *testing.T)
// {"). The JS/TS pattern is the one exception: its captured name IS the
// quoted BDD description, so it runs against the comments-only-stripped
// view, which keeps string content intact.
type testNamePattern struct {
	re          *regexp.Regexp
	preserveStr bool
}

// testNamePatterns capture the NAME of a test function header, one pattern
// per language family, tried in order. This is the name-capturing sibling of
// reTestFuncHeader (which only detects the shape, for over-mock scoping); the
// two stay in this package so test-shape knowledge lives in exactly one place.
var testNamePatterns = []testNamePattern{
	{re: regexp.MustCompile(`\bfunc\s+(?:\([^)]*\)\s*)?(Test` + identChar + `*)`)},                                       // Go
	{re: regexp.MustCompile(`\bdef\s+(test_?` + identChar + `*)\s*\(`)},                                                  // Python
	{re: regexp.MustCompile("\\b(?:it|test)\\s*\\(\\s*[\"'`]([^\"'`]+)"), preserveStr: true},                             // JS/TS BDD
	{re: regexp.MustCompile(`\bpublic\s+function\s+(test` + identChar + `*)\s*\(`)},                                      // PHP / PHPUnit
	{re: regexp.MustCompile(`(?:void|async\s+` + identChar + `+)\s+(` + identChar + `*[Tt]est` + identChar + `*)\s*\(`)}, // Java / C#-ish
}

// matchTestName returns the captured test name for one line, trying each
// pattern in testNamePatterns in order against the view it declares.
func matchTestName(codeOnly, quotesPreserved string) (string, bool) {
	for _, p := range testNamePatterns {
		text := codeOnly
		if p.preserveStr {
			text = quotesPreserved
		}
		if m := p.re.FindStringSubmatch(text); m != nil && m[1] != "" {
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
// time line is reached, and neither the header nor the braces that define its
// scope may come from inside a string or comment.
func EnclosingTest(path string, line int) (string, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // G304: path comes from the diff being reviewed
	if err != nil {
		return "", false
	}
	src := string(data)

	if detectLang(path) == "python" {
		lines := strings.Split(stripPythonLike(src), "\n")
		if line < 1 || line > len(lines) {
			return "", false
		}
		return enclosingTestIndented(lines, line)
	}

	codeLines := strings.Split(stripBraceLike(src, true), "\n")
	quotedLines := strings.Split(stripBraceLike(src, false), "\n")
	if line < 1 || line > len(codeLines) {
		return "", false
	}
	return enclosingTestBraced(codeLines, quotedLines, line)
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
// brace-nesting depth. codeLines has comments and every string/rune literal
// blanked, so a brace that only appears inside a fixture string or a
// comment can never desync the depth counter; quotedLines has only comments
// blanked, keeping string content intact for the one pattern (JS/TS) that
// needs to read inside a literal. A test header attaches only to the *next*
// opening brace, and its scope is popped the instant nesting returns to the
// depth it was opened at, so a helper declared after a test's closing brace
// is never mistaken for still being inside it.
func enclosingTestBraced(codeLines, quotedLines []string, line int) (string, bool) {
	depth := 0
	var stack []braceScope
	pending := ""

	for i, text := range codeLines {
		if pending == "" {
			if name, ok := matchTestName(text, quotedLines[i]); ok {
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
// single forward pass that tracks indentation instead of braces, over lines
// already stripped by stripPythonLike (comments and every string literal,
// including triple-quoted docstrings, blanked). Without that, a docstring
// line that happens to sit at column 0 -- or even look like a "def" header
// -- would desync the tracker exactly as an unstripped brace desyncs the
// brace tracker; a fully-blanked line reads as blank and is skipped, just
// like a real blank line. Per Python's own dedent rule, a scope closes the
// moment a later non-blank line's indentation drops back to (or below) the
// width of the def line that opened it, so a helper declared after a test
// at the same or a shallower indent is never mistaken for still being
// inside it.
func enclosingTestIndented(lines []string, line int) (string, bool) {
	var stack []indentScope

	for i, text := range lines {
		if strings.TrimSpace(text) != "" {
			indent := leadingWhitespace(text)
			for len(stack) > 0 && indent <= stack[len(stack)-1].indent {
				stack = stack[:len(stack)-1]
			}
			if name, ok := matchTestName(text, text); ok {
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

// stripBraceLike returns a view of src (Go/Java/C#/PHP/JS-TS-family source)
// with comments always blanked and, when blankStrings is true, the content
// of every string/rune/backtick literal blanked too -- but never the
// newlines, so line numbers and later strings.Split are unaffected either
// way. blankStrings=true is the view used for brace-depth counting and for
// matching every header pattern except JS/TS's: a brace or a header-shaped
// run of text inside a literal is not real code and must not be counted or
// matched as if it were. blankStrings=false keeps literal content intact --
// the view JS/TS's pattern needs, since its captured test name IS the
// quoted BDD description.
//
// This is a small hand-rolled state machine, not a full lexer: backslash
// escapes are honoured inside "..."/'...' (so \" does not close a string),
// backtick literals have none (matching Go raw strings) and may span lines,
// and /* */ may span lines while // runs to end of line. Anything this
// cannot classify with that little state is left as ordinary code -- the
// conservative direction, since an unrecognised construct then keeps
// whatever brace/header exposure it already had rather than the function
// inventing new blanking behaviour for a guessed construct.
func stripBraceLike(src string, blankStrings bool) string {
	var b strings.Builder
	b.Grow(len(src))
	runes := []rune(src)
	n := len(runes)
	i := 0

	blankRune := func(r rune) {
		if r == '\n' {
			b.WriteRune('\n')
		} else {
			b.WriteByte(' ')
		}
	}
	literalRune := func(r rune) {
		if r == '\n' {
			b.WriteRune('\n')
		} else if blankStrings {
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}

	for i < n {
		r := runes[i]
		switch {
		case r == '/' && i+1 < n && runes[i+1] == '/':
			for i < n && runes[i] != '\n' {
				blankRune(runes[i])
				i++
			}
		case r == '/' && i+1 < n && runes[i+1] == '*':
			blankRune(r)
			blankRune(runes[i+1])
			i += 2
			for i < n {
				if runes[i] == '*' && i+1 < n && runes[i+1] == '/' {
					blankRune(runes[i])
					blankRune(runes[i+1])
					i += 2
					break
				}
				blankRune(runes[i])
				i++
			}
		case r == '`':
			literalRune(r)
			i++
			for i < n && runes[i] != '`' {
				literalRune(runes[i])
				i++
			}
			if i < n {
				literalRune(runes[i])
				i++
			}
		case r == '"' || r == '\'':
			delim := r
			literalRune(r)
			i++
			for i < n && runes[i] != delim && runes[i] != '\n' {
				if runes[i] == '\\' && i+1 < n && runes[i+1] != '\n' {
					literalRune(runes[i])
					literalRune(runes[i+1])
					i += 2
					continue
				}
				literalRune(runes[i])
				i++
			}
			if i < n && runes[i] == delim {
				literalRune(runes[i])
				i++
			}
		default:
			b.WriteRune(r)
			i++
		}
	}
	return b.String()
}

// stripPythonLike returns a view of src with # comments and every string
// literal -- including triple-quoted docstrings, which may span lines --
// blanked. Newlines always pass through, so line numbers and
// strings.Split are unaffected. Unlike stripBraceLike this has no
// "preserve" mode: Python's own header pattern never needs to read inside a
// literal, so every use blanks unconditionally. Escapes inside a
// single-line "..."/'...' string are honoured; triple-quoted strings are
// closed only by the matching delimiter run of three, with no escape
// handling inside them (the same hand-rolled-state-machine, not-a-lexer
// trade-off as stripBraceLike).
func stripPythonLike(src string) string {
	var b strings.Builder
	b.Grow(len(src))
	runes := []rune(src)
	n := len(runes)
	i := 0

	blank := func(r rune) {
		if r == '\n' {
			b.WriteRune('\n')
		} else {
			b.WriteByte(' ')
		}
	}

	for i < n {
		r := runes[i]
		switch {
		case r == '#':
			for i < n && runes[i] != '\n' {
				blank(runes[i])
				i++
			}
		case (r == '"' || r == '\'') && i+2 < n && runes[i+1] == r && runes[i+2] == r:
			delim := r
			blank(runes[i])
			blank(runes[i+1])
			blank(runes[i+2])
			i += 3
			for i < n {
				if runes[i] == delim && i+2 < n && runes[i+1] == delim && runes[i+2] == delim {
					blank(runes[i])
					blank(runes[i+1])
					blank(runes[i+2])
					i += 3
					break
				}
				blank(runes[i])
				i++
			}
		case r == '"' || r == '\'':
			delim := r
			blank(runes[i])
			i++
			for i < n && runes[i] != delim && runes[i] != '\n' {
				if runes[i] == '\\' && i+1 < n && runes[i+1] != '\n' {
					blank(runes[i])
					blank(runes[i+1])
					i += 2
					continue
				}
				blank(runes[i])
				i++
			}
			if i < n && runes[i] == delim {
				blank(runes[i])
				i++
			}
		default:
			b.WriteRune(r)
			i++
		}
	}
	return b.String()
}
