// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/clipboard"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/ignore"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

type reviewScopeFlags struct {
	staged   bool
	unstaged bool
}

var reviewScope reviewScopeFlags

func bindScopeFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.BoolVarP(&reviewScope.staged, "staged", "s", false, "review staged changes (default)")
	f.BoolVarP(&reviewScope.unstaged, "unstaged", "u", false, "review unstaged changes")
	cmd.MarkFlagsMutuallyExclusive("staged", "unstaged")
}

func runReview(cmd *cobra.Command, scope reviewScopeFlags, diffArgs []string) error {
	ctx := cmd.Context()
	app, err := resolveContext(true)
	if err != nil {
		return err
	}

	// Load rules + output template up front so any "using built-in"
	// infof emissions land BEFORE the progress UI starts animating —
	// otherwise they would interleave with the spinner's cursor-up
	// redraws and look garbled.
	loaded, err := rules.Load(app.RepoRoot)
	if err != nil {
		return err
	}
	if loaded.Source == rules.SourceDefault {
		infof("%s", app.Catalog.T("rules.using_default"))
	}
	outputLoaded, err := rules.LoadOutput(app.RepoRoot, userHome())
	if err != nil {
		return err
	}
	if outputLoaded.Source == rules.SourceDefault {
		infof("%s", app.Catalog.T("rules.output.using_default"))
	} else if err := render.ValidateOutputTemplate(outputLoaded.Content); err != nil {
		// Pre-send guard (ADR-0014 §5): bail out before any provider call so
		// a malformed user template doesn't burn tokens. The embedded
		// default is presumed-valid via release-check.sh and skipped here.
		return errors.New(app.Catalog.T("output.template.invalid", outputLoaded.Path, err.Error()))
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
	matcher := buildMatcher(app.RepoRoot)
	parsed = diff.Filter(parsed, matcher)
	parsed = diff.KeepPaths(parsed, global.files, global.dirs)
	if parsed.Empty() {
		prog.Finish()
		prog.Close()
		infof("%s", app.Catalog.T("review.no_changes"))
		return nil
	}
	prog.Info(app.Catalog.T("progress.diff_stats",
		parsed.FileCount(), parsed.AddedLines(), parsed.DeletedLines()))

	// Hoist the diff text once. The downstream pipeline asks for it
	// 3+ times (secret scan, prompt builder, cache key) and each
	// Diff.String() call walks the file/hunk/line tree afresh. On
	// large diffs the repeat allocations are real GC pressure.
	diffText := parsed.String()

	// Guard + secret scan can prompt interactively. Pause the animation
	// so the prompt has a clean canvas; Resume redraws the tree below
	// the prompt afterwards. Both are typically silent (no-op) on a
	// clean diff, so the brief flicker is rare in practice.
	prog.Pause()
	if res, _ := guard.CheckDiffForLocalConfig(parsed, guard.Options{
		AssumeYes:      global.yes,
		NonInteractive: !ui.IsStdinTTY(os.Stdin),
	}); res == guard.Abort {
		return errors.New("aborted by pre-send guard")
	}

	// Pre-send secret scan (ADR-0007 follow-up, v0.8.0). Looks for
	// credential-shaped patterns in the *added* diff lines before any
	// LLM call. Off by setting guard.secret_scan=false in config; user-
	// bypassable per-invocation via --allow-secrets or --yes.
	if app.Config.Guard.SecretScan && !global.allowSecrets {
		matches := guard.ScanForSecrets(diffText)
		if len(matches) > 0 {
			if abort := handleSecretMatches(cmd, app, matches); abort {
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
	// PlainTextEmitter providers (claude-cli / gemini-cli) get the
	// plain-text response-format contract instead of the JSON one —
	// the host CLI's agentic system prompt makes structured-output
	// guarantees unreliable. See ADR-0009 supersession note and the
	// clireview package.
	_, plainText := prov.(provider.PlainTextEmitter)
	var p prompt.Prompt
	if plainText {
		p = prompt.BuildPlainText(loaded, app.Lang, diffText)
	} else {
		p = prompt.Build(loaded, app.Lang, diffText)
	}

	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}

	cacheKey := cache.Compute(cache.ComputeArgs{
		Diff:         diffText,
		SystemPrompt: p.System,
		Provider:     prov.Name(),
		Model:        model,
		Lang:         app.Lang.Code,
	})

	cacheStore, err := openCache(app.RepoRoot)
	if err != nil {
		infof("%s", app.Catalog.T("review.cache_disabled", err))
	}

	if !global.noCache && cacheStore != nil {
		if entry, hit := cacheStore.Get(cacheKey); hit {
			prog.Finish()
			// Cards/JSON/Markdown render takes over the screen — clear
			// the progress tree so the breadcrumbs don't sit above the
			// real output as duplicate clutter.
			prog.Clear()
			usage := provider.Usage{
				InputTokens:       entry.Result.Tokens.Input,
				OutputTokens:      entry.Result.Tokens.Output,
				CachedInputTokens: entry.Result.Tokens.Cached,
			}
			// On a cache hit no provider call is made; the cost figure is
			// what would have been spent — surfaced as "Saved" by the
			// verbose footer (see render/verbose.go).
			meta := render.Meta{
				Provider:     prov.Name(),
				Model:        model,
				Lang:         app.Lang.Code,
				Cached:       true,
				Timestamp:    entry.CreatedAt,
				Usage:        usage,
				Cost:         prov.Pricing(model).Cost(usage),
				Files:        parsed.FileCount(),
				LinesAdded:   parsed.AddedLines(),
				LinesRemoved: parsed.DeletedLines(),
				RulesLoaded:  loaded.Source != rules.SourceDefault,
			}
			// Parse Findings unless the entry was written in markdown-fallback
			// or plain-text mode — in those cases the cached Content is
			// already in its final renderable shape and there is no
			// findings array to recover.
			var findings []render.Finding
			switch entry.Result.Format {
			case cache.FormatJSON, "":
				findings, _ = render.ParseFindings(entry.Result.Content)
			}
			if entry.Result.Format == cache.FormatPlainText {
				// CLI-emitted output: stream the cached body verbatim to
				// stdout instead of going through the cards renderer.
				if err := emitPlainText(cmd, entry.Result.Content); err != nil {
					return err
				}
			} else if err := renderResult(cmd, entry.Result.Content, outputLoaded.Content, findings, meta); err != nil {
				return err
			}
			handleCopyFlag(cmd, app, findings)
			return applyFailOn(cmd, app, findings)
		}
	}

	prog.Finish() // Preparing → done

	// Cost preflight (11.5.6): we're past the cache lookup and about to
	// spend real tokens. If the estimated cost exceeds the configured
	// threshold, prompt the user (TTY) or abort (non-TTY). --yes and
	// --no-cost-check both bypass with an info notice. Pause the
	// progress animation around the prompt so the user sees a clean
	// y/N question instead of a flickering tree underneath.
	if !global.noCostCheck {
		estUsage := provider.Usage{
			InputTokens:  p.EstimatedTokens(),
			OutputTokens: estimateOutputTokens(p.EstimatedTokens()),
		}
		estCost := prov.Pricing(model).Cost(estUsage)
		prog.Pause()
		if abort := handleCostPreflight(cmd, app, estCost); abort {
			return errors.New(app.Catalog.T("cost.aborted_user"))
		}
		prog.Resume()
	}

	prog.Start(app.Catalog.T("progress.thinking"))
	start := time.Now()
	req := provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
	}
	var (
		content string
		usage   provider.Usage
		format  string
	)
	if plainText {
		// CLI-backed providers: single-shot call, no JSON parsing, no
		// retry-once. The host CLI returns the formatted plain-text
		// review which we stream straight to stdout after Clear.
		resp, callErr := prov.Review(ctx, req)
		if callErr != nil {
			prog.Fail(callErr)
			return fmt.Errorf("provider %s: %w", prov.Name(), callErr)
		}
		content, usage, format = resp.Content, resp.Usage, cache.FormatPlainText
	} else {
		var callErr error
		content, usage, format, callErr = tryStructuredReview(ctx, prov, req, func() {
			// First attempt produced unparseable JSON; ADR-0014 §4
			// retry fires next. Mark the current "Thinking..." as
			// Soft (neutral) and start a fresh "Retrying..." stage so
			// the user sees we noticed the first attempt was iffy.
			prog.Soft()
			prog.Start(app.Catalog.T("progress.retrying"))
		})
		if callErr != nil {
			prog.Fail(callErr)
			return fmt.Errorf("provider %s: %w", prov.Name(), callErr)
		}
	}
	prog.Finish()
	// Cards / JSON / Markdown render takes over the screen below —
	// erase the progress tree so the breadcrumbs don't sit above the
	// real output as duplicate clutter.
	prog.Clear()
	latency := time.Since(start)

	// Parse + warn happen here on a fresh call (the cache-hit path above
	// honours the cached Format and skips this warning to avoid repeats).
	// FormatPlainText is a deliberate non-JSON path (CLI providers); no
	// warning. FormatMarkdownFallback is a degradation (API provider
	// produced unparseable JSON despite the retry) and DOES warn.
	var findings []render.Finding
	switch format {
	case cache.FormatJSON:
		findings, _ = render.ParseFindings(content)
	case cache.FormatMarkdownFallback:
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), app.Catalog.T("review.degraded"))
	}

	respModel := model
	meta := render.Meta{
		Provider:     prov.Name(),
		Model:        respModel,
		Lang:         app.Lang.Code,
		Usage:        usage,
		Cost:         prov.Pricing(respModel).Cost(usage),
		Latency:      latency,
		Timestamp:    time.Now().UTC(),
		Files:        parsed.FileCount(),
		LinesAdded:   parsed.AddedLines(),
		LinesRemoved: parsed.DeletedLines(),
		RulesLoaded:  loaded.Source != rules.SourceDefault,
	}

	if !global.noCache && cacheStore != nil {
		_ = cacheStore.Put(cacheKey, cache.Entry{
			Key: cache.KeyMeta{
				DiffHash:         "sha256:" + cacheKey[:16],
				SystemPromptHash: "",
				Provider:         prov.Name(),
				Model:            respModel,
				Lang:             app.Lang.Code,
			},
			Result: cache.Result{
				Content: content,
				Format:  format,
				Tokens: cache.Tokens{
					Input:  usage.InputTokens,
					Output: usage.OutputTokens,
					Cached: usage.CachedInputTokens,
				},
			},
		})
	}
	if format == cache.FormatPlainText {
		if err := emitPlainText(cmd, content); err != nil {
			return err
		}
	} else if err := renderResult(cmd, content, outputLoaded.Content, findings, meta); err != nil {
		return err
	}
	handleCopyFlag(cmd, app, findings)
	return applyFailOn(cmd, app, findings)
}

