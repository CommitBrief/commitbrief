// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// newSummaryCmd is the `commitbrief summary [<git diff args>...]` entry point
// (ADR-0020): a read-only, human-readable digest of a set of changes, grouped
// by logical area and — for ranges — attributed to the short commit hash(es)
// responsible. Unlike a review it produces no findings, severity, or JSON;
// the provider returns prose that is emitted verbatim.
//
// Scope mirrors the review surface: no args ⇒ staged (default) / --unstaged;
// positional args ⇒ an arbitrary `git diff` range, forwarded verbatim exactly
// like the `diff` subcommand (HEAD, HEAD~3 HEAD, main...develop, …). For a
// range, the matching commit messages are ingested as context.
func newSummaryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "summary [<git diff args>...]",
		Short: "Summarize a set of changes in plain language (read-only)",
		Long: "Explain what changed — and, when the commit messages make it clear, " +
			"why — as a short, human-readable digest grouped by logical area.\n\n" +
			"With no arguments it summarizes the staged diff (--unstaged for the " +
			"working tree). Pass `git diff` arguments to summarize an arbitrary " +
			"range exactly like the `diff` subcommand: `commitbrief summary " +
			"main...develop`, `commitbrief summary HEAD~3 HEAD`. For a range, the " +
			"commit messages in that range are taken into account and each line is " +
			"attributed to the short commit hash(es) responsible.\n\n" +
			"Output is plain text; use -o/--output to write it to a file. The " +
			"pre-send guard, secret scan, and cost preflight all apply as they do " +
			"for a review. This command never writes to git.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSummary(cmd, reviewScope, args)
		},
	}
	bindScopeFlags(cmd)
	return cmd
}

