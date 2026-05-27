// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

// TestMain forces a TrueColor profile so the hex palette ported from
// the secguard prototype emits as 24-bit ANSI escapes (`38;2;R;G;B` /
// `48;2;R;G;B`) we can match in tests. Without it, lipgloss treats
// bytes.Buffer as non-TTY and downgrades / strips colors.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// hexToTrueColor converts "#RRGGBB" to the "<R>;<G>;<B>" fragment
// inside a `38;2;…` or `48;2;…` ANSI CSI. Test helper only — the
// production code feeds hex strings directly to lipgloss.Color.
func hexToTrueColor(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	r, _ := strconv.ParseUint(hex[0:2], 16, 8)
	g, _ := strconv.ParseUint(hex[2:4], 16, 8)
	b, _ := strconv.ParseUint(hex[4:6], 16, 8)
	return fmt.Sprintf("%d;%d;%d", r, g, b)
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
	// Each severity's chip label prefixes the uppercased name with a
	// distinct glyph (secguard palette). A11y guard: even on NO_COLOR
	// terminals the user can tell critical from low at a glance.
	cases := map[Severity]string{
		SeverityCritical: "💥",
		SeverityHigh:     "🚨",
		SeverityMedium:   "⚡",
		SeverityLow:      "📌",
		SeverityInfo:     "💡",
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
	// Secguard layout puts a 2-space gap + middle-dot · between the
	// severity chip and the file:line path. Catches a regression where
	// the chip and path glue together visually.
	p := samplePayload()
	p.Findings = []Finding{{
		Severity: SeverityCritical, File: "a.go", Line: 42, Title: "t", Description: "d",
	}}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "CRITICAL  · a.go:42") {
		t.Errorf("expected 'CRITICAL  · a.go:42' substring; got:\n%s", plain)
	}
}

func TestCardsPanelShowsLineRange(t *testing.T) {
	// When LineEnd > Line the header should render "file:start-end"
	// instead of just "file:start". Same path used in the bullet
	// separator above, so we only need to assert on the range form.
	p := samplePayload()
	p.Findings = []Finding{{
		Severity: SeverityHigh, File: "internal/db/migrate.go",
		Line: 73, LineEnd: 91, Title: "t", Description: "d",
	}}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "internal/db/migrate.go:73-91") {
		t.Errorf("expected range 'file:73-91' in panel header; got:\n%s", plain)
	}
}

