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
	return &cobra.Command{
		Use:   "init",
		Short: "Write COMMITBRIEF.md and a per-user OUTPUT.md template",
		Long: "Writes two files:\n" +
			"  - COMMITBRIEF.md at the repo root (team-shared review content)\n" +
			"  - .commitbrief/OUTPUT.md (per-user output format template; gitignored)\n" +
			"Both fall back to embedded defaults at runtime, so creating these files\n" +
			"is only necessary when you want to customize the prompt.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveContext(true)
			if err != nil {
				return err
			}

			rulesPath := filepath.Join(ctx.RepoRoot, rules.Filename)
			outputPath := filepath.Join(ctx.RepoRoot, rules.LocalSubdir, rules.OutputFilename)

			if err := writeIfAbsent(ctx.Catalog, rulesPath, []byte(rules.Default().Content), 0o644); err != nil {
				return err
			}
			if err := writeIfAbsent(ctx.Catalog, outputPath, []byte(rules.DefaultOutput().Content), 0o644); err != nil {
				return err
			}
			return nil
		},
	}
}

// writeIfAbsent writes data to path unless the file already exists. The
// --yes flag bypasses the "already exists" guard and forces an overwrite.
// Creates any missing parent directories with 0700 (the .commitbrief/
// subdirectory must be readable to its owner only, like config.yml).
func writeIfAbsent(cat *i18n.Catalog, path string, data []byte, mode os.FileMode) error {
	switch _, err := os.Stat(path); {
	case err == nil:
		if !global.yes {
			return errors.New(cat.T("init.exists", path))
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
