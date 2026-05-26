//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableANSI flips on ENABLE_VIRTUAL_TERMINAL_PROCESSING on Windows 10+
// so that the standard ANSI escape sequences we emit get interpreted by
// the console host rather than printed literally. Failure is non-fatal:
// older Windows hosts render escapes raw, which is ugly but not broken.
func enableANSI(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(handle, &mode); err != nil {
		return nil
	}
	return windows.SetConsoleMode(handle, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING)
}
