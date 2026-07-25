// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"os"
	"path/filepath"
)

// CleanupStale removes scratch files a previous, interrupted upgrade
// could not clean up itself. A normal InstallManual run's defers remove
// its own ".commitbrief-dl-*" (downloaded archive) and
// ".commitbrief-bin-*" (extracted binary) temp files, but Ctrl-C kills
// the process before any defer runs, so an interrupted upgrade leaves
// one of them — up to ~12 MiB — sitting next to the target binary
// forever, until the next `upgrade` sweeps it here.
//
// A concurrent upgrade's in-flight temp file could in principle be
// swept out from under it by this same glob. That upgrade simply fails
// on its next read or write of the now-missing file; it never gets far
// enough to touch the installed binary, so the loser of that race fails
// safely rather than corrupting anything.
//
// Also runs cleanupOld, the platform-specific half of this sweep: the
// moved-aside ".old" binary a previous Windows upgrade could not
// delete while it was still executing (a no-op on Unix, where the
// rename leaves nothing behind).
func CleanupStale(target string) {
	dir := filepath.Dir(target)
	for _, pattern := range []string{".commitbrief-dl-*", ".commitbrief-bin-*"} {
		matches, _ := filepath.Glob(filepath.Join(dir, pattern))
		for _, m := range matches {
			_ = os.Remove(m)
		}
	}
	cleanupOld(target)
}
