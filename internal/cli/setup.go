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
			// Resolve catalog up front so the post-wizard "saved to ..." line
			// honors --lang. resolveContext(false) tolerates a missing repo
			// (setup is the one command users run before having a repo set up).
			ctx, err := resolveContext(false)
			if err != nil {
				return err
			}
			opts := setup.RunOptions{Local: local}
			if local {
				opts.RepoRoot = ctx.RepoRoot
			}
			if _, err := setup.Run(cmd.Context(), opts); err != nil {
				return err
			}
			if local {
				infof("%s", ctx.Catalog.T("setup.saved_local"))
			} else {
				infof("%s", ctx.Catalog.T("setup.saved_global"))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "save to repo .commitbrief/config.yml instead of user-level")
	return cmd
}
