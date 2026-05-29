// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/setup"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// newProvidersCmd is the `commitbrief providers` subtree. It exposes
// non-destructive operations on the provider/model configuration:
// `list` for visibility, `use` for switching the active default, `test`
// for ping-checking a configured provider. These are the natural
// complement to `setup` (which writes API keys) and let users juggle
// multiple providers without re-running the wizard or hand-editing YAML.
func newProvidersCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "providers",
		Short: "List, switch, and test configured LLM providers",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newProvidersListCmd())
	cmd.AddCommand(newProvidersUseCmd())
	cmd.AddCommand(newProvidersTestCmd())
	return cmd
}

func newProvidersListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Show configured providers (active marker, model, API key status)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(false)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()

			// Union of registered providers (so we surface ones the user has
			// not configured yet) and providers in the merged config map (so
			// custom names still show). Sorted alphabetically.
			seen := map[string]struct{}{}
			for _, n := range provider.Names() {
				seen[n] = struct{}{}
			}
			for n := range app.Config.Providers {
				seen[n] = struct{}{}
			}
			names := make([]string, 0, len(seen))
			for n := range seen {
				names = append(names, n)
			}
			sort.Strings(names)

			if _, err := fmt.Fprintln(w, app.Catalog.T("providers.list.heading")); err != nil {
				return err
			}
			for _, name := range names {
				pc := app.Config.Providers[name]
				marker := "  "
				if name == app.Config.Provider {
					marker = "* "
				}
				keyStatus := app.Catalog.T("providers.key.not_set")
				if pc.APIKey != "" {
					keyStatus = maskAPIKey(pc.APIKey)
				} else if name == "ollama" {
					// Ollama doesn't use API keys; surface base_url instead so
					// `list` doesn't lie about it being "not set".
					keyStatus = pc.BaseURL
					if keyStatus == "" {
						keyStatus = app.Catalog.T("providers.key.not_set")
					}
				}
				model := pc.Model
				if model == "" {
					model = "—"
				}
				if _, err := fmt.Fprintf(w, "%s%-10s  model=%-30s  key=%s\n", marker, name, model, keyStatus); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w, app.Catalog.T("providers.list.footer")); err != nil {
				return err
			}
			return nil
		},
	}
}

func newProvidersUseCmd() *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch the active default provider (no API keys changed)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			app, err := resolveContext(local) // need repo when --local
			if err != nil {
				return err
			}
			if !isRegistered(name) {
				return errors.New(app.Catalog.T("providers.use.unknown", name, provider.Names()))
			}
			// Warn (not fail) when the picked provider has no API key —
			// useful for non-key providers like ollama, and a soft nudge
			// for the case where the user forgot to run setup first.
			pc := app.Config.Providers[name]
			if pc.APIKey == "" && name != "ollama" {
				infof("%s", app.Catalog.T("providers.use.no_key_warning", name))
			}

			// Load and rewrite only the targeted file so we don't accidentally
			// promote merged-in repo-level state to global, or vice versa.
			path, err := targetPath(app.RepoRoot, local)
			if err != nil {
				return err
			}
			cfg, err := config.LoadFile(path)
			if err != nil {
				return err
			}
			if cfg == nil {
				cfg = config.Default()
			}
			cfg.Provider = name
			if err := setup.WriteConfig(path, cfg); err != nil {
				return err
			}
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), app.Catalog.T("providers.use.success", name, path)); err != nil {
				return err
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "write to repo .commitbrief/config.yml instead of user-level")
	return cmd
}

func newProvidersTestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "test <name>",
		Short: "Ping a configured provider to verify the API key and reachability",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			app, err := resolveContext(false)
			if err != nil {
				return err
			}
			if !isRegistered(name) {
				return errors.New(app.Catalog.T("providers.test.unknown", name, provider.Names()))
			}
			pc := app.Config.Providers[name]
			// Single network step, shown through the shared staged-tree
			// progress (a spinner while the ping is in flight). Close keeps
			// the finished stage line above the stdout success summary.
			prog := ui.NewProgress(cmd.ErrOrStderr(), ui.ParseColorMode(global.color), global.quiet)
			defer prog.Close()
			prog.Start(app.Catalog.T("providers.test.pinging", name))
			start := time.Now()
			if err := setup.TestConnection(cmd.Context(), name, pc); err != nil {
				e := errors.New(app.Catalog.T("providers.test.failed", name, err.Error()))
				prog.Fail(e)
				return e
			}
			elapsed := time.Since(start)
			prog.Finish()
			prog.Close()
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), app.Catalog.T("providers.test.success", name, elapsed.Round(time.Millisecond).String())); err != nil {
				return err
			}
			return nil
		},
	}
}

// maskAPIKey shows the first 7 chars (covers prefixes like "sk-ant-",
// "AIza", "sk-") plus the last 4, dots in between. Short keys get
// summarised as "configured" so we never accidentally print a low-entropy
// secret verbatim.
func maskAPIKey(s string) string {
	if len(s) < 12 {
		return "(configured)"
	}
	return s[:7] + "…" + s[len(s)-4:]
}

func isRegistered(name string) bool {
	for _, n := range provider.Names() {
		if n == name {
			return true
		}
	}
	return false
}

// targetPath mirrors setup.targetConfigPath logic without depending on
// the wizard's RunOptions struct. Used by `providers use` to write the
// active-provider change.
func targetPath(repoRoot string, local bool) (string, error) {
	if local {
		if repoRoot == "" {
			return "", errors.New("--local requires a repository")
		}
		return setup.RepoConfigPath(repoRoot), nil
	}
	return setup.GlobalConfigPath()
}
