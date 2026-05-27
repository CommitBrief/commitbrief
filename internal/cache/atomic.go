// SPDX-License-Identifier: GPL-3.0-or-later

package cache

import (
	"fmt"
	"os"
)

// writeAtomic writes data to path via a temp+rename dance so that a
// crash mid-write never produces a half-written file. The temp file is
// in the same directory as path so the rename is atomic on POSIX and
// best-effort atomic on Windows.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("cache: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cache: rename: %w", err)
	}
	return nil
}
