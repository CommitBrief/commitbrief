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

// cardsCompactLine formats one finding as a single line:
//
//	‼ CRITICAL • internal/auth/session.go:142 — SQL fragment built from request input
//
// Icon + badge share the severity color; the rest stays muted so the
// severity remains the eye's anchor. Description and snippet are
// intentionally omitted — that's the trade for the density.
func cardsCompactLine(f Finding) string {
	color, ok := severityColors[f.Severity]
	if !ok {
		color = severityColors[SeverityInfo]
	}
	icon := severityIcon[f.Severity]
	if icon == "" {
		icon = severityIcon[SeverityInfo]
	}
	badge := lipgloss.NewStyle().Foreground(color).Bold(true).Render(icon + " " + strings.ToUpper(string(f.Severity)))
	sep := styleMuted.Render(" • ")
	location := styleMuted.Render(fmt.Sprintf("%s:%d", f.File, f.Line))
	dash := styleMuted.Render(" — ")
	title := f.Title
	return badge + sep + location + dash + title
}

var (
	stylePrefix = lipgloss.NewStyle().Foreground(lipgloss.Color("241")) // dim grey
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // soft cyan
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green
	styleMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // medium grey
	styleBullet = lipgloss.NewStyle().Foreground(lipgloss.Color("238")) // very dim
)

// severityColors maps each severity level to its lipgloss border color,
// per ADR-0014 §1. Keep these in sync with the rubric in
// internal/rules/prompt.go — they're the visual half of the contract.
var severityColors = map[Severity]lipgloss.Color{
	SeverityCritical: lipgloss.Color("196"), // red
	SeverityHigh:     lipgloss.Color("208"), // orange
	SeverityMedium:   lipgloss.Color("220"), // yellow
	SeverityLow:      lipgloss.Color("33"),  // soft blue
	SeverityInfo:     lipgloss.Color("244"), // dim grey
}

// severityBG is a subtly-tinted shade of the border color used as the
// panel background. Adaptive so dark-terminal users see a darker tint
// and light-terminal users a paler one; either way the card reads as a
// distinct block without overpowering the surrounding terminal.
var severityBG = map[Severity]lipgloss.AdaptiveColor{
	SeverityCritical: {Dark: "52", Light: "224"},  // dark red / light pink
	SeverityHigh:     {Dark: "94", Light: "223"},  // brown / peach
	SeverityMedium:   {Dark: "100", Light: "229"}, // dark olive / cream
	SeverityLow:      {Dark: "17", Light: "153"},  // dark blue / light blue
	SeverityInfo:     {Dark: "237", Light: "252"}, // dark grey / light grey
}

// cardText is the high-contrast foreground used for panel body text
// (title, description, file:line, default context, snippet "context"
// rows that lack a diff prefix). Pure white on dark terminals, near-
// black on light. The severity badge keeps its own color so urgency
// is still the eye's anchor.
var cardText = lipgloss.AdaptiveColor{Dark: "255", Light: "232"}

// snippetAddedBG / snippetRemovedBG are the full-width strip colors
// used behind diff lines. Pairs with cardText foreground (white-ish on
// dark, near-black on light) for high contrast on both themes. Picked
// to be darker / less saturated than the severity-tinted panel bg so
// the strips read as "different from the surrounding panel" rather
// than "almost the same color as the panel".
var (
	snippetAddedBG   = lipgloss.AdaptiveColor{Dark: "22", Light: "151"} // green
	snippetRemovedBG = lipgloss.AdaptiveColor{Dark: "52", Light: "217"} // red
)

// panelHorizPadding and panelVertPadding control the breathing room
// between the rounded border and the panel contents. The inner content
// width — used to size full-width snippet strips so their backgrounds
// extend edge-to-edge of the content area — derives from these plus
// terminalWordWrap (the outer panel width including borders).
const (
	panelHorizPadding = 2
	panelVertPadding  = 1
	panelInnerWidth   = terminalWordWrap - 2 - panelHorizPadding*2 // -2 borders, -2*padding
)

// severityIcon prefixes the badge with a glyph that visually anchors
// the severity. Glyphs are text-variant Unicode (no emoji VS-16
// selector) so lipgloss foreground colors apply — emoji would lock to
// their built-in palette and ignore the severity color.
var severityIcon = map[Severity]string{
	SeverityCritical: "‼", // double exclamation
	SeverityHigh:     "⚠", // warning triangle
	SeverityMedium:   "▲", // up-pointing triangle
	SeverityLow:      "●", // bullet/circle
	SeverityInfo:     "ⓘ", // circled info
}

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

