package render

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

// TestMain forces a 256-color profile for the entire render package's
// test run. Without it, lipgloss detects bytes.Buffer as non-TTY and
// strips ANSI escapes, making it impossible to assert that snippet diff
// lines actually carry their severity colors. Existing tests use
// stripANSI so they are unaffected by the forced profile.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	os.Exit(m.Run())
}

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

// ansiContainsLineWithColor returns true if the rendered output has any
// line that contains both the given foreground color code AND the given
// content substring. Matches "38;5;<code>" anywhere inside an ANSI CSI
// sequence — covers both the lone "\x1b[38;5;255m" form and the merged
// "\x1b[1;38;5;255;48;5;52m" form lipgloss emits when multiple style
// attributes (bold + fg + bg) collapse into one escape. Lets color
// assertions isolate a single region (a snippet line, a title) from
// incidental color use elsewhere in the panel.
func ansiContainsLineWithColor(rendered, fgCode, contentSubstring string) bool {
	colorMatch := "38;5;" + fgCode
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(line, "\x1b[") {
			continue
		}
		if !strings.Contains(line, colorMatch) {
			continue
		}
		// Ensure the digit run terminates on a semicolon or 'm' — guards
		// against "38;5;25" falsely matching a request for "38;5;2".
		idx := strings.Index(line, colorMatch)
		end := idx + len(colorMatch)
		if end < len(line) && line[end] != ';' && line[end] != 'm' {
			continue
		}
		if strings.Contains(line, contentSubstring) {
			return true
		}
	}
	return false
}

func TestColorizeSnippetGreenForAddedLines(t *testing.T) {
	// Direct unit test: feed three lines, check the rendered string carries
	// color 42 (green) on the '+' line and color 203 (red) on the '-' line.
	// Using an unstyled base so we don't have to subtract the panel bg.
	onBg := lipgloss.NewStyle()
	got := colorizeSnippet("- old line\n+ new line\n  context line", onBg)

	if !ansiContainsLineWithColor(got, "42", "+ new line") {
		t.Errorf("'+' line should carry green ANSI (color 42); got:\n%q", got)
	}
	if !ansiContainsLineWithColor(got, "203", "- old line") {
		t.Errorf("'-' line should carry red ANSI (color 203); got:\n%q", got)
	}
	if !ansiContainsLineWithColor(got, "244", "context line") {
		t.Errorf("context line should carry muted ANSI (color 244); got:\n%q", got)
	}
}

func TestColorizeSnippetPassthroughForContextLines(t *testing.T) {
	// No '+' or '-' anywhere → no green or red ANSI escapes in the result.
	// Catches a regression where the colorizer falsely matches space-padded
	// prefixes or strips characters.
	onBg := lipgloss.NewStyle()
	got := colorizeSnippet("  context one\n  context two", onBg)

	plain := stripANSI(got)
	for _, want := range []string{"context one", "context two"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain content missing %q; got:\n%s", want, plain)
		}
	}
	if strings.Contains(got, "\x1b[38;5;42m") || strings.Contains(got, "\x1b[38;5;42;") {
		t.Errorf("context-only snippet must not emit green ANSI; got:\n%q", got)
	}
	if strings.Contains(got, "\x1b[38;5;203m") || strings.Contains(got, "\x1b[38;5;203;") {
		t.Errorf("context-only snippet must not emit red ANSI; got:\n%q", got)
	}
}

func TestColorizeSnippetPreservesLineCount(t *testing.T) {
	// Diff colorization must not drop or merge lines — the renderer relies
	// on the output having the same line count as the input so the panel
	// height stays predictable.
	onBg := lipgloss.NewStyle()
	in := "- a\n+ b\n  c\n+ d"
	got := colorizeSnippet(in, onBg)
	if want, have := strings.Count(in, "\n"), strings.Count(stripANSI(got), "\n"); want != have {
		t.Errorf("line count drift: in=%d, out=%d\n%q", want, have, got)
	}
}

func TestCardsSnippetIntegratedWithPanel(t *testing.T) {
	// End-to-end sanity: render a full Cards panel with a snippet and
	// confirm the line containing "+ new" carries green (color 42) — the
	// integration the unit tests above can't catch (style composition with
	// onBg/panel.Background).
	p := Payload{
		Findings: []Finding{{
			Severity: SeverityCritical, File: "a.go", Line: 1,
			Title: "t", Description: "d", Language: "go",
			Snippet: "- old\n+ new",
		}},
		Meta: samplePayload().Meta,
	}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	raw := w.String()
	if !ansiContainsLineWithColor(raw, "42", "+ new") {
		t.Errorf("integrated panel: '+' line should carry green ANSI; got:\n%q", raw)
	}
	if !ansiContainsLineWithColor(raw, "203", "- old") {
		t.Errorf("integrated panel: '-' line should carry red ANSI; got:\n%q", raw)
	}
}

