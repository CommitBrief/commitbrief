// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package upgrade

import "os"

// replaceBinary swaps target with the prepared file at tmp. On Unix a
// rename is a single atomic operation and is legal while the target is
// executing: the running process keeps its own inode, so the swap is
// invisible to it. On failure nothing has changed, so there is no
// rollback to perform.
func replaceBinary(tmp, target string) error {
	return os.Rename(tmp, target)
}

// CleanupStale is a no-op on Unix — the rename leaves nothing behind.
func CleanupStale(target string) {}