// emitPlainText streams a CLI-provider's already-formatted output to
// stdout verbatim. We don't run it through the cards renderer or
// glamour — the host CLI's output is the final form the user wants
// to see, and double-rendering would just re-flow the formatting we
// asked the model to produce.
func emitPlainText(cmd *cobra.Command, content string) error {
	w := cmd.OutOrStdout()
	if _, err := fmt.Fprint(w, content); err != nil {
		return fmt.Errorf("emit plain-text: %w", err)
	}
	// Ensure a trailing newline so the shell prompt lands on a fresh
	// line. CLIs sometimes omit it; defensive but cheap.
	if len(content) == 0 || content[len(content)-1] != '\n' {
		_, _ = fmt.Fprintln(w)
	}
	return nil
}

// handleCopyFlag pushes a plain-text summary of findings onto the
// system clipboard when --copy is set. Silently no-ops when the flag
// is off, there are no findings to copy, or both transports fail
// (e.g. headless Linux without wl-clipboard / xclip / xsel installed
// AND a terminal that ignores OSC 52). Errors here never fail the
// review — clipboard is a convenience, not a hard requirement.
//
// The OSC 52 escape is written to stderr so it never leaks into a
// piped stdout (`commitbrief --json --copy | jq` stays clean). The
// hint line goes through the same stderr stream and honours
// --quiet via infof.
func handleCopyFlag(cmd *cobra.Command, app *appContext, findings []render.Finding) {
	if !global.copy {
		return
	}
	w := cmd.ErrOrStderr()
	hint := func(key string, args ...any) {
		if global.quiet {
			return
		}
		_, _ = fmt.Fprintln(w, app.Catalog.T(key, args...))
	}
	if len(findings) == 0 {
		hint("clipboard.empty")
		return
	}
	payload := render.BuildCopyPayload(findings)
	// OSC 52 escape always goes through w (cmd.ErrOrStderr), even under
	// --quiet — it's a terminal side-channel, not an info message, and
	// suppressing it would silently break the headline feature.
	result := clipboard.Copy(w, payload)
	label := result.MethodLabel()
	if label == "" {
		hint("clipboard.failed")
		return
	}
	hint("clipboard.copied", len(findings), label)
}

