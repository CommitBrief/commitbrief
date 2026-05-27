// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"strings"
	"testing"
)

func TestSeverity_IsValid(t *testing.T) {
	valid := []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityLow, SeverityInfo}
	for _, s := range valid {
		if !s.IsValid() {
			t.Errorf("Severity(%q).IsValid() = false, want true", s)
		}
	}
	invalid := []Severity{"", "blocker", "warning", "BLOCKER", "Critical"}
	for _, s := range invalid {
		if s.IsValid() {
			t.Errorf("Severity(%q).IsValid() = true, want false", s)
		}
	}
}

func TestParseFindings_Valid(t *testing.T) {
	in := `{
	  "findings": [
	    {
	      "severity": "critical",
	      "file": "internal/auth/session.go",
	      "line": 142,
	      "title": "SQL fragment built from request input",
	      "description": "String concatenation feeds db.Query() directly.",
	      "language": "go",
	      "snippet": "- old\n+ new"
	    },
	    {
	      "severity": "info",
	      "file": "internal/util/log.go",
	      "line": 7,
	      "title": "Unused import",
	      "description": "context not referenced."
	    }
	  ]
	}`
	got, err := ParseFindings(in)
	if err != nil {
		t.Fatalf("ParseFindings: unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(findings) = %d, want 2", len(got))
	}
	if got[0].Severity != SeverityCritical {
		t.Errorf("findings[0].Severity = %q, want %q", got[0].Severity, SeverityCritical)
	}
	if got[0].Snippet == "" {
		t.Errorf("findings[0].Snippet should round-trip; got empty")
	}
	if got[1].Language != "" {
		t.Errorf("findings[1].Language = %q, want empty (omitempty)", got[1].Language)
	}
}

func TestParseFindings_EmptyArray(t *testing.T) {
	got, err := ParseFindings(`{"findings": []}`)
	if err != nil {
		t.Fatalf("ParseFindings empty: %v", err)
	}
	if got == nil {
		t.Errorf("ParseFindings returned nil slice; want non-nil empty")
	}
	if len(got) != 0 {
		t.Errorf("len(findings) = %d, want 0", len(got))
	}
}

func TestParseFindings_MissingFindingsKey(t *testing.T) {
	// {} is legal JSON but findings field is absent → slice stays nil internally;
	// we normalize to empty slice so callers don't need a nil check.
	got, err := ParseFindings(`{}`)
	if err != nil {
		t.Fatalf("ParseFindings empty object: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("ParseFindings({}): got=%v, want empty non-nil", got)
	}
}

func TestParseFindings_Errors(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty input", "", "empty content"},
		{"whitespace only", "   \n  ", "empty content"},
		{"malformed json", `{"findings":`, "parse findings"},
		{"unknown severity", `{"findings":[{"severity":"blocker","file":"a.go","line":1,"title":"t","description":"d"}]}`, "unknown severity"},
		{"missing file", `{"findings":[{"severity":"high","file":"","line":1,"title":"t","description":"d"}]}`, "missing file"},
		{"missing title", `{"findings":[{"severity":"high","file":"a.go","line":1,"title":"","description":"d"}]}`, "missing title"},
		{"missing description", `{"findings":[{"severity":"high","file":"a.go","line":1,"title":"t","description":""}]}`, "missing description"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseFindings(tc.in)
			if err == nil {
				t.Fatalf("ParseFindings(%q): want error, got nil", tc.in)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("ParseFindings(%q): error %q does not contain %q", tc.in, err.Error(), tc.want)
			}
		})
	}
}

func TestParseFindings_LineEndRoundTrip(t *testing.T) {
	// line_end is the schema-additive multi-line indicator (see
	// docs/json-schema.md). Parser must round-trip it as an integer
	// and tolerate its absence (single-line findings omit it).
	in := `{
	  "findings": [
	    {"severity":"high","file":"x.go","line":10,"line_end":15,"title":"t","description":"d"},
	    {"severity":"low","file":"y.go","line":3,"title":"t","description":"d"}
	  ]
	}`
	got, err := ParseFindings(in)
	if err != nil {
		t.Fatalf("ParseFindings: %v", err)
	}
	if got[0].LineEnd != 15 {
		t.Errorf("findings[0].LineEnd = %d, want 15", got[0].LineEnd)
	}
	if got[1].LineEnd != 0 {
		t.Errorf("findings[1].LineEnd = %d, want 0 (omitted)", got[1].LineEnd)
	}
}

func TestLineRef(t *testing.T) {
	cases := []struct {
		name string
		f    Finding
		want string
	}{
		{"single line", Finding{Line: 142}, "142"},
		{"multi-line range", Finding{Line: 142, LineEnd: 145}, "142-145"},
		{"line_end equal collapses", Finding{Line: 142, LineEnd: 142}, "142"},
		{"line_end less collapses",
			// Defensive: we never trust LineEnd < Line. Model bugs or
			// truncated streams shouldn't produce "145-142" backwards
			// ranges; collapse to single-line ref.
			Finding{Line: 142, LineEnd: 100}, "142"},
		{"zero line", Finding{Line: 0}, ""},
		{"negative line", Finding{Line: -5}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.LineRef(); got != c.want {
				t.Errorf("LineRef = %q, want %q", got, c.want)
			}
		})
	}
}

