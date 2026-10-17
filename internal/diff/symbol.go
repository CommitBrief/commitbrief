// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"strings"
	"unicode/utf8"
)

// symbolMaxBytes caps a symbol at git's own funcname buffer size (80 bytes)
// so a symbol taken from a hunk body and one taken from a hunk header are
// bounded identically.
const symbolMaxBytes = 80

// CanonicalPath is the single path canonicalisation shared by the finding
// fingerprint and SymbolFor: backslashes become slashes (on every platform, so
// a Windows-built path hashes like its POSIX twin) and any number of leading
// "./" prefixes are removed.
func CanonicalPath(file string) string {
	file = strings.ReplaceAll(file, "\\", "/")
	for strings.HasPrefix(file, "./") {
		file = strings.TrimPrefix(file, "./")
	}
	return file
}

// SymbolFor returns the normalised enclosing-symbol text for a finding at
// (file, line) — the stable anchor of a finding fingerprint (ADR-0045).
//
// The finding's line is looked up in the diff, right (post-image) side first
// and then the left (pre-image) side, mirroring the anchor order used for
// inline comments. Inside the covering hunk the nearest preceding line that
// satisfies git's default funcname rule wins; when the hunk body has none the
// raw hunk header is used if it satisfies the same rule. Everything else —
// a line <= 0, a file or line outside the diff, no qualifying line — yields
// "". The result is run through normaliseSymbol.
//
// When the same path appears in several file sections (a commit-walk diff is
// a concatenation of per-commit patches) the first matching section in diff
// order is used.
func (d Diff) SymbolFor(file string, line int) string {
	path := CanonicalPath(file)
	if path == "" || line <= 0 {
		return ""
	}
	for _, fd := range d.Files {
		if fd.Path != path {
			continue
		}
		for _, h := range fd.Hunks {
			if line >= h.NewStart && line < h.NewStart+max(h.NewLines, 1) {
				return scanHunkSymbol(h, line, false)
			}
		}
	}
	for _, fd := range d.Files {
		if fd.Path != path && fd.OldPath != path {
			continue
		}
		for _, h := range fd.Hunks {
			if line >= h.OldStart && line < h.OldStart+max(h.OldLines, 1) {
				return scanHunkSymbol(h, line, true)
			}
		}
	}
	return ""
}

// scanHunkSymbol walks the lines visible on one side of h (post-image:
// context + added; pre-image: context + removed), numbered on that side, up
// to and including line, and returns the last funcname-qualifying line.
func scanHunkSymbol(h Hunk, line int, left bool) string {
	n := h.NewStart
	if left {
		n = h.OldStart
	}
	last := ""
	for _, l := range h.Lines {
		visible := l.Kind == LineContext ||
			(!left && l.Kind == LineAdd) ||
			(left && l.Kind == LineDel)
		if !visible {
			continue
		}
		if n > line {
			break
		}
		if isFuncLine(l.Text) {
			last = l.Text
		}
		n++
	}
	if last != "" {
		return normaliseSymbol(last)
	}
	if isFuncLine(h.HeaderRaw) {
		return normaliseSymbol(h.HeaderRaw)
	}
	return ""
}

// isFuncLine reproduces git's default funcname rule: a line qualifies when its
// first byte is an ASCII letter, '_' or '$'. Indented lines, blanks and
// punctuation-led lines (closing braces, comments) never qualify.
func isFuncLine(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// normaliseSymbol makes a funcname line stable across edits that do not change
// which symbol it names: it drops CR and trailing whitespace, caps the text at
// 80 bytes (never splitting a UTF-8 sequence), empties every balanced
// parenthesised group to "()" and cuts at an unbalanced "(", collapses
// whitespace runs, and trims a trailing "{" or ":".
func normaliseSymbol(s string) string {
	s = strings.TrimRight(s, "\r\n\t ")
	if len(s) > symbolMaxBytes {
		s = s[:symbolMaxBytes]
	}
	// Drop a trailing partial UTF-8 sequence whether we cut it or git did:
	// git truncates the funcname to 80 bytes without looking at UTF-8, so a
	// raw hunk header can arrive at exactly 80 bytes already ending mid-rune.
	for len(s) > 0 {
		r, size := utf8.DecodeLastRuneInString(s)
		if r != utf8.RuneError || size != 1 {
			break
		}
		s = s[:len(s)-1]
	}
	s = collapseParens(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(strings.TrimRight(s, "{: "))
}

// collapseParens replaces each balanced top-level "(...)" group (nesting
// included) with "()". An opening parenthesis with no matching close — the
// signature was cut by the 80-byte cap — truncates the string at that point.
func collapseParens(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '(' {
			b.WriteByte(s[i])
			continue
		}
		depth, j := 0, i
		for ; j < len(s); j++ {
			if s[j] == '(' {
				depth++
			} else if s[j] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if depth != 0 {
			break
		}
		b.WriteString("()")
		i = j
	}
	return b.String()
}
