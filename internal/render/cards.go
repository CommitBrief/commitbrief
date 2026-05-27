package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/CommitBrief/commitbrief/internal/version"
)

// Cards is the rich terminal renderer that frames the review with a styled
// header, a pre-body status line, one bordered panel per finding (severity-
// colored), and a one-line summary footer. It is the default Format when
// stdout is a TTY and neither --markdown nor --json is set.
//
// Under ADR-0014 the LLM emits a JSON document parsed into Payload.Findings
// before render. If Findings is nil (graceful degrade — parse failed) the
// renderer falls back to a Stage A glamour-rendered body of Payload.Content
// so the user still sees something rather than a stack trace. Color
// resolution is delegated to lipgloss; NO_COLOR=1 and non-TTY stdout both
// downgrade automatically.
func Cards(w io.Writer, p Payload) error {
	body, err := cardsBody(p)
	if err != nil {
		return err
	}

	parts := []string{cardsHeader(p.Meta)}
	if status := cardsStatus(p.Meta); status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, "", body, "", cardsFooter(p.Meta, p.Findings))

	out := strings.Join(parts, "\n") + "\n"
	if _, err := io.WriteString(w, out); err != nil {
		return fmt.Errorf("render: write: %w", err)
	}
	return nil
}

func cardsBody(p Payload) (string, error) {
	// Graceful degrade: no parsed findings → render raw Content via glamour.
	if p.Findings == nil {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(terminalWordWrap),
		)
		if err != nil {
			return "", fmt.Errorf("render: glamour init: %w", err)
		}
		body, err := r.Render(p.Content)
		if err != nil {
			return "", fmt.Errorf("render: glamour render: %w", err)
		}
		return strings.TrimRight(body, "\n"), nil
	}

	// Clean review with no findings — single short success panel in the
	// normal layout, or a one-liner in compact mode.
	if len(p.Findings) == 0 {
		if p.Compact {
			return styleOK.Render("✓ ") + styleMuted.Render("No findings. Looks good."), nil
		}
		return cardsEmptyPanel(), nil
	}

	// Compact mode: one line per finding, severity-ordered. No panel
	// borders — density is the whole point.
	if p.Compact {
		return cardsCompactBody(p.Findings), nil
	}

	// Per-finding panels, ordered critical → info.
	var sb strings.Builder
	first := true
	for _, group := range GroupBySeverity(p.Findings) {
		for _, f := range group.Items {
			if !first {
				sb.WriteString("\n\n")
			}
			sb.WriteString(cardsFindingPanel(f))
			first = false
		}
	}
	return sb.String(), nil
}

// cardsCompactBody renders findings as severity-grouped one-liners.
// Critical → info ordering matches the panel layout so a user toggling
// `--compact` doesn't see findings re-shuffle.
func cardsCompactBody(findings []Finding) string {
	var sb strings.Builder
	first := true
	for _, group := range GroupBySeverity(findings) {
		for _, f := range group.Items {
			if !first {
				sb.WriteString("\n")
			}
			sb.WriteString(cardsCompactLine(f))
			first = false
		}
	}
	return sb.String()
}

// cardsCompactLine formats one finding as a single line. Uses the
// severity theme's label + accent color so compact mode reads as a
// flat version of the full panel (same icon glyph, same color cue).
// Description and snippet are intentionally omitted — that's the
// trade for the density.
//
//	💥 CRITICAL · internal/auth/session.go:142 — SQL fragment built from request input
func cardsCompactLine(f Finding) string {
	theme, ok := severityThemes[f.Severity]
	if !ok {
		theme = severityThemes[SeverityInfo]
	}
	badge := lipgloss.NewStyle().Foreground(theme.accent).Bold(true).Render(theme.label)
	sep := lipgloss.NewStyle().Foreground(cardMuted).Render(" · ")
	location := lipgloss.NewStyle().Foreground(cardMuted).Render(fmt.Sprintf("%s:%d", f.File, f.Line))
	dash := lipgloss.NewStyle().Foreground(cardMuted).Render(" — ")
	return badge + sep + location + dash + f.Title
}

