package render

import (
	"encoding/json"
	"errors"
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
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Language    string   `json:"language,omitempty"`
	Snippet     string   `json:"snippet,omitempty"`
}

// findingsEnvelope is the JSON shape the LLM is contracted to return.
// Top-level shape: {"findings": [...]}. Extra fields are ignored.
type findingsEnvelope struct {
	Findings []Finding `json:"findings"`
}

// ParseFindings decodes the LLM-emitted JSON payload into a slice. The
// returned slice may be empty (a clean review) but is non-nil on success.
// Errors trigger graceful degrade at the caller (ADR-0014 §4) — the
// pipeline never crashes on a malformed response.
func ParseFindings(content string) ([]Finding, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, errors.New("parse findings: empty content")
	}
	var env findingsEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return nil, fmt.Errorf("parse findings: %w", err)
	}
	for i, f := range env.Findings {
		if !f.Severity.IsValid() {
			return nil, fmt.Errorf("parse findings: finding %d: unknown severity %q", i, f.Severity)
		}
		if f.File == "" {
			return nil, fmt.Errorf("parse findings: finding %d: missing file", i)
		}
		if f.Title == "" {
			return nil, fmt.Errorf("parse findings: finding %d: missing title", i)
		}
		if f.Description == "" {
			return nil, fmt.Errorf("parse findings: finding %d: missing description", i)
		}
	}
	if env.Findings == nil {
		return []Finding{}, nil
	}
	return env.Findings, nil
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