// tryStructuredReview runs Review and, on parse failure, retries once.
// Returns (content, totalUsage, format, err). format is FormatJSON when
// either the first or retry response parses cleanly; FormatMarkdownFallback
// when both attempts fail (the caller emits the user warning and stores
// the marker in cache so replays stay silent).
//
// Token usage is summed across both attempts so the verbose footer / cost
// reflects what the user actually spent, even on a graceful degrade.
//
// onRetry, if non-nil, fires after the first attempt parses-fails but
// before the retry call goes out. The progress UI uses it to flip the
// "Thinking..." stage to a soft (neutral) state and start a fresh
// "Retrying..." stage so the user sees what happened.
func tryStructuredReview(
	ctx context.Context,
	prov provider.Provider,
	req provider.Request,
	onRetry func(),
) (string, provider.Usage, string, error) {
	resp, err := prov.Review(ctx, req)
	if err != nil {
		return "", provider.Usage{}, "", err
	}
	if _, parseErr := render.ParseFindings(resp.Content); parseErr == nil {
		return resp.Content, resp.Usage, cache.FormatJSON, nil
	}
	// First attempt unparseable — ADR-0014 §4 retry-once.
	if onRetry != nil {
		onRetry()
	}
	resp2, err2 := prov.Review(ctx, req)
	if err2 != nil {
		// Network/auth failure on retry: surface the first response with
		// the fallback marker; the caller can still render via degrade.
		return resp.Content, resp.Usage, cache.FormatMarkdownFallback, nil
	}
	totalUsage := provider.Usage{
		InputTokens:       resp.Usage.InputTokens + resp2.Usage.InputTokens,
		OutputTokens:      resp.Usage.OutputTokens + resp2.Usage.OutputTokens,
		CachedInputTokens: resp.Usage.CachedInputTokens + resp2.Usage.CachedInputTokens,
	}
	if _, parseErr := render.ParseFindings(resp2.Content); parseErr == nil {
		return resp2.Content, totalUsage, cache.FormatJSON, nil
	}
	// Both attempts produced unparseable output — degrade with first
	// response cached as the canonical fallback content.
	return resp.Content, totalUsage, cache.FormatMarkdownFallback, nil
}