var (
	stylePrefix = lipgloss.NewStyle().Foreground(lipgloss.Color("241")) // dim grey
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // soft cyan
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green
	styleMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // medium grey
	styleBullet = lipgloss.NewStyle().Foreground(lipgloss.Color("238")) // very dim
)

// severityTheme bundles the four colors + label that uniquely identify
// a severity in the card layout. Sourced verbatim from the maintainer's
// secguard prototype; hex values, label strings, and prefix icons are
// load-bearing — change them and the entire visual identity shifts.
type severityTheme struct {
	panelBg lipgloss.Color // panel arka planı (cards body bg)
	border  lipgloss.Color // ╭ ╮ ╰ ╯ ─ │ characters' foreground
	accent  lipgloss.Color // severity chip metin rengi
	label   string         // severity chip metni (icon + uppercase name)
}

var severityThemes = map[Severity]severityTheme{
	SeverityCritical: {panelBg: "#1A1116", border: "#602B38", accent: "#ff6b8a", label: "💥 CRITICAL"},
	SeverityHigh:     {panelBg: "#1A1511", border: "#603F2B", accent: "#ffa86b", label: "🚨 HIGH"},
	SeverityMedium:   {panelBg: "#1A1A11", border: "#5A5A2B", accent: "#f0d050", label: "⚡ MEDIUM"},
	SeverityLow:      {panelBg: "#11161A", border: "#2B4760", accent: "#6bb8ff", label: "📌 LOW"},
	SeverityInfo:     {panelBg: "#11181A", border: "#2B5560", accent: "#6be0e0", label: "💡 INFO"},
}

// Shared palette colors used across all severity themes. Same source
// as severityThemes — hex codes verbatim from the secguard reference.
var (
	cardMuted  = lipgloss.Color("#9CA3AF") // file path, description, low-emphasis text
	cardWhite  = lipgloss.Color("#F3F4F6") // title (high-contrast on dark panels)
	cardDelBg  = lipgloss.Color("#22141A") // removed-line strip background
	cardDelFg  = lipgloss.Color("#ff6b8a") // removed-line text
	cardAddBg  = lipgloss.Color("#111C1C") // added-line strip background
	cardAddFg  = lipgloss.Color("#22d3a0") // added-line text
	cardSignFg = lipgloss.Color("#3a3f4f") // " - "/" + " sign chars (faint, on diff strip bg)
	cardCodeFg = lipgloss.Color("#E5E7EB") // context line text (no strip, default bg)
)

// clearEOL resets ANSI state and erases to end of line. Appended after
// every panel row so colored backgrounds don't bleed past the card's
// right border when the terminal line is wider than the rendered card.
const clearEOL = "\x1b[0m\x1b[49m\x1b[K"

// cardContentWidth is the fixed inner content width of a finding panel.
// Outer card = cardContentWidth + 4 (2 borders + 1 space padding each
// side) = 100 columns, matching typical terminal width. Title and
// description wrap to this width via lipgloss `Width()`; long diff
// lines wrap inside their colored strip. Without a cap, real LLM
// descriptions (often 200+ chars without line breaks) would balloon
// the card past the terminal width and the right border would fall
// off-screen.
const cardContentWidth = 96

// cardsHeader: "commitbrief vX.Y.Z · provider: name/model · cache: hit"
// Each segment is colored independently; bullets stay quiet.
func cardsHeader(m Meta) string {
	bullet := styleBullet.Render(" · ")
	v := version.Version
	if v != "" && v != "dev" && !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	tool := stylePrefix.Render("commitbrief ") + styleAccent.Render(v)

	providerStr := m.Provider
	if m.Model != "" {
		providerStr = m.Provider + "/" + m.Model
	}
	prov := stylePrefix.Render("provider: ") + styleAccent.Render(providerStr)

	cacheStr := "miss"
	if m.Cached {
		cacheStr = "hit"
	}
	cache := stylePrefix.Render("cache: ") + styleAccent.Render(cacheStr)

	return tool + bullet + prov + bullet + cache
}

