// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/ui"
)

// newCacheCmd is the `commitbrief cache` subtree. Currently exposes a
// single `clear` child that deletes the repo-local response cache at
// <repoRoot>/.commitbrief/cache/. The parent exists as a namespace so
// future inspection helpers (e.g. `cache stats`, `cache inspect`) can
// slot in without re-flattening the CLI surface.
func newCacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Inspect and manage the local response cache",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newCacheClearCmd())
	cmd.AddCommand(newCachePruneCmd())
	return cmd
}

func newCacheClearCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Remove cached LLM responses for this repo",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(true)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			cacheDir := filepath.Join(app.RepoRoot, ".commitbrief", "cache")
			files, totalBytes, err := scanCacheDir(cacheDir)
			if err != nil {
				return fmt.Errorf("cache clear: scan %s: %w", cacheDir, err)
			}

			if files == 0 {
				_, _ = fmt.Fprintln(w, app.Catalog.T("cache.clear.empty", cacheDir))
				return nil
			}

			// Surface what's about to be removed before any confirm —
			// users may have a large cache they didn't realize is there.
			_, _ = fmt.Fprintln(w, app.Catalog.T(
				"cache.clear.summary", files, formatBytes(totalBytes), cacheDir))

			if !global.yes {
				ok, err := ui.AskYesNo(
					os.Stdin,
					cmd.OutOrStderr(),
					app.Catalog.T("cache.clear.confirm"),
					ui.AskOptions{NonInteractive: !ui.IsStdinTTY(os.Stdin)},
				)
				if err != nil {
					return err
				}
				if !ok {
					_, _ = fmt.Fprintln(w, app.Catalog.T("cache.clear.aborted"))
					return nil
				}
			}

			if err := os.RemoveAll(cacheDir); err != nil {
				return fmt.Errorf("cache clear: remove %s: %w", cacheDir, err)
			}
			_, _ = fmt.Fprintln(w, app.Catalog.T(
				"cache.clear.success", files, formatBytes(totalBytes)))
			return nil
		},
	}
}

// scanCacheDir walks the cache directory and returns the number of
// regular files plus their summed byte size. A missing directory is
// not an error — it just means "nothing cached yet" and the caller
// short-circuits with the empty-state message.
func scanCacheDir(dir string) (count int, total int64, err error) {
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) && path == dir {
				return filepath.SkipAll
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		count++
		total += info.Size()
		return nil
	})
	if walkErr != nil {
		return 0, 0, walkErr
	}
	return count, total, nil
}

// formatBytes turns a raw byte count into a short human-readable
// string (KB/MB/GB with one decimal place). Bytes under 1024 stay as
// "N B" — keeping the units consistent with `du -h`-style output.
func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	suffixes := []string{"KB", "MB", "GB", "TB"}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), suffixes[exp])
}