func runSummary(cmd *cobra.Command, scope reviewScopeFlags, diffArgs []string) error {
	ctx := cmd.Context()
	app, err := resolveContext(true)
	if err != nil {
		return err
	}

	// summary emits prose, not findings. Reject the structured-output flags
	// (--json/--markdown drive the findings renderers) and the findings-only
	// flags (--suggest-commit/--fail-on/--min-severity have nothing to act
	// on) up front, before any provider call, rather than silently ignoring
	// them. --output is supported and handled below.
	if global.json || global.markdown {
		return errors.New(app.Catalog.T("summary.flag_conflict_format"))
	}
	if global.suggestCommit || global.failOn != "" || global.minSeverity != "" {
		return errors.New(app.Catalog.T("summary.flag_conflict_review"))
	}

	prog := ui.NewProgress(cmd.ErrOrStderr(), ui.ParseColorMode(global.color), global.quiet)
	defer prog.Close()

	prog.Start(app.Catalog.T("progress.searching"))
	rawDiff, err := fetchDiff(app.Repo, scope, diffArgs)
	if err != nil {
		prog.Fail(err)
		return err
	}
	parsed, err := diff.Parse(rawDiff)
	if err != nil {
		prog.Fail(err)
		return err
	}
	parsed = diff.Filter(parsed, buildMatcher(app.RepoRoot))
	parsed, err = diff.KeepPaths(parsed, global.files, global.dirs)
	if err != nil {
		err = errors.New(app.Catalog.T("filter.glob.invalid", err.Error()))
		prog.Fail(err)
		return err
	}
	if parsed.Empty() {
		prog.Finish()
		prog.Close()
		infof("%s", app.Catalog.T("summary.no_changes"))
		return nil
	}
	prog.Info(app.Catalog.T("progress.diff_stats",
		parsed.FileCount(), parsed.AddedLines(), parsed.DeletedLines()))
	diffText := parsed.String()

	// Commit manifest (the "and the commit messages, if any" half of the
	// feature). Only a clean two-endpoint range yields commits — a bare ref
	// or a pathspec-bearing invocation degrades to a diff-only summary so we
	// never walk all of history. Best-effort: a git error here is swallowed
	// and the summary proceeds without attribution.
	var manifest string
	if logArgs, ok := deriveLogRange(diffArgs); ok {
		if commits, cErr := git.RangeCommits(ctx, app.RepoRoot, logArgs); cErr == nil {
			manifest = formatManifest(commits)
		}
	}

	// Shared reader for the guard / secret / preflight prompts (UC-21: one
	// buffer over os.Stdin, never several competing ones).
	stdinReader := bufio.NewReader(os.Stdin)

	prog.Pause()
	if res, _ := guard.CheckDiffForLocalConfig(parsed, guard.Options{
		AssumeYes:      global.yes,
		NonInteractive: !ui.IsStdinTTY(os.Stdin),
		Interactive:    ui.IsStdinTTY(os.Stdin),
		Catalog:        app.Catalog,
		Reader:         stdinReader,
	}); res == guard.Abort {
		return errors.New("aborted by pre-send guard")
	}
	if app.Config.Guard.SecretScan && !global.allowSecrets {
		if matches := guard.ScanForSecrets(diffText); len(matches) > 0 {
			if abort := handleSecretMatches(cmd, app, matches, stdinReader); abort {
				return errors.New(app.Catalog.T("guard.secrets.aborted_user"))
			}
		}
	}
	prog.Resume()

	prog.Start(app.Catalog.T("progress.preparing"))
	prov, err := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider])
	if err != nil {
		prog.Fail(err)
		return err
	}
	// --with-context (ADR-0017) only means anything for a CLI-backed provider:
	// an API provider has no filesystem to read, so the flag is inert there.
	// Reject it before any provider call rather than silently ignoring it.
	// Mirrors the review path (review.go).
	_, plainText := prov.(provider.PlainTextEmitter)
	if global.withContext && !plainText {
		ctxErr := errors.New(app.Catalog.T("context.cli_only"))
		prog.Fail(ctxErr)
		return ctxErr
	}
	if global.withContext {
		prog.Info(app.Catalog.T("context.warning"))
	}
	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}
	p := prompt.BuildSummary(diffText, manifest, app.Lang, global.withContext)

	// --show-prompt: dump the exact prompt and stop (no provider call, no
	// cost). After the guard/secret scan so a secret in the dump surfaces first.
	if global.showPrompt {
		prog.Finish()
		prog.Clear()
		return showPromptOutput(cmd, p)
	}

	// Cache identity folds in the manifest (distinct commit messages over an
	// identical diff must not collide) and namespaces under Mode "summary".
	cacheDiff := diffText
	if manifest != "" {
		cacheDiff = diffText + "\x1e" + manifest
	}
	cacheKey := cache.Compute(cache.ComputeArgs{
		Diff:         cacheDiff,
		SystemPrompt: p.System,
		Provider:     prov.Name(),
		Model:        model,
		Lang:         app.Lang.Code,
		Mode:         "summary",
		WithContext:  global.withContext,
	})
	cacheStore, err := openCache(app.RepoRoot, app.Config.Cache)
	if err != nil {
		infof("%s", app.Catalog.T("review.cache_disabled", err))
	}

	var content string
	if !global.noCache && cacheStore != nil {
		if entry, hit := cacheStore.Get(cacheKey); hit {
			content = entry.Result.Content
		}
	}

	if content == "" {
		prog.Finish() // preparing → done

		if app.Config.Guard.TokenPreflight {
			prog.Pause()
			if abort := handleTokenPreflight(cmd, app, prov, p, model, stdinReader); abort {
				return errors.New(app.Catalog.T("guard.tokens.aborted_user"))
			}
			prog.Resume()
		}
		if !global.noCostCheck {
			estUsage := provider.Usage{
				InputTokens:  p.EstimatedTokens(),
				OutputTokens: estimateOutputTokens(p.EstimatedTokens()),
			}
			estCost := resolvePricing(app.Config, prov, model).Cost(estUsage)
			prog.Pause()
			if abort := handleCostPreflight(cmd, app, estCost, stdinReader); abort {
				return errors.New(app.Catalog.T("cost.aborted_user"))
			}
			prog.Resume()
		}

		prog.Start(app.Catalog.T("summary.generating"))
		resp, callErr := prov.Review(ctx, provider.Request{
			Model:        model,
			SystemPrompt: p.System,
			UserPrompt:   p.User,
			Lang:         app.Lang.Code,
			FreeForm:     true,
			// --with-context (ADR-0017): inert for API providers (they ignore
			// ProviderOpts); the clireview backend reads it to grant read tools
			// and run in the repo root. Only meaningful when withContext is
			// true, which the validation above already restricted to a CLI
			// (PlainTextEmitter) provider.
			ProviderOpts: provider.ContextOptions{
				Enabled:  global.withContext,
				RepoRoot: app.RepoRoot,
			},
		})
		if callErr != nil {
			prog.Fail(callErr)
			return fmt.Errorf("provider %s: %w", prov.Name(), callErr)
		}
		prog.Finish()
		content = resp.Content

		if !global.noCache && cacheStore != nil {
			_ = cacheStore.Put(cacheKey, cache.Entry{
				Key: cache.KeyMeta{
					Provider: prov.Name(),
					Model:    model,
					Lang:     app.Lang.Code,
				},
				Result: cache.Result{
					Content: content,
					Format:  cache.FormatPlainText,
					Tokens: cache.Tokens{
						Input:  resp.Usage.InputTokens,
						Output: resp.Usage.OutputTokens,
						Cached: resp.Usage.CachedInputTokens,
					},
				},
			})
		}
	} else {
		prog.Finish() // preparing → done (cache hit, no call)
	}
	prog.Clear()

	if strings.TrimSpace(content) == "" {
		return errors.New(app.Catalog.T("summary.empty"))
	}
	return emitSummary(cmd, content)
}

