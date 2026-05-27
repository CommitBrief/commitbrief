// SPDX-License-Identifier: GPL-3.0-or-later

// Package logo renders a terminal version of the CommitBrief web logo.
//
// The web Logo component is a rounded-square mark (gradient from
// term-green to term-greenDim) with a dark ">" prompt-arrow inside,
// set next to the "commitbrief" wordmark. This package reproduces
// that mark as a 16x16 pixel grid drawn with upper-half-block
// characters and 24-bit ANSI colors, then prints the wordmark plus
// tagline + links alongside it.
//
// Print is called once from cli.Execute at the top of every CLI run
// (gated on a TTY stderr). The intentional surrounding whitespace —
// one blank line above, two blank lines below — is part of the
// visual rhythm and must not be trimmed by callers.
package logo

import (
	"fmt"
	"io"
	"strings"
)

type rgb struct{ r, g, b uint8 }

// Design tokens — mirror values from web/src/styles/global.css. Keep
// these in sync if the web brand palette ever shifts.
var (
	termGreen    = rgb{34, 211, 160}  // #22d3a0
	termGreenDim = rgb{26, 163, 124}  // #1aa37c
	ink950       = rgb{8, 9, 12}      // #08090c — arrow stroke
	ink50        = rgb{238, 240, 245} // #eef0f5 — wordmark
	ink300       = rgb{91, 98, 115}   // #5b6273 — muted tagline
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	defBg  = "\033[49m"
	upHalf = "▀"
	loHalf = "▄"
)

// pixels is the 16x16 source grid for the icon.
//
//	'.' = transparent (terminal default background)
//	'G' = green tile (gradient applied per row)
//	'D' = dark stroke (arrow body)
//
// The arrow path mirrors the favicon SVG (M22 18 L36 32 L22 46 on a
// 64 canvas), scaled to a 16-grid: tip at col ~9, vertical center at
// row 8.
var pixels = [16]string{
	"..GGGGGGGGGGGG..",
	".GGGGGGGGGGGGGG.",
	"GGGGGGGGGGGGGGGG",
	"GGGGGGGGGGGGGGGG",
	"GGGGGDDDGGGGGGGG",
	"GGGGGDDDDGGGGGGG",
	"GGGGGGDDDDGGGGGG",
	"GGGGGGGDDDDGGGGG",
	"GGGGGGGGDDDDGGGG",
	"GGGGGGGDDDDGGGGG",
	"GGGGGGDDDDGGGGGG",
	"GGGGGDDDDGGGGGGG",
	"GGGGGDDDGGGGGGGG",
	"GGGGGGGGGGGGGGGG",
	".GGGGGGGGGGGGGG.",
	"..GGGGGGGGGGGG..",
}

func fg(c rgb) string { return fmt.Sprintf("\033[38;2;%d;%d;%dm", c.r, c.g, c.b) }
func bg(c rgb) string { return fmt.Sprintf("\033[48;2;%d;%d;%dm", c.r, c.g, c.b) }

// link wraps label in an OSC 8 hyperlink escape so terminals that
// support it (iTerm2, WezTerm, Kitty, Ghostty, VS Code, Windows
// Terminal, modern GNOME Terminal, …) render it clickable.
// Unsupported terminals fall back to showing the label as plain text.
func link(url, label string) string {
	return "\033]8;;" + url + "\033\\" + label + "\033]8;;\033\\"
}

func lerp(a, b rgb, t float64) rgb {
	mix := func(x, y uint8) uint8 {
		return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5)
	}
	return rgb{mix(a.r, b.r), mix(a.g, b.g), mix(a.b, b.b)}
}

func pixelColor(p byte, row int) (rgb, bool) {
	switch p {
	case 'G':
		return lerp(termGreen, termGreenDim, float64(row)/15.0), true
	case 'D':
		return ink950, true
	default:
		return rgb{}, false
	}
}

// renderRow collapses two pixel rows into one terminal row using a
// half-block, so a 16-row source grid renders in 8 terminal rows.
func renderRow(rowTop, rowBot int) string {
	top, bot := pixels[rowTop], pixels[rowBot]
	var b strings.Builder
	for c := 0; c < 16; c++ {
		tc, tok := pixelColor(top[c], rowTop)
		bc, bok := pixelColor(bot[c], rowBot)
		switch {
		case !tok && !bok:
			b.WriteByte(' ')
		case tok && !bok:
			b.WriteString(fg(tc))
			b.WriteString(defBg)
			b.WriteString(upHalf)
			b.WriteString(reset)
		case !tok && bok:
			b.WriteString(fg(bc))
			b.WriteString(defBg)
			b.WriteString(loHalf)
			b.WriteString(reset)
		default:
			b.WriteString(fg(tc))
			b.WriteString(bg(bc))
			b.WriteString(upHalf)
			b.WriteString(reset)
		}
	}
	return b.String()
}

// Print renders the logo to w. The version string is substituted into
// the wordmark line — pass version.Version from the cli/version
// package so the displayed version always matches the running build
// ("v0.9.2", "dev", etc.). The leading newline and trailing pair of
// blank lines are deliberate spacing; callers should not trim.
func Print(w io.Writer, version string) {
	const pad = "  "

	lines := make([]string, 8)
	for i := 0; i < 8; i++ {
		lines[i] = pad + renderRow(i*2, i*2+1)
	}

	gap := "   "
	word := bold + fg(ink50) + "commitbrief" + reset + " " +
		fg(ink300) + version + " © GNU-GPL3.0" + reset
	tagline := fg(ink300) + "LLM-driven code review for git diffs" + reset

	sep := fg(ink300) + " · " + reset
	links := fg(ink50) + link("https://commitbrief.com", "Home") + reset + sep +
		fg(ink50) + link("https://commitbrief.com/docs", "Docs") + reset + sep +
		fg(ink50) + link("https://github.com/CommitBrief/commitbrief", "GitHub") + reset + sep +
		fg(ink50) + link("https://github.com/sponsors/muhammetsafak", "Donation") + reset + sep +
		fg(ink50) + link("https://www.muhammetsafak.com.tr", "Author") + reset

	lines[2] += gap + word
	lines[4] += gap + tagline
	lines[5] += gap + links

	_, _ = fmt.Fprint(w, "\n"+strings.Join(lines, "\n")+"\n\n\n")
}