// cardsStatus: "analyzing N files · X added · Y removed · COMMITBRIEF.md loaded"
// Returns "" when no stats are populated.
func cardsStatus(m Meta) string {
	if m.Files == 0 && m.LinesAdded == 0 && m.LinesRemoved == 0 && !m.RulesLoaded {
		return ""
	}
	bullet := styleBullet.Render(" · ")
	parts := []string{
		styleMuted.Render(fmt.Sprintf("analyzing %d %s", m.Files, plural(m.Files, "file"))),
		styleMuted.Render(fmt.Sprintf("%d added", m.LinesAdded)),
		styleMuted.Render(fmt.Sprintf("%d removed", m.LinesRemoved)),
	}
	if m.RulesLoaded {
		parts = append(parts, styleMuted.Render("COMMITBRIEF.md loaded"))
	}
	return strings.Join(parts, bullet)
}

// cardsFooter: "✓ Done in Ns · N findings · T tokens · $cost" (or Saved on
// cache hit). When findings is non-nil the count is included; on degrade
// the count is omitted (we don't know how many "findings" the raw body
// contains).
func cardsFooter(m Meta, findings []Finding) string {
	bullet := styleBullet.Render(" · ")
	check := styleOK.Render("✓ ")
	timing := stylePrefix.Render("Done in ") + styleAccent.Render(formatDuration(m.Latency))

	totalTokens := m.Usage.InputTokens + m.Usage.OutputTokens
	tokens := styleMuted.Render(fmt.Sprintf("%d tokens", totalTokens))

	moneyLabel := "Cost: "
	if m.Cached {
		moneyLabel = "Saved: "
	}
	money := stylePrefix.Render(moneyLabel) + styleAccent.Render(fmt.Sprintf("$%.4f", m.Cost))

	segments := []string{check + timing}
	if findings != nil {
		n := len(findings)
		segments = append(segments, styleMuted.Render(fmt.Sprintf("%d %s", n, plural(n, "finding"))))
	}
	segments = append(segments, tokens, money)
	return strings.Join(segments, bullet)
}

// diffLine is one row of a code excerpt to be diff-rendered. kind is
// '-' for removed, '+' for added, ' ' for context.
type diffLine struct {
	kind byte
	text string
}

// parseSnippetToDiffLines turns the LLM-emitted snippet string (per
// ADR-0014 §1: lines prefixed with "-", "+", or "  ") into structured
// diffLine values. Empty lines are skipped — they have no diff
// semantics and rendering them as a blank "context" row collapses
// height predictably anyway.
func parseSnippetToDiffLines(snippet string) []diffLine {
	if snippet == "" {
		return nil
	}
	var out []diffLine
	for _, line := range strings.Split(snippet, "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case '+', '-':
			out = append(out, diffLine{kind: line[0], text: strings.TrimPrefix(line[1:], " ")})
		default:
			// Unified-diff context lines use "  text" (sign-space sign-
			// space). Strip up to two leading spaces so the rendered
			// body lines up with the +/- variants, then let any deeper
			// indentation in the source code pass through verbatim.
			text := line
			switch {
			case strings.HasPrefix(text, "  "):
				text = text[2:]
			case strings.HasPrefix(text, " "):
				text = text[1:]
			}
			out = append(out, diffLine{kind: ' ', text: text})
		}
	}
	return out
}