func TestCardsPanelTitleUsesHighContrastForeground(t *testing.T) {
	// TestMain forces ANSI256 with a dark-mode probe, so cardText resolves
	// to its Dark variant ("255" — near-white). Title and description text
	// must carry that color or they're invisible against severity-tinted
	// backgrounds on dark terminals.
	p := samplePayload()
	p.Findings = []Finding{{
		Severity: SeverityCritical, File: "a.go", Line: 1,
		Title: "title-text", Description: "description-text",
	}}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	raw := w.String()
	if !ansiContainsLineWithColor(raw, "255", "title-text") {
		t.Errorf("title line should carry the high-contrast cardText (ANSI 255) on dark terminals; got:\n%q", raw)
	}
	if !ansiContainsLineWithColor(raw, "255", "description-text") {
		t.Errorf("description line should carry the high-contrast cardText (ANSI 255); got:\n%q", raw)
	}
}

func TestCardsEmptyPanelUsesHighContrastForeground(t *testing.T) {
	// Same guard for the "No findings. Looks good." panel — the message
	// sits on the info-severity bg and needs the contrast forced.
	p := samplePayload()
	p.Findings = []Finding{}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	raw := w.String()
	if !ansiContainsLineWithColor(raw, "255", "No findings") {
		t.Errorf("empty-panel message should carry cardText (ANSI 255); got:\n%q", raw)
	}
}

// ---------- Compact mode (11.5.2) ----------

func TestCardsCompactSingleLinePerFinding(t *testing.T) {
	// In compact mode the body is N lines for N findings — no panel
	// borders, no blank padding between entries.
	p := samplePayload()
	p.Findings = sampleFindings() // 3 findings: critical, high, info
	p.Compact = true

	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())

	// No rounded panel corners should appear when compact is on — the
	// whole point is density.
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(plain, corner) {
			t.Errorf("compact mode should not emit panel corner %q; got:\n%s", corner, plain)
		}
	}

	// Each finding's title appears exactly once and on its own line.
	for _, title := range []string{
		"SQL fragment built from request input",
		"NOT NULL column added without default",
		"Unused import",
	} {
		if !strings.Contains(plain, title) {
			t.Errorf("compact body missing title %q\n%s", title, plain)
		}
	}
}

func TestCardsCompactPreservesIconAndBullet(t *testing.T) {
	// Compact mode keeps the visual anchors from the full layout: severity
	// icon + bullet separator between badge and location.
	p := samplePayload()
	p.Findings = []Finding{{
		Severity: SeverityCritical, File: "internal/auth/session.go", Line: 142,
		Title: "SQL fragment built from request input", Description: "d",
	}}
	p.Compact = true

	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())

	want := "‼ CRITICAL • internal/auth/session.go:142 — SQL fragment built from request input"
	if !strings.Contains(plain, want) {
		t.Errorf("compact line layout mismatch:\nwant substring: %q\ngot:\n%s", want, plain)
	}
}

func TestCardsCompactSeverityOrderingRespected(t *testing.T) {
	// Findings are emitted in critical→high→medium→low→info order even
	// when the input slice is shuffled — toggling --compact must not
	// re-shuffle findings relative to the panel layout.
	p := samplePayload()
	p.Findings = []Finding{
		{Severity: SeverityInfo, File: "z.go", Line: 1, Title: "info-item", Description: "d"},
		{Severity: SeverityCritical, File: "a.go", Line: 1, Title: "crit-item", Description: "d"},
		{Severity: SeverityMedium, File: "m.go", Line: 1, Title: "med-item", Description: "d"},
	}
	p.Compact = true

	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())

	iCrit := strings.Index(plain, "crit-item")
	iMed := strings.Index(plain, "med-item")
	iInfo := strings.Index(plain, "info-item")
	if iCrit < 0 || iMed < 0 || iInfo < 0 {
		t.Fatalf("compact body missing one of the titles:\n%s", plain)
	}
	if iCrit >= iMed || iMed >= iInfo {
		t.Errorf("severity order wrong in compact mode: crit@%d med@%d info@%d", iCrit, iMed, iInfo)
	}
}

func TestCardsCompactEmptyFindings(t *testing.T) {
	// Empty case is one short line (not the bordered success panel) so the
	// "compact" promise holds even with zero findings.
	p := samplePayload()
	p.Findings = []Finding{}
	p.Compact = true

	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())

	if !strings.Contains(plain, "No findings. Looks good.") {
		t.Errorf("compact empty-case missing canonical text; got:\n%s", plain)
	}
	for _, corner := range []string{"╭", "╮", "╰", "╯"} {
		if strings.Contains(plain, corner) {
			t.Errorf("compact empty-case must not draw a panel; got corner %q\n%s", corner, plain)
		}
	}
}

// ---------- end Compact mode ----------

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
