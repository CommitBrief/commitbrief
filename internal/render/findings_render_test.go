package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

func sampleFindings() []Finding {
	return []Finding{
		{
			Severity:    SeverityCritical,
			File:        "internal/auth/session.go",
			Line:        142,
			Title:       "SQL fragment built from request input",
			Description: "String concatenation feeds db.Query() directly.",
			Language:    "go",
			Snippet:     "- q := raw\n+ q := param",
		},
		{
			Severity:    SeverityHigh,
			File:        "internal/db/migrate.go",
			Line:        73,
			Title:       "NOT NULL column added without default",
			Description: "Migration will fail on populated tables.",
		},
		{
			Severity:    SeverityInfo,
			File:        "internal/util/log.go",
			Line:        7,
			Title:       "Unused import",
			Description: "context is no longer referenced.",
		},
	}
}

// findingsPayload combines samplePayload's Meta scaffolding with parsed
// findings, simulating the happy ADR-0014 path where the LLM produced
// valid structured output.
func findingsPayload() Payload {
	p := samplePayload()
	// On the happy path Content is the raw JSON string the provider
	// returned; the renderer ignores it once Findings is populated. We set
	// something recognisably JSON-shaped so assertions catch any accidental
	// echo of it into the body.
	p.Content = `{"findings":[{"severity":"critical","file":"a.go","line":1,"title":"x","description":"y"}]}`
	p.Findings = sampleFindings()
	return p
}

func TestCardsPerFindingPanels(t *testing.T) {
	var w bytes.Buffer
	if err := Cards(&w, findingsPayload()); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())

	// Each finding's severity badge, file:line, title, and description must
	// surface somewhere in the body.
	wantStrings := []string{
		"CRITICAL", "HIGH", "INFO",
		"internal/auth/session.go:142",
		"internal/db/migrate.go:73",
		"internal/util/log.go:7",
		"SQL fragment built from request input",
		"NOT NULL column added without default",
		"Unused import",
		"String concatenation feeds db.Query() directly.",
	}
	for _, want := range wantStrings {
		if !strings.Contains(plain, want) {
			t.Errorf("cards body missing %q; got:\n%s", want, plain)
		}
	}
}

func TestCardsOrdersBySeverity(t *testing.T) {
	// Severities are deliberately out of order on the input; the panel
	// stream must come out critical → info regardless.
	p := findingsPayload()
	p.Findings = []Finding{
		{Severity: SeverityInfo, File: "z.go", Line: 1, Title: "info", Description: "d"},
		{Severity: SeverityCritical, File: "a.go", Line: 1, Title: "crit", Description: "d"},
		{Severity: SeverityMedium, File: "m.go", Line: 1, Title: "med", Description: "d"},
	}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())

	iCrit := strings.Index(plain, "CRITICAL")
	iMed := strings.Index(plain, "MEDIUM")
	iInfo := strings.Index(plain, "INFO")
	if iCrit < 0 || iMed < 0 || iInfo < 0 {
		t.Fatalf("missing one of CRITICAL/MEDIUM/INFO in:\n%s", plain)
	}
	if iCrit >= iMed || iMed >= iInfo {
		t.Errorf("severity order wrong: CRITICAL@%d MEDIUM@%d INFO@%d", iCrit, iMed, iInfo)
	}
}

func TestCardsPanelHasSeverityIcon(t *testing.T) {
	// Each severity's badge must be prefixed by its mapped icon glyph so
	// users get a visual anchor independent of color (a11y for users with
	// red/green confusion or NO_COLOR set).
	cases := map[Severity]string{
		SeverityCritical: "‼",
		SeverityHigh:     "⚠",
		SeverityMedium:   "▲",
		SeverityLow:      "●",
		SeverityInfo:     "ⓘ",
	}
	for sev, icon := range cases {
		t.Run(string(sev), func(t *testing.T) {
			p := samplePayload()
			p.Findings = []Finding{{
				Severity: sev, File: "a.go", Line: 1, Title: "t", Description: "d",
			}}
			var w bytes.Buffer
			if err := Cards(&w, p); err != nil {
				t.Fatal(err)
			}
			plain := stripANSI(w.String())
			if !strings.Contains(plain, icon) {
				t.Errorf("panel for %s missing icon %q; got:\n%s", sev, icon, plain)
			}
		})
	}
}

