// SPDX-License-Identifier: GPL-3.0-or-later

// Package flaky is CommitBrief's deterministic, static flaky-test
// anti-pattern detector (ADR-0022). Unlike the LLM review path it produces
// reproducible findings from the diff alone: it scans the added lines of
// changed test files for high-precision anti-patterns (hard-coded sleeps,
// unseeded randomness, …) and emits standard render.Finding values — no
// JSON-schema change, no provider call. Recall is intentionally secondary
// to precision: a noisy commit-stage gate erodes trust.
package flaky

import (
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// Detector turns a parsed diff into deterministic flaky-test findings. The
// catalog localizes finding text to the resolved output language (ADR-0021);
// the Severity value stays the English wire vocabulary, as for LLM findings.
type Detector struct {
	cat *i18n.Catalog
}

// New returns a Detector that localizes finding text via cat.
func New(cat *i18n.Catalog) *Detector { return &Detector{cat: cat} }

// Detect scans the added lines of every changed test file in parsed and
// returns the matched anti-patterns as findings. The returned slice is
// non-nil only when there are findings; order follows file → hunk → line.
func (d *Detector) Detect(parsed diff.Diff) []render.Finding {
	var out []render.Finding
	for _, f := range parsed.Files {
		if f.Binary || f.Mode == diff.ModeDeleted {
			continue
		}
		if !isTestFile(f.Path) {
			continue
		}
		lang := detectLang(f.Path)
		for _, h := range f.Hunks {
			// new-file line cursor: context and added lines advance it,
			// deleted lines do not (standard unified-diff walk).
			line := h.NewStart
			for _, l := range h.Lines {
				switch l.Kind {
				case diff.LineContext:
					line++
				case diff.LineAdd:
					out = append(out, d.scanLine(f.Path, lang, line, l.Text)...)
					line++
				case diff.LineDel:
					// does not advance the new-file cursor
				}
			}
		}
	}
	return out
}

// scanLine evaluates one added line against every applicable rule. A line may
// match more than one distinct rule, but a single rule matches a line at most
// once (regexp alternation collapses internal duplicates).
func (d *Detector) scanLine(path, lang string, line int, text string) []render.Finding {
	var out []render.Finding
	for _, r := range rules {
		if !r.appliesTo(lang) || !r.pattern.MatchString(text) {
			continue
		}
		out = append(out, render.Finding{
			Severity:    r.severity,
			File:        path,
			Line:        line,
			Title:       d.cat.T(r.titleKey),
			Description: d.cat.T(r.descKey),
			Suggestion:  d.cat.T(r.sugKey),
			Language:    lang,
			Snippet:     "+" + text,
		})
	}
	return out
}

// isTestFile reports whether path looks like a test file by convention across
// the languages CommitBrief commonly reviews. Conservative on directories so
// non-test fixtures rarely match; the rules themselves are the second filter.
func isTestFile(path string) bool {
	p := strings.ToLower(toSlash(path))
	b := base(p)

	switch {
	case strings.HasSuffix(b, "_test.go"):
		return true
	case strings.HasSuffix(b, "_test.py"), strings.HasPrefix(b, "test_") && strings.HasSuffix(b, ".py"):
		return true
	case strings.HasSuffix(b, "_spec.rb"), strings.HasSuffix(b, "_test.rb"):
		return true
	case strings.HasSuffix(b, "test.java"), strings.HasSuffix(b, "tests.java"):
		return true
	case strings.HasSuffix(b, "test.cs"), strings.HasSuffix(b, "tests.cs"):
		return true
	case strings.HasSuffix(b, "test.php"):
		return true
	case containsAny(b, ".test.", ".spec."):
		return true
	}

	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "__tests__", "tests", "test", "spec", "e2e", "cypress":
			return true
		}
	}
	return false
}

// detectLang maps a file extension to the short language identifier used for
// Finding.Language and for per-rule language gating. Empty when unknown.
func detectLang(path string) string {
	ext := strings.ToLower(extension(path))
	switch ext {
	case ".go":
		return "go"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "js"
	case ".ts", ".tsx":
		return "ts"
	case ".py":
		return "python"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	default:
		return ""
	}
}

// toSlash normalizes separators to "/" without importing path/filepath, so
// path handling is identical on every OS (paths in a diff are already "/").
func toSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

// base returns the final "/"-separated segment of p.
func base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// extension returns the dotted extension of p (including the leading "."),
// or "" when the final segment has none.
func extension(p string) string {
	b := base(p)
	if i := strings.LastIndex(b, "."); i > 0 {
		return b[i:]
	}
	return ""
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
