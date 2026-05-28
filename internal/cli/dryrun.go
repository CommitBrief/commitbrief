// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/ignore"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
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
			rawDiff, err := fetchDiff(app.Repo, reviewScope, nil)
			if err != nil {
				return err
			}
			parsed, err := diff.Parse(rawDiff)
			if err != nil {
				return err
			}
			before := parsed.FileCount()
			builtinMatcher := ignore.Builtin()
			afterBuiltin := diff.Filter(parsed, builtinMatcher)
			repoIgnore, _ := ignore.ParseFile(filepath.Join(app.RepoRoot, ignore.Filename))
			combined := ignore.Compose(builtinMatcher, repoIgnore)
			parsed = diff.Filter(parsed, combined)
			builtinExcluded := before - afterBuiltin.FileCount()
			repoExcluded := afterBuiltin.FileCount() - parsed.FileCount()
			beforePathFilter := parsed.FileCount()
			parsed = diff.KeepPaths(parsed, global.files, global.dirs)
			pathFilterExcluded := beforePathFilter - parsed.FileCount()
			loaded, err := rules.Load(app.RepoRoot)
			if err != nil {
				return err
			}
			outputLoaded, err := rules.LoadOutput(app.RepoRoot, userHome())
			if err != nil {
				return err
			}
			if outputLoaded.Source != rules.SourceDefault {
				// Pre-send template guard mirrors the runReview path so
				// dry-run fails fast on the same condition (ADR-0014 §5).
				if vErr := render.ValidateOutputTemplate(outputLoaded.Content); vErr != nil {
					return errors.New(app.Catalog.T("output.template.invalid", outputLoaded.Path, vErr.Error()))
				}
			}
			// Hoist the diff text once; prompt build + cache key both
			// need it and Diff.String() rewalks the file tree on
			// every call.
			diffText := parsed.String()
			p := prompt.Build(loaded, app.Lang, diffText)

			// UC-19: surface output-tokens / context-window / cost
			// alongside the input-tokens estimate so dry-run answers
			// "what will this cost me?" without having to actually
			// fire the review. Provider instantiation can fail in
			// unusual setups (missing API key, etc.); we tolerate
			// that by zero-ing the provider-derived numbers rather
			// than aborting the whole dry-run report.
			modelName := app.Config.Providers[app.Config.Provider].Model
			inputTokens := p.EstimatedTokens()
			outputTokens := estimateOutputTokens(inputTokens)
			var contextWindow int
			var estCost float64
			if prov, perr := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider]); perr == nil {
				if modelName == "" {
					modelName = prov.DefaultModel()
				}
				contextWindow = prov.ContextWindow(modelName)
				estCost = resolvePricing(app.Config, prov, modelName).Cost(provider.Usage{
					InputTokens:  inputTokens,
					OutputTokens: outputTokens,
				})
			}

			cacheKey := cache.Compute(cache.ComputeArgs{
				Diff:         diffText,
				SystemPrompt: p.System,
				Provider:     app.Config.Provider,
				Model:        modelName,
				Lang:         app.Lang.Code,
			})

			w := cmd.OutOrStdout()
			lines := []string{
				"Dry run — no provider call.",
				fmt.Sprintf("Origin:        %s", rawDiff.Origin),
				fmt.Sprintf("Files (input): %d", before),
				fmt.Sprintf("  built-in ignore filtered:        %d", builtinExcluded),
				fmt.Sprintf("  .commitbriefignore net filtered: %d", repoExcluded),
				fmt.Sprintf("  --file/--dir path filter:        %d", pathFilterExcluded),
				fmt.Sprintf("Files (review): %d", parsed.FileCount()),
				fmt.Sprintf("Added lines:   %d", parsed.AddedLines()),
				fmt.Sprintf("Deleted lines: %d", parsed.DeletedLines()),
				fmt.Sprintf("Provider:      %s", app.Config.Provider),
				fmt.Sprintf("Model:         %s", modelName),
				fmt.Sprintf("Lang:          %s (source: %s)", app.Lang.Code, app.Lang.Source),
			}
			rulesLine := fmt.Sprintf("Rules source:  %s", loaded.Source)
			if loaded.Path != "" {
				rulesLine += fmt.Sprintf(" (%s)", loaded.Path)
			}
			outputLine := fmt.Sprintf("Output source: %s", outputLoaded.Source)
			if outputLoaded.Path != "" {
				outputLine += fmt.Sprintf(" (%s)", outputLoaded.Path)
			}
			lines = append(lines,
				rulesLine,
				outputLine,
				fmt.Sprintf("Input tokens (est):  %d", inputTokens),
				fmt.Sprintf("Output tokens (est): %d", outputTokens),
				fmt.Sprintf("Context window:      %d", contextWindow),
				fmt.Sprintf("Cost estimate:       $%.4f", estCost),
				fmt.Sprintf("Cache key:           %s", cacheKey),
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
