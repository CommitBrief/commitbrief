package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

func newDryRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dry-run",
		Short: "Build prompt and report what would be sent; no API call",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(true)
			if err != nil {
				return err
			}
			rawDiff, err := fetchDiff(app.Repo, reviewScope)
			if err != nil {
				return err
			}
			parsed, err := diff.Parse(rawDiff)
			if err != nil {
				return err
			}
			matcher := buildMatcher(app.RepoRoot)
			before := parsed.FileCount()
			parsed = diff.Filter(parsed, matcher)
			loaded, err := rules.Load(app.RepoRoot)
			if err != nil {
				return err
			}
			p := prompt.Build(loaded, app.Lang, parsed.String())

			cacheKey := cache.Compute(cache.ComputeArgs{
				Diff:         parsed.String(),
				SystemPrompt: p.System,
				Provider:     app.Config.Provider,
				Model:        app.Config.Providers[app.Config.Provider].Model,
				Lang:         app.Lang.Code,
			})

			w := cmd.OutOrStdout()
			lines := []string{
				"Dry run — no provider call.",
				fmt.Sprintf("Origin:        %s", rawDiff.Origin),
				fmt.Sprintf("Files:         %d (filtered from %d)", parsed.FileCount(), before),
				fmt.Sprintf("Added lines:   %d", parsed.AddedLines()),
				fmt.Sprintf("Deleted lines: %d", parsed.DeletedLines()),
				fmt.Sprintf("Provider:      %s", app.Config.Provider),
				fmt.Sprintf("Model:         %s", app.Config.Providers[app.Config.Provider].Model),
				fmt.Sprintf("Lang:          %s (source: %s)", app.Lang.Code, app.Lang.Source),
			}
			rulesLine := fmt.Sprintf("Rules source:  %s", loaded.Source)
			if loaded.Path != "" {
				rulesLine += fmt.Sprintf(" (%s)", loaded.Path)
			}
			lines = append(lines,
				rulesLine,
				fmt.Sprintf("Est. tokens:   %d", p.EstimatedTokens()),
				fmt.Sprintf("Cache key:     %s", cacheKey),
			)
			for _, line := range lines {
				if _, err := fmt.Fprintln(w, line); err != nil {
					return fmt.Errorf("dry-run: write: %w", err)
				}
			}
			return nil
		},
	}
	bindScopeFlags(cmd)
	return cmd
}