func fetchDiff(repo *git.DispatchRepo, scope reviewScopeFlags, diffArgs []string) (git.Diff, error) {
	if len(diffArgs) > 0 {
		return repo.Diff(diffArgs)
	}
	if scope.unstaged {
		return repo.UnstagedDiff()
	}
	return repo.StagedDiff()
}

func buildMatcher(repoRoot string) *ignore.Matcher {
	builtin := ignore.Builtin()
	if repoRoot == "" {
		return builtin
	}
	repoIgnore, _ := ignore.ParseFile(filepath.Join(repoRoot, ignore.Filename))
	return ignore.Compose(builtin, repoIgnore)
}

func openCache(repoRoot string) (*cache.Cache, error) {
	if repoRoot == "" {
		return nil, errors.New("no repo root")
	}
	return cache.Open(cache.Options{
		Dir:      filepath.Join(repoRoot, ".commitbrief", "cache"),
		RepoRoot: repoRoot,
	})
}

func renderResult(cmd *cobra.Command, content, outputTemplate string, findings []render.Finding, meta render.Meta) error {
	// Findings is pre-resolved by the caller — fresh-call retries and
	// cache-hit format markers are handled there so renderResult never
	// emits the malformed-JSON warning twice for the same review.
	payload := render.Payload{
		Content:        content,
		Findings:       findings,
		OutputTemplate: outputTemplate,
		Meta:           meta,
		Verbose:        global.verbose,
		Compact:        global.compact,
	}

	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()

	switch {
	case global.json:
		return render.JSON(w, payload)
	case global.markdown:
		return render.Markdown(w, payload)
	}
	if ui.ColorEnabled(w, ui.ParseColorMode(global.color)) {
		// Cards is the default rich TTY layout: glamour-rendered body
		// framed by a styled header, status line, and footer (Phase 11
		// Stage A). Terminal (glamour-only, no frame) stays exported for
		// callers that want a thinner render but is no longer the default.
		return render.Cards(w, payload)
	}
	return render.Markdown(w, payload)
}

