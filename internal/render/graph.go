// SPDX-License-Identifier: GPL-3.0-or-later

package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/graph"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// Commit-graph rendering for `commitbrief map` (ADR-0037).
//
// internal/graph owns lane assignment; this file owns nothing but appearance —
// which rune goes in a cell, what is coloured, and how a row is clipped to the
// terminal. The split is what lets the topology be tested against hand-written
// DAGs with no terminal in sight.

// GraphOptions controls appearance. The zero value is a safe ASCII, no-colour,
// unclipped render — what a pipe or a test buffer should get.
type GraphOptions struct {
	// Color enables ANSI styling. Callers pass ui.ColorEnabled(w, mode).
	Color bool
	// Unicode selects box-drawing glyphs over the ASCII fallback. Callers
	// normally tie this to Color: a terminal that refused ANSI is also the
	// one most likely to mangle U+2502.
	Unicode bool
	// Width is the terminal width; 0 means unknown, so do not clip.
	Width int
	// Filtered marks that a commit filter was active, which is what makes the
	// matched/context distinction meaningful. Without it every commit renders
	// as matched and no legend is printed.
	Filtered bool
	// Now anchors relative dates. Zero means time.Now() — injectable so tests
	// are not clock-dependent.
	Now time.Time
}

// graphGlyphs is one rune set. Two exist: box-drawing for a capable terminal,
// ASCII for everything else.
type graphGlyphs struct {
	commit    string // this row's commit, matched
	context   string // this row's commit, filtered out
	vertical  string
	fork      string
	merge     string
	branchTee string
	branchEnd string
}

var (
	unicodeGlyphs = graphGlyphs{
		commit: "●", context: "○", vertical: "│", fork: "╲", merge: "╱",
		branchTee: "├─", branchEnd: "└─",
	}
	asciiGlyphs = graphGlyphs{
		commit: "*", context: "o", vertical: "|", fork: "\\", merge: "/",
		branchTee: "|-", branchEnd: "`-",
	}
)

// Graph colours. Deliberately reusing the palette already established by the
// cards renderer and the progress tree (DESIGN_SYSTEM.md) rather than
// introducing a third one.
var (
	graphLaneStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5b6273"))
	graphMatchedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#22d3a0"))
	graphContextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	graphHashStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e2b714"))
	graphRefStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#7aa2f7"))
	graphMetaStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CA3AF"))
	graphDimStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#5b6273"))
)

// GraphRow pairs a laid-out row with the commit metadata needed to describe it.
type GraphRow struct {
	Row    graph.Row
	Commit git.CommitMeta
	Refs   []string // branch/tag labels pointing at this commit
}

