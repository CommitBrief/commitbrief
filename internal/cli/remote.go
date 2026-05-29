// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import "github.com/spf13/cobra"

// newRemoteCmd is the parent for GitHub-facing operations. v1.1.0 ships
// only `pr`; the group is structured to host more (`issue`, `release`)
// later. See ADR-0016.
func newRemoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Drive GitHub operations (PR review) through the gh CLI",
		Long: "Run CommitBrief against GitHub resources via your local `gh` CLI.\n" +
			"Currently: `remote pr <ID>` reviews a pull request and posts findings\n" +
			"as inline comments plus a review verdict. Requires an API provider\n" +
			"(CLI-tool providers claude-cli / gemini-cli / codex-cli are incompatible —\n" +
			"they don't produce structured findings).",
	}
	cmd.AddCommand(newRemotePRCmd())
	return cmd
}
