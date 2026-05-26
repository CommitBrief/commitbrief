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

// severityColors maps each severity level to its lipgloss color, per
// ADR-0014 §1. Keep these in sync with the rubric in
// internal/rules/prompt.go — they're the visual half of the contract.
var severityColors = map[Severity]lipgloss.Color{
	SeverityCritical: lipgloss.Color("196"), // red
	SeverityHigh:     lipgloss.Color("208"), // orange
	SeverityMedium:   lipgloss.Color("220"), // yellow
	SeverityLow:      lipgloss.Color("33"),  // soft blue
	SeverityInfo:     lipgloss.Color("244"), // dim grey
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

// cardsFindingPanel renders one Finding as a bordered panel. The border
// color comes from the severity. Layout inside the panel:
//
//	<SEVERITY>  file:line — title
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
	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(color).
		Padding(0, 1).
		Width(terminalWordWrap)

	badge := lipgloss.NewStyle().
		Foreground(color).
		Bold(true).
		Render(strings.ToUpper(string(f.Severity)))
	location := styleMuted.Render(fmt.Sprintf("%s:%d", f.File, f.Line))
	title := lipgloss.NewStyle().Bold(true).Render(f.Title)

	header := badge + "  " + location + "\n" + title

	body := f.Description
	if f.Snippet != "" {
		fence := "```"
		body += "\n\n" + fence + f.Language + "\n" + f.Snippet + "\n" + fence
	}

	return style.Render(header + "\n\n" + body)
}

// cardsEmptyPanel is the success view for a review that produced zero
// findings. Per ADR-0014 §3 it is a single short panel with a green
// checkmark and the canonical message kept in sync with the default
// OUTPUT.md template.
func cardsEmptyPanel() string {
	color := severityColors[SeverityInfo]
	style := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(color).
		Padding(0, 1)
	check := styleOK.Render("✓ ")
	return style.Render(check + "No findings. Looks good.")
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