// Graph writes the commit DAG. Rows must already be laid out by graph.Layout
// and carry their commit metadata.
func Graph(w io.Writer, rows []GraphRow, opts GraphOptions) error {
	if len(rows) == 0 {
		return nil
	}
	g := asciiGlyphs
	if opts.Unicode {
		g = unicodeGlyphs
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	// The lane gutter is fixed-width across all rows so the text columns line
	// up; graph.Layout already padded every row to the same cell count.
	laneCells := rows[0].Row.Width()

	var sb strings.Builder
	for _, r := range rows {
		gutter := renderLaneGutter(r.Row, g, opts, laneCells)
		text := renderCommitText(r, opts, now)

		line := gutter + " " + text
		// Clip rather than wrap: a wrapped row would have no lane gutter on
		// its continuation line, which visually detaches it from the graph.
		sb.WriteString(ui.Clip(line, opts.Width))
		sb.WriteByte('\n')
	}

	if opts.Filtered {
		sb.WriteByte('\n')
		sb.WriteString(renderGraphLegend(g, opts))
		sb.WriteByte('\n')
	}
	_, err := io.WriteString(w, sb.String())
	if err != nil {
		return fmt.Errorf("render: write graph: %w", err)
	}
	return nil
}

// renderLaneGutter draws one row's lane columns.
func renderLaneGutter(row graph.Row, g graphGlyphs, opts GraphOptions, cells int) string {
	var sb strings.Builder
	for i := 0; i < cells; i++ {
		var glyph graph.Glyph
		if i < len(row.Cells) {
			glyph = row.Cells[i]
		}
		switch glyph {
		case graph.GlyphCommit:
			marker := g.commit
			style := graphMatchedStyle
			if !row.Matched {
				marker, style = g.context, graphContextStyle
			}
			sb.WriteString(paint(marker, style, opts.Color))
		case graph.GlyphVertical:
			sb.WriteString(paint(g.vertical, graphLaneStyle, opts.Color))
		case graph.GlyphFork:
			sb.WriteString(paint(g.fork, graphLaneStyle, opts.Color))
		case graph.GlyphMerge:
			sb.WriteString(paint(g.merge, graphLaneStyle, opts.Color))
		default:
			sb.WriteString(" ")
		}
	}
	return sb.String()
}

// renderCommitText draws everything right of the lane gutter: hash, ref
// labels, subject, author, relative date.
func renderCommitText(r GraphRow, opts GraphOptions, now time.Time) string {
	c := r.Commit
	parts := make([]string, 0, 5)
	parts = append(parts, paint(shortOrHash(c), graphHashStyle, opts.Color))

	if len(r.Refs) > 0 {
		parts = append(parts, paint("("+strings.Join(r.Refs, ", ")+")", graphRefStyle, opts.Color))
	}

	subject := c.Subject
	if subject == "" {
		subject = "(no subject)"
	}
	// A filtered-out commit is context, not the answer — dim it so the eye
	// lands on what the filter actually selected.
	if opts.Filtered && !r.Row.Matched {
		subject = paint(subject, graphDimStyle, opts.Color)
	}
	parts = append(parts, subject)

	meta := c.Author
	if !c.Date.IsZero() {
		if meta != "" {
			meta += "  "
		}
		meta += RelativeAge(c.Date, now)
	}
	if meta != "" {
		parts = append(parts, paint(meta, graphMetaStyle, opts.Color))
	}
	return strings.Join(parts, "  ")
}

func renderGraphLegend(g graphGlyphs, opts GraphOptions) string {
	return paint(g.commit, graphMatchedStyle, opts.Color) + " matches the filter   " +
		paint(g.context, graphContextStyle, opts.Color) + " context"
}

func shortOrHash(c git.CommitMeta) string {
	if c.Short != "" {
		return c.Short
	}
	if len(c.Hash) >= 7 {
		return c.Hash[:7]
	}
	return c.Hash
}

// BranchTopology writes the `--branches` view: one row per branch with its
// ahead/behind position relative to the base.
func BranchTopology(w io.Writer, branches []git.Branch, opts GraphOptions) error {
	if len(branches) == 0 {
		return nil
	}
	g := asciiGlyphs
	if opts.Unicode {
		g = unicodeGlyphs
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	// Align the counts column against the widest rendered name, connector
	// included, so the numbers form a readable column.
	nameWidth := 0
	for i, b := range branches {
		w := lipgloss.Width(branchLabel(b, g, i == len(branches)-1))
		if w > nameWidth {
			nameWidth = w
		}
	}

	var sb strings.Builder
	for i, b := range branches {
		label := branchLabel(b, g, i == len(branches)-1)
		pad := strings.Repeat(" ", nameWidth-lipgloss.Width(label))

		var counts string
		if b.IsBase {
			counts = paint("base", graphMatchedStyle, opts.Color)
		} else {
			counts = paint(fmt.Sprintf("%s%-4d", aheadMark(opts.Unicode), b.Ahead), graphMatchedStyle, opts.Color) +
				paint(fmt.Sprintf("%s%-4d", behindMark(opts.Unicode), b.Behind), graphContextStyle, opts.Color)
		}

		meta := b.Author
		if !b.Date.IsZero() {
			if meta != "" {
				meta += "  "
			}
			meta += RelativeAge(b.Date, now)
		}

		line := label + pad + "  " + counts + "  " + paint(meta, graphMetaStyle, opts.Color)
		sb.WriteString(ui.Clip(strings.TrimRight(line, " "), opts.Width))
		sb.WriteByte('\n')
	}
	if _, err := io.WriteString(w, sb.String()); err != nil {
		return fmt.Errorf("render: write branch topology: %w", err)
	}
	return nil
}

// branchLabel prefixes non-base branches with a tree connector so the base
// visually parents them.
func branchLabel(b git.Branch, g graphGlyphs, last bool) string {
	if b.IsBase {
		return b.Name
	}
	connector := g.branchTee
	if last {
		connector = g.branchEnd
	}
	return connector + " " + b.Name
}

func aheadMark(unicode bool) string {
	if unicode {
		return "▲"
	}
	return "+"
}

func behindMark(unicode bool) string {
	if unicode {
		return "▼"
	}
	return "-"
}

// paint applies a style only when colour is enabled, so the same code path
// produces clean text for a pipe, a file, or --color=never.
func paint(s string, style lipgloss.Style, color bool) string {
	if !color || s == "" {
		return s
	}
	return style.Render(s)
}

// RelativeAge renders a coarse "how long ago" label. Deliberately low
// resolution — the graph wants a scannable column, not a precise duration, and
// a fixed-width-ish token keeps rows aligned.
func RelativeAge(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < 0:
		// A commit dated in the future (skewed clock, rewritten history).
		// Reporting "now" is honest enough and avoids a negative duration.
		return "now"
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/(24*7)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}
