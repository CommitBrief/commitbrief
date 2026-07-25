// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package upgrade

import "os"

// replaceBinary swaps target with the prepared file at tmp. Windows
// refuses to overwrite a running .exe but does allow renaming it, so
// the live binary is moved aside first. If the second rename fails the
// original is put back, leaving the installation exactly as it was.
//
// Removing the moved-aside file fails while the process is still
// running; that is expected, and CleanupStale sweeps it on the next
// upgrade.
func replaceBinary(tmp, target string) error {
	old := target + ".old"
	_ = os.Remove(old)

	if err := os.Rename(target, old); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(old, target) // rollback
		return err
	}
	_ = os.Remove(old)
	return nil
}

// CleanupStale removes the moved-aside binary a previous upgrade could
// not delete because it was still executing. Best effort by design: a
// failure here is never worth interrupting an upgrade over.
func CleanupStale(target string) {
	_ = os.Remove(target + ".old")
}