func TestPathRef(t *testing.T) {
	cases := []struct {
		name string
		f    Finding
		want string
	}{
		{"file + single line", Finding{File: "a.go", Line: 7}, "a.go:7"},
		{"file + range", Finding{File: "a.go", Line: 7, LineEnd: 12}, "a.go:7-12"},
		{"file only (line missing)", Finding{File: "a.go"}, "a.go"},
		{"file only (line zero)", Finding{File: "a.go", Line: 0}, "a.go"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.f.PathRef(); got != c.want {
				t.Errorf("PathRef = %q, want %q", got, c.want)
			}
		})
	}
}

func TestGroupBySeverity_Ordering(t *testing.T) {
	in := []Finding{
		{Severity: SeverityInfo, File: "z.go", Title: "t", Description: "d"},
		{Severity: SeverityCritical, File: "a.go", Title: "t", Description: "d"},
		{Severity: SeverityMedium, File: "m.go", Title: "t", Description: "d"},
		{Severity: SeverityHigh, File: "h.go", Title: "t", Description: "d"},
		{Severity: SeverityCritical, File: "b.go", Title: "t", Description: "d"},
	}
	groups := GroupBySeverity(in)
	wantOrder := []Severity{SeverityCritical, SeverityHigh, SeverityMedium, SeverityInfo}
	if len(groups) != len(wantOrder) {
		t.Fatalf("len(groups) = %d, want %d", len(groups), len(wantOrder))
	}
	for i, g := range groups {
		if g.Severity != wantOrder[i] {
			t.Errorf("groups[%d].Severity = %q, want %q", i, g.Severity, wantOrder[i])
		}
	}
	// critical bucket should preserve insertion order (a.go before b.go).
	if got := groups[0].Items; len(got) != 2 || got[0].File != "a.go" || got[1].File != "b.go" {
		t.Errorf("critical bucket files = %+v, want [a.go b.go]", got)
	}
}

func TestGroupBySeverity_Empty(t *testing.T) {
	if got := GroupBySeverity(nil); len(got) != 0 {
		t.Errorf("GroupBySeverity(nil) = %+v, want empty", got)
	}
	if got := GroupBySeverity([]Finding{}); len(got) != 0 {
		t.Errorf("GroupBySeverity([]) = %+v, want empty", got)
	}
}

func TestCountFiles(t *testing.T) {
	in := []Finding{
		{File: "a.go"},
		{File: "b.go"},
		{File: "a.go"},
		{File: "c.go"},
	}
	if got, want := CountFiles(in), 3; got != want {
		t.Errorf("CountFiles = %d, want %d", got, want)
	}
	if got := CountFiles(nil); got != 0 {
		t.Errorf("CountFiles(nil) = %d, want 0", got)
	}
}

func TestValidateOutputTemplate_OK(t *testing.T) {
	tpl := `# Review
{{ if .Findings }}
{{ len .Findings }} findings across {{ countFiles .Findings }} file(s).
{{- range groupBySeverity .Findings }}
## {{ upper (printf "%s" .Severity) }} ({{ len .Items }})
{{- range .Items }}
- {{ .File }}:{{ .Line }} — {{ .Title }}
{{- end }}
{{- end }}
{{- else }}
No findings.
{{- end }}
`
	if err := ValidateOutputTemplate(tpl); err != nil {
		t.Fatalf("ValidateOutputTemplate: unexpected error: %v", err)
	}
}

func TestValidateOutputTemplate_ParseError(t *testing.T) {
	tpl := `{{ if .Findings }` // missing closing brace
	err := ValidateOutputTemplate(tpl)
	if err == nil {
		t.Fatal("ValidateOutputTemplate: want parse error, got nil")
	}
	if !strings.Contains(err.Error(), "output template parse") {
		t.Errorf("error %q does not include parse prefix", err.Error())
	}
}

func TestValidateOutputTemplate_UnknownFunc(t *testing.T) {
	tpl := `{{ fooBar .Findings }}`
	err := ValidateOutputTemplate(tpl)
	if err == nil {
		t.Fatal("ValidateOutputTemplate: want unknown-func error, got nil")
	}
	// text/template surfaces unknown funcs at parse time, not execute time.
	if !strings.Contains(err.Error(), "output template parse") {
		t.Errorf("error %q does not include parse prefix", err.Error())
	}
}

func TestValidateOutputTemplate_EmptyExecuteFailure(t *testing.T) {
	// .Findings is a slice; .Findings.Foo errors at execute time because
	// slices don't have fields. The empty-case execute path catches this.
	tpl := `{{ .Findings.Foo }}`
	err := ValidateOutputTemplate(tpl)
	if err == nil {
		t.Fatal("ValidateOutputTemplate: want execute error, got nil")
	}
	if !strings.Contains(err.Error(), "output template execute (empty findings)") {
		t.Errorf("error %q does not include empty-execute prefix", err.Error())
	}
}

func TestValidateOutputTemplate_SampleExecuteFailure(t *testing.T) {
	// .Title is fine on each Finding, but .Title.Foo errors at execute time —
	// and only when a finding actually exists. The sample-case execute path
	// catches templates that crash on populated data after surviving empty.
	tpl := `{{ range .Findings }}{{ .Title.Foo }}{{ end }}`
	err := ValidateOutputTemplate(tpl)
	if err == nil {
		t.Fatal("ValidateOutputTemplate: want sample-execute error, got nil")
	}
	if !strings.Contains(err.Error(), "output template execute (sample findings)") {
		t.Errorf("error %q does not include sample-execute prefix", err.Error())
	}
}
