// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/template"
)

// Severity ranks how urgently a finding should be addressed. The vocabulary
// is the wire contract with the LLM (ADR-0014 §1) and is intentionally
// English-only; UI strings around it are i18n-able at the renderer layer.
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

// IsValid reports whether s is one of the five canonical levels.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo:
		return true
	}
	return false
}

// severityOrder is the display priority of each level (critical → info).
// Any severity missing from this slice is appended after info in encounter
// order; the parser rejects unknown severities before they reach this code.
var severityOrder = []Severity{
	SeverityCritical,
	SeverityHigh,
	SeverityMedium,
	SeverityLow,
	SeverityInfo,
}

// Finding is one review item the LLM returned. See ADR-0014 §1 for the
// full contract, including which fields are required.
type Finding struct {
	Severity    Severity `json:"severity"`
	File        string   `json:"file"`
	Line        int      `json:"line"`
	LineEnd     int      `json:"line_end,omitempty"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Suggestion  string   `json:"suggestion"`
	Language    string   `json:"language,omitempty"`
	Snippet     string   `json:"snippet,omitempty"`
}

// LineRef formats the line reference as "142" when the finding is
// pinned to a single line, or "142-145" when LineEnd is set and
// greater than Line. Used by all renderers (cards, copytext,
// markdown via .LineRef template field) so the range display stays
// consistent. Returns "" when Line <= 0 so callers can branch on
// the missing case rather than emit "path:0".
//
// LineEnd is treated as a closed range — both endpoints inclusive,
// matching how source-code "lines 142–145" reads in human writing.
// Out-of-order values (LineEnd < Line) and equal values (LineEnd ==
// Line) collapse to a single-line ref; we never emit "142-142" or
// "145-142", both of which would be confusing.
func (f Finding) LineRef() string {
	if f.Line <= 0 {
		return ""
	}
	if f.LineEnd > f.Line {
		return fmt.Sprintf("%d-%d", f.Line, f.LineEnd)
	}
	return fmt.Sprintf("%d", f.Line)
}

// PathRef joins File and LineRef with ":" so callers don't repeat
// the conditional logic. Returns File alone when LineRef is empty.
func (f Finding) PathRef() string {
	ref := f.LineRef()
	if ref == "" {
		return f.File
	}
	return f.File + ":" + ref
}

// findingsEnvelope is the JSON shape the LLM is contracted to return.
// Top-level shape: {"findings": [...]}. Extra fields are ignored.
type findingsEnvelope struct {
	Findings []Finding `json:"findings"`
}

// ParseErrorKind classifies why ParseFindings rejected a payload so the
// retry/repair path (ADR-0031) can pick a failure-mode-specific recovery
// prompt instead of a blind re-roll of the identical request.
type ParseErrorKind int

const (
	// ParseErrEmpty — the response was empty (or whitespace only) after
	// trimming; there was no JSON at all. Recovered with a hard "JSON only"
	// reset, same as prose / schema-ignored output.
	ParseErrEmpty ParseErrorKind = iota
	// ParseErrProse — json.Unmarshal failed and the payload does not even look
	// like a JSON attempt (it starts with commentary, not `{`/`[`): the model
	// ignored the schema and wrote prose. Recovered with a hard "JSON only,
	// no prose" reset — asking it to "complete the JSON" would be nonsense.
	ParseErrProse
	// ParseErrMalformedJSON — json.Unmarshal failed but the payload looks like
	// a genuine JSON attempt that is truncated or syntactically broken (often
	// max_tokens exhaustion). Recovered by asking the model to complete/correct
	// the JSON, with a raised token ceiling.
	ParseErrMalformedJSON
	// ParseErrSchema — the payload decoded as JSON but violated the findings
	// contract (unknown severity or a missing required field). Recovered with
	// a clean schema-conformant redraw.
	ParseErrSchema
)

// ParseError is the typed error ParseFindings returns. Its Error() text is
// kept byte-identical to the pre-ADR-0031 messages so existing substring
// assertions keep working; Kind adds machine-readable classification and
// Unwrap exposes the underlying json error for further inspection.
type ParseError struct {
	Kind ParseErrorKind
	msg  string
	err  error
}

func (e *ParseError) Error() string { return e.msg }
func (e *ParseError) Unwrap() error { return e.err }

// ParseFindings decodes the LLM-emitted JSON payload into a slice. The
// returned slice may be empty (a clean review) but is non-nil on success.
// Errors are *ParseError (classifiable via errors.As) and trigger the
// repair-oriented retry, then graceful degrade, at the caller (ADR-0014 §4,
// ADR-0031) — the pipeline never crashes on a malformed response.
func ParseFindings(content string) ([]Finding, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, &ParseError{Kind: ParseErrEmpty, msg: "parse findings: empty content"}
	}
	// Phase-0 salvage (ADR-0031): unwrap a lone ```json … ``` fence pair so a
	// provider that fenced otherwise-valid findings JSON parses cleanly with
	// no retry. Non-fenced input is returned unchanged (byte-identical path).
	trimmed = stripCodeFence(trimmed)
	var env findingsEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		// Distinguish a broken JSON *attempt* (starts with `{`/`[` → truncated
		// or syntactically malformed) from prose the model wrote instead of
		// JSON. The two need different repair prompts (ADR-0031): complete-the-
		// JSON vs a hard schema reset.
		kind := ParseErrMalformedJSON
		if !looksLikeJSON(trimmed) {
			kind = ParseErrProse
		}
		return nil, &ParseError{Kind: kind, msg: fmt.Sprintf("parse findings: %v", err), err: err}
	}
	for i, f := range env.Findings {
		if !f.Severity.IsValid() {
			return nil, &ParseError{Kind: ParseErrSchema, msg: fmt.Sprintf("parse findings: finding %d: unknown severity %q", i, f.Severity)}
		}
		if f.File == "" {
			return nil, &ParseError{Kind: ParseErrSchema, msg: fmt.Sprintf("parse findings: finding %d: missing file", i)}
		}
		if f.Title == "" {
			return nil, &ParseError{Kind: ParseErrSchema, msg: fmt.Sprintf("parse findings: finding %d: missing title", i)}
		}
		if f.Description == "" {
			return nil, &ParseError{Kind: ParseErrSchema, msg: fmt.Sprintf("parse findings: finding %d: missing description", i)}
		}
		if f.Suggestion == "" {
			return nil, &ParseError{Kind: ParseErrSchema, msg: fmt.Sprintf("parse findings: finding %d: missing suggestion", i)}
		}
	}
	if env.Findings == nil {
		return []Finding{}, nil
	}
	return env.Findings, nil
}

// stripCodeFence unwraps a single markdown code-fence pair (```json … ``` or
// ``` … ```) when — and only when — the content is exactly one fenced block:
// a leading fence line and a trailing fence line with the payload between
// them. This is the deterministic Phase-0 salvage (ADR-0031) for providers
// that wrap otherwise-valid findings JSON in fences. It is intentionally
// conservative: it never hunts for a {…} substring inside surrounding prose
// (that would blur failure-mode classification and risk accepting truncated
// fragments). Non-fenced input is returned unchanged so the happy path — and
// therefore the cache key — stays byte-identical.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return s // a single line starting with ``` is not a fence pair
	}
	if !isFenceOpener(strings.TrimSpace(s[:nl])) {
		return s
	}
	body := strings.TrimRight(s[nl+1:], " \t\r\n")
	lastNL := strings.LastIndexByte(body, '\n')
	if strings.TrimSpace(body[lastNL+1:]) != "```" {
		return s // no matching closing fence — leave untouched
	}
	return strings.TrimSpace(body[:lastNL+1])
}

// looksLikeJSON reports whether s begins with a JSON object/array opener,
// used to tell a truncated/broken JSON attempt apart from prose the model
// wrote instead. s is assumed already trimmed and fence-stripped.
func looksLikeJSON(s string) bool {
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

// isFenceOpener reports whether line is a bare ``` optionally followed by a
// simple language tag (letters/digits only, e.g. ```json). Anything else —
// including a prose line that merely happens to start with ``` — is rejected
// so it is not mistaken for a fence.
func isFenceOpener(line string) bool {
	tag := strings.TrimPrefix(line, "```")
	for i := 0; i < len(tag); i++ {
		c := tag[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

// SeverityGroup is the value type produced by GroupBySeverity for template
// consumption. Items preserve the order they appeared in the source slice.
type SeverityGroup struct {
	Severity Severity
	Items    []Finding
}

// GroupBySeverity returns findings grouped by severity, ordered
// critical → info. Empty buckets are omitted so templates can iterate
// without a presence check.
func GroupBySeverity(findings []Finding) []SeverityGroup {
	buckets := make(map[Severity][]Finding)
	for _, f := range findings {
		buckets[f.Severity] = append(buckets[f.Severity], f)
	}
	out := make([]SeverityGroup, 0, len(buckets))
	for _, sev := range severityOrder {
		if items, ok := buckets[sev]; ok && len(items) > 0 {
			out = append(out, SeverityGroup{Severity: sev, Items: items})
		}
	}
	return out
}

// CountFiles returns the number of distinct files referenced across the
// given findings.
func CountFiles(findings []Finding) int {
	seen := make(map[string]struct{}, len(findings))
	for _, f := range findings {
		seen[f.File] = struct{}{}
	}
	return len(seen)
}

// TemplateFuncs is the function map registered on every OUTPUT.md template
// the renderer parses. The set is the public template contract (ADR-0014
// §2); additions are allowed in v1.x but removals require a schema bump.
func TemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"upper":           strings.ToUpper,
		"lower":           strings.ToLower,
		"groupBySeverity": GroupBySeverity,
		"countFiles":      CountFiles,
	}
}

// TemplateData is the value passed to a parsed OUTPUT.md template at
// execution time. Keeping the struct named (rather than a bare slice) lets
// templates write `{{ range .Findings }}` and gives future fields a stable
// home.
type TemplateData struct {
	Findings []Finding
}

// ValidateOutputTemplate is the pre-send guard from ADR-0014 §5. It runs
// three checks against a template body so a malformed user OUTPUT.md fails
// before any provider round-trip:
//
//  1. Parse — text/template syntax check.
//  2. Empty-findings execute — does the template crash on the empty case?
//  3. Sample-findings execute — does the template crash with two findings
//     (critical + info) populated, covering both branches of any
//     severity-based conditional logic.
//
// Returns the first failure verbatim, wrapped with a stable prefix the CLI
// uses to format an i18n'd error message. Returns nil on success.
func ValidateOutputTemplate(content string) error {
	t, err := template.New("output").Funcs(TemplateFuncs()).Parse(content)
	if err != nil {
		return fmt.Errorf("output template parse: %w", err)
	}
	if err := t.Execute(io.Discard, TemplateData{Findings: nil}); err != nil {
		return fmt.Errorf("output template execute (empty findings): %w", err)
	}
	sample := []Finding{
		{
			Severity:    SeverityCritical,
			File:        "internal/auth/session.go",
			Line:        142,
			Title:       "sample critical finding",
			Description: "Validation probe — content not user-visible.",
			Language:    "go",
			Snippet:     "- old\n+ new",
		},
		{
			Severity:    SeverityInfo,
			File:        "internal/util/log.go",
			Line:        7,
			Title:       "sample info finding",
			Description: "Validation probe — content not user-visible.",
		},
	}
	if err := t.Execute(io.Discard, TemplateData{Findings: sample}); err != nil {
		return fmt.Errorf("output template execute (sample findings): %w", err)
	}
	return nil
}
