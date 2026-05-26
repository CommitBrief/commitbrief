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

	// Clean review with no findings — single short success panel.
	if len(p.Findings) == 0 {
		return cardsEmptyPanel(), nil
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
		Background(bg).
		Padding(0, 1).
		Width(terminalWordWrap)

	// All inline segments share the panel's tinted background so the
	// card reads as a single block; lipgloss otherwise leaks the default
	// terminal background between pre-rendered runs.
	onBg := lipgloss.NewStyle().Background(bg)

	badge := onBg.Foreground(color).Bold(true).Render(icon + " " + strings.ToUpper(string(f.Severity)))
	sep := onBg.Foreground(lipgloss.Color("244")).Render(" • ")
	location := onBg.Foreground(lipgloss.Color("244")).Render(fmt.Sprintf("%s:%d", f.File, f.Line))
	title := onBg.Bold(true).Render(f.Title)

	header := badge + sep + location + "\n" + title

	body := onBg.Render(f.Description)
	if f.Snippet != "" {
		fence := "```"
		body += "\n\n" + onBg.Render(fence+f.Language) + "\n" + colorizeSnippet(f.Snippet, onBg) + "\n" + onBg.Render(fence)
	}

	return panel.Render(header + "\n\n" + body)
}

// colorizeSnippet walks each line of the snippet and applies diff
// semantics: leading "+" → green, leading "-" → red, anything else stays
// muted. Composes onto the panel's tinted background so the card stays
// visually unified. ADR-0014 §1 lists `snippet` as optional, so input may
// be empty; in that case the caller handles fence emission and we never
// land here.
func colorizeSnippet(snippet string, onBg lipgloss.Style) string {
	lines := strings.Split(snippet, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		var style lipgloss.Style
		switch {
		case strings.HasPrefix(line, "+"):
			style = onBg.Foreground(lipgloss.Color("42")) // green (matches styleOK)
		case strings.HasPrefix(line, "-"):
			style = onBg.Foreground(lipgloss.Color("203")) // soft red — distinct from severity 196
		default:
			style = onBg.Foreground(lipgloss.Color("244")) // muted grey (context line)
		}
		out = append(out, style.Render(line))
	}
	return strings.Join(out, "\n")
}

// cardsEmptyPanel is the success view for a review that produced zero
// findings. Per ADR-0014 §3 it is a single short panel with a green
// checkmark and the canonical message kept in sync with the default
// OUTPUT.md template. Uses the same rounded look as finding panels.
func cardsEmptyPanel() string {
	color := severityColors[SeverityInfo]
	bg := severityBG[SeverityInfo]
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(color).
		Background(bg).
		Padding(0, 1)
	onBg := lipgloss.NewStyle().Background(bg)
	check := onBg.Foreground(lipgloss.Color("42")).Bold(true).Render("✓ ")
	msg := onBg.Render("No findings. Looks good.")
	return panel.Render(check + msg)
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
