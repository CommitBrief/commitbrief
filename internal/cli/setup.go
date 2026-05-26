package cli

import (
	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/setup"
)

func newSetupCmd() *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive provider + API key wizard",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := setup.RunOptions{Local: local}
			if local {
				ctx, err := resolveContext(true)
				if err != nil {
					return err
				}
				opts.RepoRoot = ctx.RepoRoot
			}
			_, err := setup.Run(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if local {
				infof("Configuration saved to ./.commitbrief/config.yml")
			} else {
				infof("Configuration saved to ~/.commitbrief/config.yml")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "save to repo .commitbrief/config.yml instead of user-level")
	return cmd
}
