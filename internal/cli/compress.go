// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/compress"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

func newCompressCmd() *cobra.Command {
	var (
		levelFlag string
		outFlag   string
	)
	cmd := &cobra.Command{
		Use:   "compress",
		Short: "Shrink COMMITBRIEF.md losslessly via the configured provider",
		Long: "Compresses the repo's COMMITBRIEF.md by sending it to the configured\n" +
			"LLM provider with a compression-specific system prompt. Three levels\n" +
			"are available: light (~20-30% reduction, preserves examples),\n" +
			"balanced (~40-60%, default), aggressive (~60-80%, may merge similar\n" +
			"rules). The original is backed up under .commitbrief/backups/ with\n" +
			"an ISO timestamp before the file is replaced.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(true)
			if err != nil {
				return err
			}

			level, err := compress.ParseLevel(levelFlag)
			if err != nil {
				return err
			}

			rulesPath := filepath.Join(app.RepoRoot, rules.Filename)
			data, err := os.ReadFile(rulesPath)
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return errors.New(app.Catalog.T("compress.no_rules", rules.Filename, rulesPath))
				}
				return fmt.Errorf("compress: read %s: %w", rulesPath, err)
			}

			prov, err := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider])
			if err != nil {
				return err
			}
			model := app.Config.Providers[app.Config.Provider].Model
			if model == "" {
				model = prov.DefaultModel()
			}

			infof("%s", app.Catalog.T("compress.compressing", rulesPath, prov.Name(), model, level))

			start := time.Now()
			result, err := compress.Run(cmd.Context(), prov, compress.Request{
				Original: string(data),
				Level:    level,
				Model:    model,
			})
			if err != nil {
				return err
			}
			latency := time.Since(start)

			percent, deltaTokens := result.Savings()
			pricing := prov.Pricing(model)
			// Cost-per-review input savings: input-token rate * delta tokens.
			perReviewSavedUSD := float64(deltaTokens) * pricing.InputPer1M / 1_000_000

			out := cmd.OutOrStdout()
			_, _ = fmt.Fprintf(out, "Original:   %d chars / ~%d tokens\n",
				result.OriginalChars, result.OriginalTokens)
			_, _ = fmt.Fprintf(out, "Compressed: %d chars / ~%d tokens\n",
				result.CompressedChars, result.CompressedTokens)
			_, _ = fmt.Fprintf(out, "Reduction:  %.1f%% (%d tokens)\n", percent, deltaTokens)
			_, _ = fmt.Fprintf(out, "Per-review input savings: $%.5f (at %s rate)\n",
				perReviewSavedUSD, model)
			_, _ = fmt.Fprintf(out, "Compression call cost: in=%d out=%d (≈$%.5f), latency %s\n",
				result.Usage.InputTokens, result.Usage.OutputTokens,
				pricing.Cost(result.Usage), latency.Round(time.Millisecond))

			if result.Aborted {
				return errors.New(app.Catalog.T("compress.aborted_larger", result.AbortReason))
			}

			// --out bypass: write elsewhere, never touch original.
			if outFlag != "" {
				if err := os.WriteFile(outFlag, []byte(result.CompressedContent), 0o644); err != nil {
					return fmt.Errorf("compress: write %s: %w", outFlag, err)
				}
				infof("%s", app.Catalog.T("compress.wrote_out", outFlag, rulesPath))
				return nil
			}

			// Confirmation prompt unless --yes.
			if !global.yes {
				ok, err := ui.AskYesNo(
					os.Stdin,
					cmd.OutOrStderr(),
					app.Catalog.T("compress.replace_prompt"),
					ui.AskOptions{NonInteractive: !ui.IsStdinTTY(os.Stdin)},
				)
				if err != nil {
					return err
				}
				if !ok {
					infof("%s", app.Catalog.T("compress.aborted_user"))
					return nil
				}
			}

			ts := compress.BackupTimestamp(time.Now())
			writtenPath, backupPath, err := compress.Apply(app.RepoRoot, result, "", ts)
			if err != nil {
				return err
			}
			infof("%s", app.Catalog.T("compress.backed_up", backupPath))
			infof("%s", app.Catalog.T("compress.wrote_compressed", writtenPath))
			return nil
		},
	}
	cmd.Flags().StringVar(&levelFlag, "level", "balanced", "compression level: light | balanced | aggressive")
	cmd.Flags().StringVar(&outFlag, "out", "", "write result to this path instead of replacing COMMITBRIEF.md")
	return cmd
}