func openOutput(cmd *cobra.Command) (io.Writer, func(), error) {
	if global.output == "" {
		return cmd.OutOrStdout(), func() {}, nil
	}
	f, err := os.Create(global.output)
	if err != nil {
		return nil, nil, fmt.Errorf("open --output: %w", err)
	}
	return f, func() { _ = f.Close() }, nil
}

// handleCostPreflight surfaces the estimated cost when it exceeds the
// configured threshold and asks the user to confirm. Returns true when
// the caller should abort the review. Below-threshold and disabled-
// (threshold <= 0) cases short-circuit silently — preflight should be
// invisible until it actually has something to say. --yes bypasses
// the prompt but still emits a one-line "bypassed by --yes" notice so
// the user knows a charge was about to surface.
func handleCostPreflight(cmd *cobra.Command, app *appContext, estCost float64) bool {
	threshold := app.Config.Cost.WarnThresholdUSD
	if threshold <= 0 || estCost <= threshold {
		return false
	}

	w := cmd.ErrOrStderr()
	if global.yes {
		_, _ = fmt.Fprintln(w, app.Catalog.T("cost.bypassed_yes", estCost))
		return false
	}

	_, _ = fmt.Fprintln(w, app.Catalog.T("cost.estimate", estCost, threshold))
	if !ui.IsStdinTTY(os.Stdin) {
		_, _ = fmt.Fprintln(w, app.Catalog.T("cost.aborted_non_interactive"))
		return true
	}

	_, _ = fmt.Fprint(w, app.Catalog.T("cost.confirm_prompt"))
	reader := bufio.NewScanner(os.Stdin)
	if !reader.Scan() {
		return true
	}
	answer := strings.TrimSpace(strings.ToLower(reader.Text()))
	if answer == "y" || answer == "yes" {
		return false
	}
	return true
}

// estimateOutputTokens is a conservative-on-the-high-side guess for
// how many output tokens a review request will burn. Underestimating
// output dramatically undercounts cost (Anthropic Opus outputs 5x the
// price of inputs); we cap at 1500 (typical structured-finding review
// is 200-1500 tokens) and floor at 200 so very small diffs still
// surface their output cost honestly. Tuning this is preferable to
// adding another config knob — users who care about exactness should
// lower their threshold rather than reach for the heuristic.
func estimateOutputTokens(inputTokens int) int {
	out := inputTokens / 4
	const minOut, maxOut = 200, 1500
	if out > maxOut {
		out = maxOut
	}
	if out < minOut {
		out = minOut
	}
	return out
}

// handleSecretMatches formats the pre-send secret-scan warnings and
// drives the y/N prompt (or non-interactive abort). Returns true when
// the caller should abort the review. The matched substring is *never*
// printed — only line numbers and pattern names — to keep the secret
// out of stderr and any captured CI log.
func handleSecretMatches(cmd *cobra.Command, app *appContext, matches []guard.SecretMatch) bool {
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.detected", len(matches)))
	for _, m := range matches {
		_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.line", m.Line, strings.Join(m.Patterns, ", ")))
	}
	_, _ = fmt.Fprintln(w)

	if global.yes {
		_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.bypassed_yes"))
		return false
	}
	if !ui.IsStdinTTY(os.Stdin) {
		_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.aborted_non_interactive"))
		return true
	}

	_, _ = fmt.Fprint(w, app.Catalog.T("guard.secrets.prompt"))
	reader := bufio.NewScanner(os.Stdin)
	if !reader.Scan() {
		return true
	}
	answer := strings.TrimSpace(strings.ToLower(reader.Text()))
	if answer == "y" || answer == "yes" {
		return false
	}
	return true
}