func TestCardsCompactShowsLineRange(t *testing.T) {
	// Compact mode shares PathRef with the panel layout, so the range
	// form must also surface there. Density mode is where a user is
	// most likely to scan many findings and want spans at a glance.
	p := samplePayload()
	p.Findings = []Finding{{
		Severity: SeverityMedium, File: "x.go",
		Line: 10, LineEnd: 25, Title: "block-level finding",
		Description: "spans multiple lines",
	}}
	p.Compact = true
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if !strings.Contains(plain, "x.go:10-25") {
		t.Errorf("expected 'x.go:10-25' in compact line; got:\n%s", plain)
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

// ansiLineHasCode scans each output line for an ANSI CSI sub-sequence
// (e.g. "38;5;255" or "48;5;22") that terminates on `;` or `m`, AND
// the supplied content substring. The terminator guard prevents
// "38;5;25" from accidentally matching a request for "38;5;2".
func ansiLineHasCode(rendered, ansiCode, contentSubstring string) bool {
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(line, "\x1b[") {
			continue
		}
		if !strings.Contains(line, contentSubstring) {
			continue
		}
		start := 0
		for {
			i := strings.Index(line[start:], ansiCode)
			if i < 0 {
				break
			}
			end := start + i + len(ansiCode)
			if end < len(line) && (line[end] == ';' || line[end] == 'm') {
				return true
			}
			start = end
			if start >= len(line) {
				break
			}
		}
	}
	return false
}

// renderDiffFromSnippet is a test helper that mirrors what
// cardsFindingPanel does inline: parse the snippet string then run
// renderDiff. Keeps the diff-rendering tests focused on the pure-
// function logic without spinning up a full Cards panel.
func renderDiffFromSnippet(snippet string, minWidth int) string {
	return renderDiff(parseSnippetToDiffLines(snippet), minWidth)
}

func TestRenderDiffStripBackgroundsByKind(t *testing.T) {
	// Per the secguard palette: '+' lines get cardAddBg (#111C1C),
	// '-' lines get cardDelBg (#22141A), context lines get no bg
	// (codeFg on default). Check via the truecolor escape fragments.
	got := renderDiffFromSnippet("- old line\n+ new line\n  context line", 40)

	addBg := hexToTrueColor("#111C1C")
	delBg := hexToTrueColor("#22141A")
	if !ansiLineHasCode(got, "48;2;"+addBg, "new line") {
		t.Errorf("'+' line should carry addBg (#111C1C → %s); got:\n%q", addBg, got)
	}
	if !ansiLineHasCode(got, "48;2;"+delBg, "old line") {
		t.Errorf("'-' line should carry delBg (#22141A → %s); got:\n%q", delBg, got)
	}
}

func TestRenderDiffContextLineHasNoBgStrip(t *testing.T) {
	// Context lines render with codeFg only — no background paint.
	// Catches a regression where the strip bg leaks into context rows.
	got := renderDiffFromSnippet("  context one\n  context two", 40)

	plain := stripANSI(got)
	for _, want := range []string{"context one", "context two"} {
		if !strings.Contains(plain, want) {
			t.Errorf("plain content missing %q; got:\n%s", want, plain)
		}
	}
	addBg := hexToTrueColor("#111C1C")
	delBg := hexToTrueColor("#22141A")
	if ansiLineHasCode(got, "48;2;"+addBg, "context one") {
		t.Errorf("context line must not paint addBg; got:\n%q", got)
	}
	if ansiLineHasCode(got, "48;2;"+delBg, "context two") {
		t.Errorf("context line must not paint delBg; got:\n%q", got)
	}
}

func TestRenderDiffPreservesLineCount(t *testing.T) {
	// Diff rendering must not drop or merge lines — the panel layout
	// relies on a stable row count for its height math.
	in := "- a\n+ b\n  c\n+ d"
	got := renderDiffFromSnippet(in, 40)
	// Input has 4 non-empty lines (no trailing newline); output joined
	// with "\n" → 3 newlines.
	wantNL := 3
	if have := strings.Count(stripANSI(got), "\n"); have != wantNL {
		t.Errorf("line count drift: want %d newlines, got %d\n%q", wantNL, have, got)
	}
}

func TestRenderDiffWrapsLongLinesWithSignAlignment(t *testing.T) {
	// A diff line longer than `width - signWidth` must wrap into
	// multiple rows where the *continuation* rows keep the sign
	// column aligned (blank pad of signWidth on the strip bg) so
	// the card body stays a clean rectangle. Without this, lipgloss
	// Width() wraps to column 0 and the strip visually breaks.
	longRemoved := strings.Repeat("x", 120) + " end"
	longAdded := strings.Repeat("y", 120) + " end"
	in := "- " + longRemoved + "\n+ " + longAdded
	got := renderDiffFromSnippet(in, 80)

	// 2 wrapped logical lines, each producing >= 2 visual rows.
	plainLines := strings.Split(stripANSI(got), "\n")
	if len(plainLines) < 4 {
		t.Fatalf("expected at least 4 visual rows after wrap, got %d:\n%s",
			len(plainLines), strings.Join(plainLines, "\n"))
	}

	// First wrapped row of `-` carries the sign; the next row must
	// start with the same column of blank pad (signWidth = 4 in our
	// renderer: " -  " / " +  "), NOT with the raw text at column 0.
	signWidth := 4
	pad := strings.Repeat(" ", signWidth)
	// Find the row that starts with " -  " then verify the next row
	// also begins with `signWidth` spaces (alignment).
	firstMinusIdx := -1
	for i, l := range plainLines {
		if strings.HasPrefix(l, " -  ") {
			firstMinusIdx = i
			break
		}
	}
	if firstMinusIdx < 0 {
		t.Fatalf("could not find ` -  ` prefixed first row:\n%s",
			strings.Join(plainLines, "\n"))
	}
	if firstMinusIdx+1 >= len(plainLines) {
		t.Fatalf("no continuation row after `-` wrap")
	}
	cont := plainLines[firstMinusIdx+1]
	if !strings.HasPrefix(cont, pad) {
		t.Errorf("continuation row of `-` lost sign-column alignment\n"+
			"want prefix %q (%d blanks), got %q",
			pad, signWidth, cont)
	}
	// Continuation must NOT itself begin with " -  " (else we'd be
	// re-emitting the sign and breaking the visual sign-once rule).
	if strings.HasPrefix(cont, " -  ") {
		t.Errorf("continuation row should not re-emit the sign, got %q", cont)
	}

	// Same alignment check on the `+` block.
	firstPlusIdx := -1
	for i, l := range plainLines {
		if strings.HasPrefix(l, " +  ") {
			firstPlusIdx = i
			break
		}
	}
	if firstPlusIdx < 0 {
		t.Fatalf("could not find ` +  ` prefixed first row:\n%s",
			strings.Join(plainLines, "\n"))
	}
	if firstPlusIdx+1 >= len(plainLines) {
		t.Fatalf("no continuation row after `+` wrap")
	}
	contPlus := plainLines[firstPlusIdx+1]
	if !strings.HasPrefix(contPlus, pad) {
		t.Errorf("continuation row of `+` lost sign-column alignment\n"+
			"want prefix %q (%d blanks), got %q",
			pad, signWidth, contPlus)
	}

	// And the strip background must extend onto continuation rows so
	// the wrap doesn't leave half the card "blank" at terminal bg.
	addBg := hexToTrueColor("#111C1C")
	delBg := hexToTrueColor("#22141A")
	// Continuation text from the `-` wrap is the tail of longRemoved
	// (a stretch of 'x' chars near the end).
	if !ansiLineHasCode(got, "48;2;"+delBg, "xxxx end") {
		t.Errorf("continuation row of `-` lost delBg strip;\n%q", got)
	}
	if !ansiLineHasCode(got, "48;2;"+addBg, "yyyy end") {
		t.Errorf("continuation row of `+` lost addBg strip;\n%q", got)
	}
}

func TestParseSnippetToDiffLinesStripsPrefix(t *testing.T) {
	// The parser strips the "- "/"+ "/"  " diff prefix so the rendered
	// text doesn't double the sign char. Regression guard against
	// reintroducing the prefix into the body.
	in := "- removed\n+ added\n  context"
	got := parseSnippetToDiffLines(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 lines, got %d: %+v", len(got), got)
	}
	cases := []struct {
		i        int
		wantKind byte
		wantText string
	}{
		{0, '-', "removed"},
		{1, '+', "added"},
		{2, ' ', "context"},
	}
	for _, c := range cases {
		if got[c.i].kind != c.wantKind {
			t.Errorf("[%d] kind = %q, want %q", c.i, got[c.i].kind, c.wantKind)
		}
		if got[c.i].text != c.wantText {
			t.Errorf("[%d] text = %q, want %q", c.i, got[c.i].text, c.wantText)
		}
	}
}

func TestCardsSnippetOmitsCodeFences(t *testing.T) {
	// The triple-backtick code fence (```language ... ```) must NOT
	// leak into rendered card output. We deliberately drop it: the
	// diff-coloured strips already mark the region as code, and a
	// fence rendered as literal text reads as random noise on screen.
	// Regression guard against re-introducing the wrapper.
	p := Payload{
		Findings: []Finding{{
			Severity: SeverityHigh, File: "src/scripts/docs-search.ts", Line: 91,
			Title:       "HTML injection vulnerability",
			Description: "snippet should appear without fence wrapping",
			Language:    "typescript",
			Snippet:     "- href=\"/docs/${escapeHtml(e.id)}\"",
		}},
		Meta: samplePayload().Meta,
	}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	plain := stripANSI(w.String())
	if strings.Contains(plain, "```") {
		t.Errorf("code fence (```) leaked into card output; got:\n%s", plain)
	}
	// Snippet content itself must still appear.
	if !strings.Contains(plain, `escapeHtml(e.id)`) {
		t.Errorf("snippet content missing from rendered card; got:\n%s", plain)
	}
}

func TestCardsSnippetIntegratedWithPanel(t *testing.T) {
	// End-to-end sanity: render a full Cards panel with a snippet and
	// confirm each diff line lands on its strip with the secguard
	// palette bg color. Catches composition regressions between the
	// diff renderer and the surrounding panel.
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
	addBg := hexToTrueColor("#111C1C")
	delBg := hexToTrueColor("#22141A")
	if !ansiLineHasCode(raw, "48;2;"+addBg, "new") {
		t.Errorf("integrated panel: '+' line should carry addBg; got:\n%q", raw)
	}
	if !ansiLineHasCode(raw, "48;2;"+delBg, "old") {
		t.Errorf("integrated panel: '-' line should carry delBg; got:\n%q", raw)
	}
}

// lineWithSubstring returns the rendered line containing the given
// substring, or "" if none. Lets contrast tests inspect just the row
// the assertion cares about without false-matching incidental escapes
// elsewhere in the card.
func lineWithSubstring(rendered, substring string) string {
	for _, line := range strings.Split(rendered, "\n") {
		if strings.Contains(line, substring) {
			return line
		}
	}
	return ""
}

func TestCardsPanelTitleAndDescAreStyled(t *testing.T) {
	// Title gets bold + a fg color; description gets a (muted) fg color.
	// We check for the styling *form* — `\x1b[1;38;2;` for title bold-fg,
	// `\x1b[38;2;` for desc fg — rather than exact RGB triples, because
	// lipgloss/termenv can quantise hex inputs by a bit or two.
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

	titleLine := lineWithSubstring(raw, "title-text")
	if titleLine == "" {
		t.Fatalf("title-text not found in output:\n%s", raw)
	}
	if !strings.Contains(titleLine, "\x1b[1;38;2;") {
		t.Errorf("title line should be bold + fg-colored (escape \\x1b[1;38;2;...); got:\n%q", titleLine)
	}

	descLine := lineWithSubstring(raw, "description-text")
	if descLine == "" {
		t.Fatalf("description-text not found in output:\n%s", raw)
	}
	if !strings.Contains(descLine, "\x1b[38;2;") {
		t.Errorf("description line should have fg color escape; got:\n%q", descLine)
	}
}

func TestCardsEmptyPanelMessageIsStyled(t *testing.T) {
	// Empty case: "✓ No findings. Looks good." must be bold + colored
	// against the info-theme panel bg.
	p := samplePayload()
	p.Findings = []Finding{}
	var w bytes.Buffer
	if err := Cards(&w, p); err != nil {
		t.Fatal(err)
	}
	raw := w.String()
	line := lineWithSubstring(raw, "No findings")
	if line == "" {
		t.Fatalf("empty-panel message not found in output:\n%s", raw)
	}
	if !strings.Contains(line, "\x1b[1;38;2;") {
		t.Errorf("empty message should be bold + fg-colored; got:\n%q", line)
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
	// Compact mode reuses the severity theme's label so the icon glyph
	// matches the full-panel layout. Separator is " · " (muted middle
	// dot) consistent with the panel chip-to-path spacing.
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

	want := "💥 CRITICAL · internal/auth/session.go:142 — SQL fragment built from request input"
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

func TestMarkdownTemplateUsesPathRefMethod(t *testing.T) {
	// Templates can call the PathRef method directly via Go's
	// text/template method-resolution; this surfaces the same range
	// formatting that cards / copytext use without forcing each
	// custom OUTPUT.md to reproduce the if/printf logic.
	p := findingsPayload()
	// Augment one of the findings with a range so we can assert on it.
	p.Findings[0].LineEnd = 145
	p.OutputTemplate = `{{ range .Findings }}- {{ .PathRef }}
{{ end }}`
	var w bytes.Buffer
	if err := Markdown(&w, p); err != nil {
		t.Fatal(err)
	}
	out := w.String()
	if !strings.Contains(out, "- internal/auth/session.go:142-145") {
		t.Errorf("expected PathRef to emit 'file:142-145'; got:\n%s", out)
	}
	if !strings.Contains(out, "- internal/db/migrate.go:73\n") {
		t.Errorf("expected bare ':73' for single-line finding; got:\n%s", out)
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
