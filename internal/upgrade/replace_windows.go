// SPDX-License-Identifier: GPL-3.0-or-later

//go:build windows

package upgrade

import (
	"fmt"
	"os"
)

// osRename indirects os.Rename so the rollback path — the one branch
// that decides whether a failed upgrade leaves a working binary behind —
// can be forced in a test. Production code never reassigns it.
var osRename = os.Rename

// replaceBinary swaps target with the prepared file at tmp. Windows
// refuses to overwrite a running .exe but does allow renaming it, so
// the live binary is moved aside first. If the second rename fails the
// original is put back.
//
// If that rollback ALSO fails, the target is left with no binary at
// all, so the returned error names both failures and points at the
// moved-aside copy — without that, a user in this state has a missing
// command and no clue that a recoverable backup is sitting next to it.
//
// Removing the moved-aside file fails while the process is still
// running; that is expected, and CleanupStale sweeps it on the next
// upgrade.
func replaceBinary(tmp, target string) error {
	old := target + ".old"
	_ = os.Remove(old)

	if err := osRename(target, old); err != nil {
		return err
	}
	if err := osRename(tmp, target); err != nil {
		if rollbackErr := osRename(old, target); rollbackErr != nil {
			return fmt.Errorf(
				"upgrade failed (%v) and the rollback also failed (%v); "+
					"your previous binary is still at %s — rename it back to %s to recover",
				err, rollbackErr, old, target)
		}
		return err
	}
	_ = os.Remove(old)
	return nil
}

// cleanupOld removes the moved-aside binary a previous upgrade could
// not delete because it was still executing. Best effort by design: a
// failure here is never worth interrupting an upgrade over. Temp-file
// sweeping is shared across platforms; see CleanupStale in cleanup.go.
func cleanupOld(target string) {
	_ = os.Remove(target + ".old")
}
