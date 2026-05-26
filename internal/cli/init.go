package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/rules"
)

func newInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write COMMITBRIEF.md to the current repo using the built-in default rules",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, err := resolveContext(true)
			if err != nil {
				return err
			}
			path := filepath.Join(ctx.RepoRoot, rules.Filename)

			if _, err := os.Stat(path); err == nil && !global.yes {
				return fmt.Errorf("%s already exists; re-run with --yes to overwrite", path)
			} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat %s: %w", path, err)
			}

			loaded := rules.Default()
			if err := os.WriteFile(path, []byte(loaded.Content), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			infof("Wrote %s", path)
			return nil
		},
	}
}
