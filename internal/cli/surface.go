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
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s: %w", tmpName, err)
	}
	return nil
}