// cardsFindingPanel renders one Finding as a rounded-border panel. The
// border color and a subtly-tinted background both come from the
// severity, with an icon glyph anchoring the badge. Layout:
//
//	‼ CRITICAL • file:line
//	title
//
//	description
//
//	```language
//	snippet
//	```  (only if snippet non-empty)
func cardsFindingPanel(f Finding) string {
	color, ok := severityColors[f.Severity]
	if !ok {
		color = severityColors[SeverityInfo]
	}
	bg, ok := severityBG[f.Severity]
	if !ok {
		bg = severityBG[SeverityInfo]
	}
	icon := severityIcon[f.Severity]
	if icon == "" {
		icon = severityIcon[SeverityInfo]
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		BorderBackground(bg). // border characters' bg matches the panel so the rounded corners blend into the card instead of sitting on a terminal-default dark gap
		Background(bg).
		Padding(panelVertPadding, panelHorizPadding).
		Width(terminalWordWrap)

	// All inline segments share the panel's tinted background so the
	// card reads as a single block; lipgloss otherwise leaks the default
	// terminal background between pre-rendered runs.
	onBg := lipgloss.NewStyle().Background(bg)
	textOnBg := onBg.Foreground(cardText)

	badge := onBg.Foreground(color).Bold(true).Render(icon + " " + strings.ToUpper(string(f.Severity)))
	sep := textOnBg.Render(" • ")
	location := textOnBg.Render(fmt.Sprintf("%s:%d", f.File, f.Line))
	title := textOnBg.Bold(true).Render(f.Title)

	header := badge + sep + location + "\n" + title

	body := textOnBg.Render(f.Description)
	if f.Snippet != "" {
		fence := "```"
		body += "\n\n" + textOnBg.Render(fence+f.Language) + "\n" + colorizeSnippet(f.Snippet, onBg) + "\n" + textOnBg.Render(fence)
	}

	return panel.Render(header + "\n\n" + body)
}

// colorizeSnippet walks each line of the snippet and applies diff
// semantics:
//   - "+" lines  → full-width green strip (whole row painted green,
//     cardText foreground) so removals stand out edge-to-edge of the
//     content area, GitHub-style;
//   - "-" lines  → full-width red strip;
//   - other lines → cardText on the panel's tinted bg (panel-flush
//     "context" rows).
//
// The Width(panelInnerWidth) on the +/- styles is what makes the
// background extend past the actual text — without it the bg only
// covers the line's characters and the strip "ends" mid-row. The
// outer panel pads its content with panelHorizPadding spaces on each
// side, so the strips visually align with the content area's edges.
func colorizeSnippet(snippet string, onBg lipgloss.Style) string {
	addedStyle := lipgloss.NewStyle().
		Background(snippetAddedBG).
		Foreground(cardText).
		Width(panelInnerWidth)
	removedStyle := lipgloss.NewStyle().
		Background(snippetRemovedBG).
		Foreground(cardText).
		Width(panelInnerWidth)
	contextStyle := onBg.Foreground(cardText)

	lines := strings.Split(snippet, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+"):
			out = append(out, addedStyle.Render(line))
		case strings.HasPrefix(line, "-"):
			out = append(out, removedStyle.Render(line))
		default:
			out = append(out, contextStyle.Render(line))
		}
	}
	return strings.Join(out, "\n")
}

// cardsEmptyPanel is the success view for a review that produced zero
// findings. Per ADR-0014 §3 it is a single short panel with a green
// checkmark and the canonical message kept in sync with the default
// OUTPUT.md template. Uses the same rounded look + border-bg blend +
// breathing-room padding as finding panels for visual consistency.
func cardsEmptyPanel() string {
	color := severityColors[SeverityInfo]
	bg := severityBG[SeverityInfo]
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		BorderBackground(bg).
		Background(bg).
		Padding(panelVertPadding, panelHorizPadding)
	onBg := lipgloss.NewStyle().Background(bg)
	check := onBg.Foreground(lipgloss.Color("42")).Bold(true).Render("✓ ")
	msg := onBg.Foreground(cardText).Render("No findings. Looks good.")
	return panel.Render(check + msg)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