// cardsFindingPanel renders one Finding as a self-contained card using
// the secguard palette + layout. Ported verbatim from the maintainer's
// reference design — hex codes, label strings, and the contentWidth+24
// sizing heuristic are load-bearing visual identity, do not retune
// without explicit intent.
//
// Layout (vertical):
//
//	╭────────────────────────────╮
//	│                            │
//	│ 💥 CRITICAL  · path:line    │
//	│                            │
//	│ Title (white, bold)        │
//	│ Description (muted)        │
//	│                            │
//	│  -  removed line           │  (red strip)
//	│  +  added line             │  (green strip)
//	│  -  context line           │  (no strip)
//	│                            │
//	╰────────────────────────────╯
func cardsFindingPanel(f Finding) string {
	theme, ok := severityThemes[f.Severity]
	if !ok {
		theme = severityThemes[SeverityInfo]
	}

	chip := lipgloss.NewStyle().
		Foreground(theme.accent).
		Background(theme.panelBg).
		Bold(true).
		Render(theme.label)

	pathStr := fmt.Sprintf("%s:%d", f.File, f.Line)
	path := lipgloss.NewStyle().
		Foreground(cardMuted).
		Background(theme.panelBg).
		Render("· " + pathStr)

	gap := lipgloss.NewStyle().Background(theme.panelBg).Render("  ")
	header := lipgloss.JoinHorizontal(lipgloss.Top, chip, gap, path)

	// Title and description wrap to cardContentWidth via lipgloss
	// `Width()` — long inputs (LLM descriptions can easily run 200+
	// chars without line breaks) get broken into multiple panel-bg
	// rows instead of expanding the card past the terminal edge.
	title := lipgloss.NewStyle().
		Foreground(cardWhite).
		Background(theme.panelBg).
		Bold(true).
		Width(cardContentWidth).
		Render(f.Title)

	desc := lipgloss.NewStyle().
		Foreground(cardMuted).
		Background(theme.panelBg).
		Width(cardContentWidth).
		Render(f.Description)

	diff := parseSnippetToDiffLines(f.Snippet)

	// Header is JoinHorizontal'd (no Width on it yet); pad to the
	// fixed content width so trailing space gets the panel bg.
	fill := lipgloss.NewStyle().Background(theme.panelBg).Width(cardContentWidth)
	header = fill.Render(header)
	blank := fill.Render("")

	parts := []string{blank, header, blank, title, desc, blank}
	if len(diff) > 0 {
		parts = append(parts, renderDiff(diff, cardContentWidth), blank)
	}
	body := lipgloss.JoinVertical(lipgloss.Left, parts...)

	borderStyle := lipgloss.NewStyle().Foreground(theme.border).Background(theme.panelBg)
	padStyle := lipgloss.NewStyle().Background(theme.panelBg)

	dashes := borderStyle.Render(strings.Repeat("─", cardContentWidth+2))
	top := borderStyle.Render("╭") + dashes + borderStyle.Render("╮")
	bot := borderStyle.Render("╰") + dashes + borderStyle.Render("╯")

	left := borderStyle.Render("│") + padStyle.Render(" ")
	right := padStyle.Render(" ") + borderStyle.Render("│")

	var rows []string
	rows = append(rows, top)
	for _, line := range strings.Split(body, "\n") {
		rows = append(rows, left+line+right)
	}
	rows = append(rows, bot)
	return strings.Join(rows, clearEOL+"\n") + clearEOL
}

// renderDiff turns the parsed diff lines into a styled multi-line
// string. Added/removed lines get a fixed-width colored strip (sign
// char in a faint signFg, body in fg+bg pair); context lines pass
// through with codeFg on default bg.
//
// Lines longer than `width - signWidth` are wrapped *manually* into
// multiple rows. Each row keeps its sign column aligned: the first
// row carries the actual " - "/" + " sign in cardSignFg, while
// continuation rows get a blank-bg pad of equal width. Naive
// lipgloss `Width()` wrapping would leave the continuation row
// hugging column 0 with no bg fill in the sign column — visually
// broken in a multi-row diff.
func renderDiff(lines []diffLine, width int) string {
	var out []string
	for _, l := range lines {
		out = append(out, renderDiffRow(l.kind, l.text, width))
	}
	return strings.Join(out, "\n")
}

