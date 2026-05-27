// SPDX-License-Identifier: GPL-3.0-or-later

package ui

import (
	"io"
	"os"

	"golang.org/x/term"
)

type ColorMode int

const (
	ColorAuto ColorMode = iota
	ColorAlways
	ColorNever
)

func ParseColorMode(s string) ColorMode {
	switch s {
	case "always":
		return ColorAlways
	case "never":
		return ColorNever
	default:
		return ColorAuto
	}
}

// ColorEnabled reports whether color/ANSI escapes should be emitted to w.
//
//   - ColorNever or NO_COLOR/COMMITBRIEF_NO_COLOR env set → false
//   - ColorAlways → true (caller's choice, even when piped)
//   - ColorAuto → true only if w is a TTY
func ColorEnabled(w io.Writer, mode ColorMode) bool {
	if mode == ColorNever {
		return false
	}
	if os.Getenv("NO_COLOR") != "" || os.Getenv("COMMITBRIEF_NO_COLOR") != "" {
		return false
	}
	if mode == ColorAlways {
		return true
	}
	return isTerminal(w)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

func isTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// EnableANSI applies platform-specific tweaks needed to render ANSI escapes
// (e.g. enabling VT processing on Windows 10+). No-op on POSIX.
func EnableANSI(w io.Writer) error {
	f, ok := w.(*os.File)
	if !ok {
		return nil
	}
	return enableANSI(f)
}
