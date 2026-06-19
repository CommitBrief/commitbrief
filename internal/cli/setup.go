// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/alias"
	"github.com/CommitBrief/commitbrief/internal/setup"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// aliasPromptSentinel is the NoOptDefVal for --alias: a bare `--alias`
// (no value) lands here and triggers the interactive name prompt, while
// `--alias=<name>` carries the explicit name. The `<`/`>` make it invalid
// as an alias name (so an explicit value can never collide) while still
// reading as a clear placeholder in `--help`.
const aliasPromptSentinel = "<prompt>"

func newSetupCmd() *cobra.Command {
	var local bool
	var aliasFlag string
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Interactive provider + API key wizard",
		Long: `Interactive provider + API key wizard.

With --alias, setup skips the provider wizard and instead installs a shell
alias (default 'cbr') for commitbrief into your shell startup file:

    commitbrief setup --alias        # prompts for the alias name (default cbr)
    commitbrief setup --alias=cb     # installs 'cb' without prompting

Supported shells: bash, zsh, fish, PowerShell, and cmd.exe (via DOSKEY). The
alias is written into a managed block so re-running updates it in place. If
the chosen name already shadows a command on your PATH you are warned first.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("alias") {
				return runAliasSetup(cmd, aliasFlag)
			}
			// Resolve catalog up front so the post-wizard "saved to ..." line
			// honors --lang. resolveContext(false) tolerates a missing repo
			// (setup is the one command users run before having a repo set up).
			ctx, err := resolveContext(false)
			if err != nil {
				return err
			}
			opts := setup.RunOptions{Local: local, Catalog: ctx.Catalog}
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
	cmd.Flags().StringVar(&aliasFlag, "alias", "", "install a shell alias for commitbrief (default cbr); skips the provider wizard. Use --alias for an interactive prompt or --alias=<name> to set it directly")
	// NoOptDefVal makes a bare `--alias` (no `=value`) valid and routes it to
	// the interactive prompt; `--alias=<name>` carries the explicit name.
	cmd.Flags().Lookup("alias").NoOptDefVal = aliasPromptSentinel
	return cmd
}

// runAliasSetup drives `commitbrief setup --alias`. aliasFlag is the raw flag
// value: the sentinel means "prompt", anything else is the explicit name.
func runAliasSetup(cmd *cobra.Command, aliasFlag string) error {
	ctx, err := resolveContext(false)
	if err != nil {
		return err
	}
	cat := ctx.Catalog

	interactive := aliasFlag == aliasPromptSentinel
	name := ""
	if !interactive {
		name = aliasFlag
	}

	tty := ui.IsStdinTTY(os.Stdin)
	if interactive && !tty {
		// No terminal to prompt on. With --yes fall back to the default
		// name; otherwise tell the user to name it explicitly.
		if global.yes {
			interactive = false
			name = alias.DefaultName
		} else {
			return errors.New(cat.T("setup.alias.no_tty"))
		}
	}

	outcome, err := setup.RunAlias(cmd.Context(), setup.AliasOptions{
		Name:        name,
		Interactive: interactive && tty,
		AutoYes:     global.yes,
		Catalog:     cat,
	})
	if err != nil {
		return err
	}
	if outcome.Canceled {
		infof("%s", cat.T("setup.alias.canceled"))
		return nil
	}

	// In non-interactive mode the conflict could not be confirmed
	// interactively, so surface the warning here before the success line.
	if outcome.Conflict != "" && (!interactive || !tty) {
		infof("%s", cat.T("setup.alias.conflict_warn", outcome.Name, outcome.Conflict))
	}

	if outcome.Changed {
		infof("%s", cat.T("setup.alias.installed", outcome.Name, outcome.Path))
	} else {
		infof("%s", cat.T("setup.alias.unchanged", outcome.Name, outcome.Path))
	}
	if outcome.ReloadCmd != "" {
		infof("%s", cat.T("setup.alias.reload", outcome.ReloadCmd))
	} else {
		infof("%s", cat.T("setup.alias.reload_restart"))
	}
	return nil
}
