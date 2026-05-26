package compress

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// BackupTimestamp returns the ISO-8601-ish stamp used in backup filenames.
// Format: 2026-05-26T14-30-00Z. Colons are replaced with dashes so the
// path is portable to Windows (which forbids `:` in filenames).
func BackupTimestamp(now time.Time) string {
	return now.UTC().Format("2006-01-02T15-04-05Z")
}

// writeBackupAndApply atomically swaps in the compressed content after
// snapshotting the original to backupPath. Sequence:
//
//  1. mkdir -p backup dir (0700; may contain proprietary review rules).
//  2. Write original to backupPath (no temp+rename here — it's a fresh
//     file, write failure leaves nothing to clean up).
//  3. Write compressed to rulesPath.tmp, then rename to rulesPath.
//
// If any step fails after the backup is written, the backup is left in
// place so the user can recover manually.
func writeBackupAndApply(rulesPath, backupPath, original, compressed string) error {
	if err := os.MkdirAll(filepath.Dir(backupPath), 0o700); err != nil {
		return fmt.Errorf("compress: mkdir backup dir: %w", err)
	}
	if err := os.WriteFile(backupPath, []byte(original), 0o644); err != nil {
		return fmt.Errorf("compress: write backup %s: %w", backupPath, err)
	}

	tmp := rulesPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(compressed), 0o644); err != nil {
		return fmt.Errorf("compress: write temp %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, rulesPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("compress: rename %s → %s: %w", tmp, rulesPath, err)
	}
	return nil
}
