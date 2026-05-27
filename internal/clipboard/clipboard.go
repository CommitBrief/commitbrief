// SPDX-License-Identifier: GPL-3.0-or-later

// Package clipboard pushes a text payload onto the host's system
// clipboard. Two transports are tried in tandem:
//
//   - OSC 52 — an ANSI escape that asks the terminal emulator itself
//     to update the clipboard. Honored by iTerm2, kitty, WezTerm,
//     Alacritty, Ghostty, recent xterm, and tmux (with
//     `allow-passthrough`). Crucially, it works over SSH because the
//     escape travels back through the same TTY the user is sitting in.
//   - Native shellout — pbcopy / wl-copy / xclip / xsel / clip.exe.
//     Covers the terminals that don't honor OSC 52 (macOS Terminal.app,
//     Warp). Does NOT work over SSH; runs on the remote host.
//
// Code ported verbatim from the maintainer's secguard prototype
// (./secguard/main.go), with one refinement: OSC 52 is emitted to
// the supplied io.Writer (caller passes stderr or /dev/tty) instead
// of stdout, so it never leaks into piped output like `--json | jq`.
package clipboard

import (
	"encoding/base64"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
)

// EmitOSC52 writes an OSC 52 escape sequence to w that places payload
// on the system clipboard of the terminal emulator on the other end
// of w. Caller controls the destination: stderr keeps the escape out
// of redirected stdout; /dev/tty bypasses any redirection entirely.
func EmitOSC52(w io.Writer, payload string) error {
	enc := base64.StdEncoding.EncodeToString([]byte(payload))
	_, err := fmt.Fprintf(w, "\x1b]52;c;%s\x07", enc)
	return err
}

// NativeCopy shells out to the platform clipboard tool. Returns nil on
// success. The Linux branch tries wl-copy → xclip → xsel in order,
// matching what's typically installed on Wayland / X11 hosts.
func NativeCopy(payload string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "windows":
		cmd = exec.Command("clip.exe")
	case "linux":
		for _, args := range [][]string{
			{"wl-copy"},
			{"xclip", "-selection", "clipboard"},
			{"xsel", "--clipboard", "--input"},
		} {
			if _, err := exec.LookPath(args[0]); err == nil {
				cmd = exec.Command(args[0], args[1:]...)
				break
			}
		}
		if cmd == nil {
			return fmt.Errorf("no clipboard tool found (install wl-clipboard, xclip, or xsel)")
		}
	default:
		return fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	cmd.Stdin = strings.NewReader(payload)
	return cmd.Run()
}

// Result reports which transports succeeded for a single Copy call.
// OSC52 is true when the escape sequence was written to the supplied
// io.Writer without error (the terminal may still ignore it, but
// that's invisible to us). Native is true when the platform tool
// exited zero. Both can be true; at least one is expected on a
// supported environment.
type Result struct {
	OSC52  bool
	Native bool
}

// Copy emits the OSC 52 escape to w *and* invokes the native tool,
// returning a Result that records which transports succeeded. Caller
// decides what to do when both fail (e.g. headless Linux without
// wl-clipboard / xclip / xsel installed AND a terminal that ignores
// OSC 52).
func Copy(w io.Writer, payload string) Result {
	return Result{
		OSC52:  EmitOSC52(w, payload) == nil,
		Native: NativeCopy(payload) == nil,
	}
}

// MethodLabel summarizes which transports succeeded as a short string
// for user-facing hints. Returns "" when neither succeeded so callers
// can branch on the empty case.
func (r Result) MethodLabel() string {
	switch {
	case r.OSC52 && r.Native:
		return "OSC 52 + native"
	case r.OSC52:
		return "OSC 52"
	case r.Native:
		return "native"
	default:
		return ""
	}
}
