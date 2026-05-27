// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

func newInitCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write COMMITBRIEF.md and a per-user OUTPUT.md template",
		Long: "Writes two files:\n" +
			"  - COMMITBRIEF.md at the repo root (team-shared review content)\n" +
			"  - .commitbrief/OUTPUT.md (per-user output format template; gitignored)\n" +
			"Both fall back to embedded defaults at runtime, so creating these files\n" +
			"is only necessary when you want to customize the prompt.\n" +
			"\n" +
			"If only one of the two files already exists, the other is still written\n" +
			"— init never aborts on first-found existing file. Pass --force (or --yes)\n" +
			"to overwrite the existing file(s) too.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveContext(true)
			if err != nil {
				return err
			}
			overwrite := force || global.yes

			rulesPath := filepath.Join(ctx.RepoRoot, rules.Filename)
			outputPath := filepath.Join(ctx.RepoRoot, rules.LocalSubdir, rules.OutputFilename)

			// UC-17: do NOT short-circuit on the first existing file. The
			// two artefacts are independent — a customised COMMITBRIEF.md
			// must not block a fresh OUTPUT.md scaffold (and vice versa).
			// Real I/O errors still bubble; "already exists" downgrades
			// to a per-file `init.skipped` info line.
			var firstErr error
			for _, t := range []struct {
				path string
				data []byte
			}{
				{rulesPath, []byte(rules.Default().Content)},
				{outputPath, []byte(rules.DefaultOutput().Content)},
			} {
				if err := writeOrSkip(ctx.Catalog, t.path, t.data, 0o644, overwrite); err != nil && firstErr == nil {
					firstErr = err
				}
			}
			return firstErr
		},
	}
	// UC-28: docs/02-commands.md has promised `init --force` for ages;
	// make it real. Same semantic as --yes for init's overwrite check.
	// Long form only — `-f` is already taken globally by --file.
	// --yes stays accepted (global flag) so existing muscle memory and
	// scripts continue to work.
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing COMMITBRIEF.md / OUTPUT.md (alias of --yes for init)")
	return cmd
}

// writeOrSkip writes data to path. If the file already exists and
// overwrite is false, it emits an info-level "skipped" log and returns
// nil — the missing-sibling case (UC-17) needs a non-fatal outcome.
// True I/O failures (permission, parent ENOENT after MkdirAll, etc.)
// still surface as errors. Parent directories are created with 0700
// so `.commitbrief/` doesn't leak more permissively than config.yml.
func writeOrSkip(cat *i18n.Catalog, path string, data []byte, mode os.FileMode, overwrite bool) error {
	switch _, err := os.Stat(path); {
	case err == nil:
		if !overwrite {
			infof("%s", cat.T("init.skipped", path))
			return nil
		}
	case errors.Is(err, fs.ErrNotExist):
		// fall through to write
	default:
		return fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	infof("%s", cat.T("init.wrote", path))
	return nil
}
