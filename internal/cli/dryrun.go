package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

func newDryRunCmd() *cobra.Command {
	return &cobra.Command{
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

			fmt.Fprintln(os.Stdout, "Dry run — no provider call.")
			fmt.Fprintf(os.Stdout, "Origin:        %s\n", rawDiff.Origin)
			fmt.Fprintf(os.Stdout, "Files:         %d (filtered from %d)\n", parsed.FileCount(), before)
			fmt.Fprintf(os.Stdout, "Added lines:   %d\n", parsed.AddedLines())
			fmt.Fprintf(os.Stdout, "Deleted lines: %d\n", parsed.DeletedLines())
			fmt.Fprintf(os.Stdout, "Provider:      %s\n", app.Config.Provider)
			fmt.Fprintf(os.Stdout, "Model:         %s\n", app.Config.Providers[app.Config.Provider].Model)
			fmt.Fprintf(os.Stdout, "Lang:          %s (source: %s)\n", app.Lang.Code, app.Lang.Source)
			fmt.Fprintf(os.Stdout, "Rules source:  %s", loaded.Source)
			if loaded.Path != "" {
				fmt.Fprintf(os.Stdout, " (%s)", loaded.Path)
			}
			fmt.Fprintln(os.Stdout)
			fmt.Fprintf(os.Stdout, "Est. tokens:   %d\n", p.EstimatedTokens())
			fmt.Fprintf(os.Stdout, "Cache key:     %s\n", cacheKey)
			return nil
		},
	}
}