func TestCardsPanelHasBulletSeparator(t *testing.T) {
	// Per the v0.6.0 visual polish: badge and file:line are separated by
	// " • " (U+2022 bullet) so the eye groups severity with its location.
	p := samplePayload()
	p.Findings = []Finding{{
		Severity: SeverityCritical, File: "a.go", Line: 42, Title: "t", Description: "d",
	}}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "CRITICAL • a.go:42") {
		t.Errorf("expected 'CRITICAL • a.go:42' substring; got:\n%s", plain)
	}
}

func TestCardsPanelUsesRoundedBorder(t *testing.T) {
	// Rounded borders use ╭ ╮ ╰ ╯ corner glyphs (lipgloss.RoundedBorder)
	// instead of the ┌ ┐ └ ┘ used by NormalBorder. Easiest visual diff.
	p := samplePayload()
	p.Findings = sampleFindings()
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if !strings.Contains(plain, corner) {
			t.Errorf("rounded-border corner %q missing; got:\n%s", corner, plain)
		}
	}
	for _, sharp := range []string{"┌", "┐", "└", "┘"} {
		if strings.Contains(plain, sharp) {
			t.Errorf("sharp-border corner %q should not appear in rounded layout; got:\n%s", sharp, plain)
		}
	}
}

func TestCardsEmptyFindings(t *testing.T) {
	p := samplePayload()
	p.Findings = []Finding{} // non-nil empty: a clean review
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "No findings. Looks good.") {
		t.Errorf("empty-findings panel missing canonical text; got:\n%s", plain)
	}
	// Empty case must NOT degrade to glamour-rendering Content.
	if strings.Contains(plain, "Looks good\n") && strings.Contains(plain, "# Review") {
		t.Errorf("empty findings should not surface raw Content; got:\n%s", plain)
	}
}

func TestCardsFooterShowsFindingCount(t *testing.T) {
	p := findingsPayload() // 3 findings
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "3 findings") {
		t.Errorf("footer should report '3 findings'; got:\n%s", plain)
	}
}

func TestCardsFooterCountSingular(t *testing.T) {
	p := samplePayload()
	p.Findings = sampleFindings()[:1]
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "1 finding") || strings.Contains(plain, "1 findings") {
		t.Errorf("footer should pluralise to '1 finding' (no s); got:\n%s", plain)
	}
}

func TestCardsDegradesOnNilFindings(t *testing.T) {
	// Explicit degrade test: samplePayload has Findings:nil and Content
	// shaped like Stage A markdown. Cards must render the body via glamour
	// instead of crashing.
	p := samplePayload()
	if p.Findings != nil {
		t.Fatal("samplePayload should leave Findings nil")
	}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "Review") || !strings.Contains(plain, "Looks good") {
		t.Errorf("degrade body missing Stage A content; got:\n%s", plain)
	}
	// Count should be omitted on degrade since we don't know how many
	// findings the raw body actually represents.
	if strings.Contains(plain, "0 findings") || strings.Contains(plain, "1 finding") {
		t.Errorf("degrade footer should not include a finding count; got:\n%s", plain)
	}
}

func TestMarkdownTemplateExecute(t *testing.T) {
	p := findingsPayload()
	p.OutputTemplate = `{{ range .Findings }}- [{{ upper (printf "%s" .Severity) }}] {{ .File }}:{{ .Line }} — {{ .Title }}
{{ end }}`
	var w bytes.Buffer
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	wantLines := []string{
		"- [CRITICAL] internal/auth/session.go:142 — SQL fragment built from request input",
		"- [HIGH] internal/db/migrate.go:73 — NOT NULL column added without default",
		"- [INFO] internal/util/log.go:7 — Unused import",
	}
	for _, want := range wantLines {
		if !strings.Contains(out, want) {
			t.Errorf("template output missing %q; got:\n%s", want, out)
		}
	}
}

func TestMarkdownTemplateWithHelpers(t *testing.T) {
	p := findingsPayload()
	p.OutputTemplate = `{{ len .Findings }} findings across {{ countFiles .Findings }} files
{{- range groupBySeverity .Findings }}
## {{ upper (printf "%s" .Severity) }} ({{ len .Items }})
{{- end }}`
	var w bytes.Buffer
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	for _, want := range []string{
		"3 findings across 3 files",
		"## CRITICAL (1)",
		"## HIGH (1)",
		"## INFO (1)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("helper output missing %q; got:\n%s", want, out)
		}
	}
}

func TestMarkdownDegradesOnNilFindings(t *testing.T) {
	// Findings nil → emit Content verbatim, ignore template.
	p := samplePayload()
	p.OutputTemplate = "THIS TEMPLATE SHOULD BE IGNORED"
	var w bytes.Buffer
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if strings.Contains(out, "THIS TEMPLATE SHOULD BE IGNORED") {
		t.Errorf("degrade path should ignore OutputTemplate; got:\n%s", out)
	}
	if !strings.Contains(out, "# Review") || !strings.Contains(out, "Looks good") {
		t.Errorf("degrade should emit Content; got:\n%s", out)
	}
}