// renderDiffRow handles one logical diff line, returning one or more
// rendered rows joined by "\n". Long source text is chunked into
// width-sized pieces so the wrap break never lands inside an ANSI
// escape sequence, and so we control the per-row prefix exactly.
func renderDiffRow(kind byte, text string, width int) string {
	sign := fmt.Sprintf(" %c  ", kind)
	signWidth := lipgloss.Width(sign)
	bodyWidth := width - signWidth
	if bodyWidth <= 0 {
		bodyWidth = width
	}
	chunks := chunkRunes(text, bodyWidth)

	switch kind {
	case '-', '+':
		var bg, fg lipgloss.Color
		if kind == '-' {
			bg, fg = cardDelBg, cardDelFg
		} else {
			bg, fg = cardAddBg, cardAddFg
		}
		signStyle := lipgloss.NewStyle().Foreground(cardSignFg).Background(bg)
		signPad := lipgloss.NewStyle().Background(bg).Render(strings.Repeat(" ", signWidth))
		textStyle := lipgloss.NewStyle().Foreground(fg).Background(bg).Width(bodyWidth)

		rendered := make([]string, 0, len(chunks))
		for i, c := range chunks {
			prefix := signPad
			if i == 0 {
				prefix = signStyle.Render(sign)
			}
			rendered = append(rendered, prefix+textStyle.Render(c))
		}
		return strings.Join(rendered, "\n")

	default:
		// Context: codeFg on default bg, no strip. The sign column on
		// continuation rows is plain spaces — context wrap is rare
		// since LLM-emitted snippets keep context lines short.
		pad := strings.Repeat(" ", signWidth)
		style := lipgloss.NewStyle().Foreground(cardCodeFg).Width(width)
		rendered := make([]string, 0, len(chunks))
		for i, c := range chunks {
			if i == 0 {
				rendered = append(rendered, style.Render(sign+c))
			} else {
				rendered = append(rendered, style.Render(pad+c))
			}
		}
		return strings.Join(rendered, "\n")
	}
}

// chunkRunes splits a string into rune-aligned chunks of at most
// `width` visual columns each. Char-based (no word boundary
// awareness) — for code review wrapping, breaking mid-token is
// acceptable since wrap is exceptional and the reader can scan the
// continuation. width <= 0 returns the input as a single chunk to
// avoid an infinite loop.
func chunkRunes(s string, width int) []string {
	if width <= 0 || s == "" {
		return []string{s}
	}
	runes := []rune(s)
	if len(runes) <= width {
		return []string{s}
	}
	out := make([]string, 0, (len(runes)+width-1)/width)
	for len(runes) > width {
		out = append(out, string(runes[:width]))
		runes = runes[width:]
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}

// cardsEmptyPanel is the success view for a review that produced zero
// findings. Reuses the info-severity theme so the empty case visually
// matches the rest of the card family.
func cardsEmptyPanel() string {
	theme := severityThemes[SeverityInfo]
	msg := "✓ No findings. Looks good."
	body := lipgloss.NewStyle().
		Foreground(cardWhite).
		Background(theme.panelBg).
		Bold(true).
		Padding(1, 2).
		Render(msg)

	contentWidth := lipgloss.Width(body) - 2 // account for our manual side padding below

	borderStyle := lipgloss.NewStyle().Foreground(theme.border).Background(theme.panelBg)
	padStyle := lipgloss.NewStyle().Background(theme.panelBg)
	dashes := borderStyle.Render(strings.Repeat("─", contentWidth+2))
	top := borderStyle.Render("╭") + dashes + borderStyle.Render("╮")
	bot := borderStyle.Render("╰") + dashes + borderStyle.Render("╯")
	left := borderStyle.Render("│") + padStyle.Render(" ")
	right := padStyle.Render(" ") + borderStyle.Render("│")

	var rows []string
	rows = append(rows, top)
	for _, line := range strings.Split(body, "\n") {
		rows = append(rows, left+line+right)
	}
	rows = append(rows, bot)
	return strings.Join(rows, clearEOL+"\n") + clearEOL
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
