// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/meta"
)

// writeSurface renders the code-derived surface inventory for the live
// command tree and writes it to path.
//
// It lives in package cli rather than in meta because only this package can
// see both halves the inventory needs: the cobra tree, and the MCP review
// tool's input schema. meta takes them as arguments precisely so it never has
// to import cli, which imports meta.
//
// The write goes through a temp file in the destination directory and an
// atomic rename, so an interrupted run cannot leave a truncated artifact
// behind -- the file is committed to git and docs-check diffs against it, so
// a half-written one would read as drift.
func writeSurface(root *cobra.Command, path string) error {
	data, err := meta.Build(root, reviewToolInputSchema()).MarshalIndent()
	if err != nil {
		return err
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".surface-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// No os.Chmod here: os.CreateTemp already leaves tmpName at 0600, which
	// is strictly tighter than the 0644 a plain os.WriteFile would use for a
	// non-secret file (see G306's exclusion rationale in
	// scripts/security-scan.sh). Widening it back to 0644 before the rename
	// only trips gosec's G302 for no behavioral gain: the committed artifact
	// in git is tracked as 100644 regardless of the generating process's
	// working-tree mode, and a fresh clone gets the user's umask either way.
	// Leave it at 0600 rather than chmod-ing or adding another exclusion.
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s: %w", tmpName, err)
	}
	return nil
}