// emitSummary writes the provider's plain-text digest to the output sink
// (stdout, or --output FILE), trimmed and newline-terminated so it sits
// cleanly above the next shell prompt. No cards/glamour rendering — the
// summary is already in its final human-readable form.
func emitSummary(cmd *cobra.Command, content string) error {
	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()
	if _, err := io.WriteString(w, strings.TrimSpace(content)+"\n"); err != nil {
		return fmt.Errorf("emit summary: %w", err)
	}
	return nil
}

// deriveLogRange turns the user's `git diff` arguments into a clean
// two-endpoint `git log` range, returning ok=false when no such range can be
// derived (in which case the summary proceeds diff-only, with no commit
// attribution). It deliberately refuses anything ambiguous:
//
//   - "main...develop" → "main..develop"  (PR-style three-dot diff → the
//     commits unique to develop)
//   - "main..develop"  → "main..develop"  (already a range)
//   - "HEAD~3 HEAD"     → "HEAD~3..HEAD"   (two endpoints)
//   - "HEAD" / "<hash>" → ok=false         (a single ref would make git log
//     walk all of history, not "this change")
//   - anything with flags or a `--` pathspec → ok=false
func deriveLogRange(diffArgs []string) ([]string, bool) {
	if len(diffArgs) == 0 {
		return nil, false
	}
	for _, a := range diffArgs {
		if a == "--" || strings.HasPrefix(a, "-") {
			return nil, false
		}
	}
	switch len(diffArgs) {
	case 1:
		a := diffArgs[0]
		if strings.Contains(a, "...") {
			return []string{strings.Replace(a, "...", "..", 1)}, true
		}
		if strings.Contains(a, "..") {
			return []string{a}, true
		}
		return nil, false
	case 2:
		// Two bare refs ("main feature", "HEAD~3 HEAD") → a..b. Refs already
		// carrying range syntax here would be malformed git diff input, so a
		// plain join is the faithful mapping.
		if strings.Contains(diffArgs[0], "..") || strings.Contains(diffArgs[1], "..") {
			return nil, false
		}
		return []string{diffArgs[0] + ".." + diffArgs[1]}, true
	default:
		return nil, false
	}
}

// maxManifestFilesPerCommit caps how many touched paths a single commit
// contributes to the manifest. A bulk commit (e.g. a man-page regeneration
// touching every page) would otherwise dump dozens of low-signal paths that
// bloat the prompt without improving area attribution; we list the first few
// and summarize the rest as "+N more".
const maxManifestFilesPerCommit = 12

// formatManifest renders the commit metadata as a compact, fenced-data block
// for the summary user prompt. Returns "" for an empty manifest so the
// caller (and BuildSummary) can branch on "no attribution available".
func formatManifest(commits []git.CommitMeta) string {
	if len(commits) == 0 {
		return ""
	}
	var b strings.Builder
	for i, c := range commits {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s  %s", c.Short, c.Subject)
		if c.Body != "" {
			// Indent the body so it reads as belonging to this commit.
			for _, line := range strings.Split(c.Body, "\n") {
				fmt.Fprintf(&b, "\n    %s", line)
			}
		}
		if len(c.Files) > 0 {
			fmt.Fprintf(&b, "\n    files: %s", formatManifestFiles(c.Files))
		}
	}
	return b.String()
}

// formatManifestFiles joins a commit's paths, capping the list at
// maxManifestFilesPerCommit and appending a "+N more" tail when it overflows.
func formatManifestFiles(files []string) string {
	if len(files) <= maxManifestFilesPerCommit {
		return strings.Join(files, ", ")
	}
	shown := strings.Join(files[:maxManifestFilesPerCommit], ", ")
	return fmt.Sprintf("%s, +%d more", shown, len(files)-maxManifestFilesPerCommit)
}