func TestMarkdownDegradesOnEmptyTemplate(t *testing.T) {
	// Findings present but OutputTemplate empty → emit Content.
	p := findingsPayload()
	p.OutputTemplate = ""
	var w bytes.Buffer
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if !strings.Contains(out, `"findings"`) {
		t.Errorf("empty-template path should emit Content (raw JSON); got:\n%s", out)
	}
}

func TestMarkdownTemplateExecuteError(t *testing.T) {
	// .Findings is a slice; .Findings.Foo fails at execute time and the
	// renderer must surface the error rather than silently rendering
	// half-formed output. Pre-send validation (Stage 5) should catch this
	// before it reaches here, so the error path is a regression alarm.
	p := findingsPayload()
	p.OutputTemplate = `{{ .Findings.Foo }}`
	var w bytes.Buffer
	err := Markdown(&w, p)
	if err == nil {
		t.Fatal("Markdown: want execute error, got nil")
	}
	if !strings.Contains(err.Error(), "OUTPUT.md template") {
		t.Errorf("error %q does not include template prefix", err.Error())
	}
}

func TestJSONFindingsPopulated(t *testing.T) {
	var w bytes.Buffer
	if err := JSON(&w, findingsPayload()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(w.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, w.String())
	}
	findings, ok := doc["findings"].([]any)
	if !ok {
		t.Fatalf("findings is not an array; got %T", doc["findings"])
	}
	if len(findings) != 3 {
		t.Fatalf("findings length = %d, want 3", len(findings))
	}
	first := findings[0].(map[string]any)
	if first["severity"] != "critical" {
		t.Errorf("findings[0].severity = %v, want critical", first["severity"])
	}
	if first["file"] != "internal/auth/session.go" {
		t.Errorf("findings[0].file = %v", first["file"])
	}
	if first["line"] != float64(142) {
		t.Errorf("findings[0].line = %v, want 142", first["line"])
	}
}

func TestJSONHappyPathContentEmpty(t *testing.T) {
	// On the happy path content is vestigial — always empty until removed
	// in v2 (ADR-0014).
	var w bytes.Buffer
	if err := JSON(&w, findingsPayload()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Bytes(), &doc)
	if got := doc["content"]; got != "" {
		t.Errorf("content = %q, want empty on happy path", got)
	}
}

func TestJSONDegradeContentPopulated(t *testing.T) {
	// On degrade content gets the raw LLM output so consumers can inspect
	// what came back even when the JSON parse failed upstream.
	var w bytes.Buffer
	if err := JSON(&w, samplePayload()); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Bytes(), &doc)
	content, ok := doc["content"].(string)
	if !ok || content == "" {
		t.Errorf("content should hold raw response on degrade; got %v", doc["content"])
	}
	findings, ok := doc["findings"].([]any)
	if !ok {
		t.Fatalf("findings type wrong: %T", doc["findings"])
	}
	if len(findings) != 0 {
		t.Errorf("findings should be empty on degrade; got %d", len(findings))
	}
}

// TestEmbeddedDefaultOutputValidates is the drift guard for
// internal/rules/output.md. If the embedded default template stops parsing
// or stops executing against either the empty or sample-findings case,
// this trips before release-check.sh ever runs. Pre-send validation in
// runReview/dryrun skips the default for performance — this test ensures
// that skip is safe.
func TestEmbeddedDefaultOutputValidates(t *testing.T) {
	tpl := rules.DefaultOutput().Content
	if tpl == "" {
		t.Fatal("rules.DefaultOutput().Content is empty; embed broken")
	}
	if err := ValidateOutputTemplate(tpl); err != nil {
		t.Fatalf("embedded default OUTPUT.md fails ValidateOutputTemplate: %v", err)
	}
}

func TestJSONEmptyFindingsHappyPath(t *testing.T) {
	// Non-nil empty Findings (clean review) → findings:[] and content:"".
	p := samplePayload()
	p.Findings = []Finding{}
	var w bytes.Buffer
	if err := JSON(&w, p); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(w.Bytes(), &doc)
	if doc["content"] != "" {
		t.Errorf("clean review should empty content; got %v", doc["content"])
	}
	findings, _ := doc["findings"].([]any)
	if len(findings) != 0 {
		t.Errorf("clean review should have empty findings; got %v", findings)
	}
}
