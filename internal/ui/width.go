// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"io"
	"os"

	"golang.org/x/term"
)

// TerminalWidth reports the writer's terminal width in columns, or 0 when it
// cannot be determined — a non-file writer (a test buffer, a pipe), a
// redirected stream, or a platform that refuses the query.
//
// **0 means "unknown", not "zero columns".** Callers must treat it as "do not
// clip" rather than "clip everything away"; both the progress tree and the
// commit-graph renderer rely on that reading.
//
// The width is a snapshot: nothing here watches SIGWINCH, so a caller that
// holds the value across a resize will be working from a stale number. That is
// deliberate — every consumer renders in one pass, and re-querying per line
// would cost a syscall per row for no benefit.
func TerminalWidth(w io.Writer) int {
	f, ok := w.(*os.File)
	if !ok {
		return 0
	}
	cols, _, err := term.GetSize(int(f.Fd()))
	if err != nil {
		return 0
	}
	return cols
}

// Clip truncates s to at most max display columns (rune count, a good enough
// proxy here), appending "…" when it cuts. max <= 0 means "no limit", matching
// TerminalWidth's "0 = unknown" convention.
//
// Keeping a rendered line inside the terminal width is what prevents wrapping.
// For the progress tree a wrapped line desyncs the cursor-up redraw and floods
// the screen; for the commit graph it breaks the lane columns, since the
// continuation row carries no graph gutter.
func Clip(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}
