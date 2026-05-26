package render

import (
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"

	"github.com/CommitBrief/commitbrief/internal/version"
)

// Cards is the rich terminal renderer that frames the glamour-rendered
// markdown body with a styled header, a pre-body status line, and a
// summary footer. It is the default Format when stdout is a TTY and
// neither --markdown nor --json is set. Compared to Terminal it adds:
//
//   - "commitbrief vX · provider: name/model · cache: hit|miss" header
//   - "analyzing N files · X added · Y removed [· COMMITBRIEF.md loaded]"
//     status line (omitted when no stats were captured)
//   - "✓ Done in Ns · T tokens · $cost" footer (always)
//
// Lipgloss is responsible for color resolution: it consults the writer's
// terminal-color profile through lipgloss.DefaultRenderer(). NO_COLOR=1
// and non-TTY stdout both downgrade to no-color automatically; this
// renderer relies on that and applies no extra branch logic.
//
// Stage A (v0.6.0): single-pass framing of the existing markdown body.
// Stage B (later): structured Finding cards from a future JSON LLM
// contract.
func Cards(w io.Writer, p Payload) error {
	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(terminalWordWrap),
	)
	if err != nil {
		return fmt.Errorf("render: glamour init: %w", err)
	}
	body, err := r.Render(p.Content)
	if err != nil {
		return fmt.Errorf("render: glamour render: %w", err)
	}

	parts := []string{
		cardsHeader(p.Meta),
	}
	if status := cardsStatus(p.Meta); status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, "", strings.TrimRight(body, "\n"), "", cardsFooter(p.Meta))

	out := strings.Join(parts, "\n") + "\n"
	if _, err := io.WriteString(w, out); err != nil {
		return fmt.Errorf("render: write: %w", err)
	}
	return nil
}

var (
	stylePrefix = lipgloss.NewStyle().Foreground(lipgloss.Color("241")) // dim grey
	styleAccent = lipgloss.NewStyle().Foreground(lipgloss.Color("39"))  // soft cyan
	styleOK     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))  // green
	styleMuted  = lipgloss.NewStyle().Foreground(lipgloss.Color("244")) // medium grey
	styleBullet = lipgloss.NewStyle().Foreground(lipgloss.Color("238")) // very dim
)

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
// Returns "" when no stats are populated — Stage A only renders this for
// commands that bothered to fill them in (review, dry-run). Other paths
// see an unobtrusive layout.
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

// cardsFooter: "✓ Done in Ns · T tokens · $cost" (or Saved on cache hit).
func cardsFooter(m Meta) string {
	bullet := styleBullet.Render(" · ")
	check := styleOK.Render("✓ ")
	timing := stylePrefix.Render("Done in ") + styleAccent.Render(formatDuration(m.Latency))

	totalTokens := m.Usage.InputTokens + m.Usage.OutputTokens
	tokens := stylePrefix.Render("") + styleMuted.Render(fmt.Sprintf("%d tokens", totalTokens))

	moneyLabel := "Cost: "
	if m.Cached {
		moneyLabel = "Saved: "
	}
	money := stylePrefix.Render(moneyLabel) + styleAccent.Render(fmt.Sprintf("$%.4f", m.Cost))

	return check + timing + bullet + tokens + bullet + money
}

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
