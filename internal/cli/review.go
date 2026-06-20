// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/clipboard"
	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/flaky"
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

	// Validate --min-severity up front so a typo fails fast instead of
	// silently showing every finding after a paid provider round-trip.
	if _, _, err := parseMinSeverity(global.minSeverity); err != nil {
		return err
	}

	// --suggest-commit (ADR-0015) is staged-only and conflicts with the
	// structured / file-output flags. Validate up front so a misuse fails
	// before any provider call.
	if global.suggestCommit {
		if scope.unstaged || len(diffArgs) > 0 {
			return errors.New(app.Catalog.T("commit.suggest_staged_only"))
		}
		if global.json || global.markdown || global.output != "" {
			return errors.New(app.Catalog.T("commit.suggest_output_conflict"))
		}
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
	parsed, err = diff.KeepPaths(parsed, global.files, global.dirs)
	if err != nil {
		err = errors.New(app.Catalog.T("filter.glob.invalid", err.Error()))
		prog.Fail(err)
		return err
	}
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

	// UC-21: one bufio.Reader for the entire review's interactive
	// surface. Guard, secret scanner, and cost preflight all read
	// from the same buffer so a piped-in `e\ne\ne\n` reaches every
	// prompt instead of being eaten by whichever scanner asked
	// first. Concrete *bufio.Reader (not io.Reader) so guard can
	// type-assert and skip its internal wrapping.
	stdinReader := bufio.NewReader(os.Stdin)

	// Guard + secret scan can prompt interactively. Pause the animation
	// so the prompt has a clean canvas; Resume redraws the tree below
	// the prompt afterwards. Both are typically silent (no-op) on a
	// clean diff, so the brief flicker is rare in practice.
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

	// Pre-send secret scan (ADR-0007 follow-up, v0.8.0). Looks for
	// credential-shaped patterns in the *added* diff lines AND in any
	// user-authored rules content (COMMITBRIEF.md / output template)
	// before any LLM call. Off by setting guard.secret_scan=false in
	// config; user-bypassable per-invocation via --allow-secrets only.
	// --yes deliberately does NOT bypass — users wire --yes into CI
	// to skip the guard prompt and we don't want that to also silently
	// nuke the secret scanner.
	//
	// UC-05: rules content joins the system prompt verbatim, so any
	// credential pasted into a user-overridden rules file would leak
	// to the provider just as surely as one pasted into a diff. The
	// embedded defaults are presumed-clean and skipped.
	if app.Config.Guard.SecretScan && !global.allowSecrets {
		// User-extensible patterns (ADR-0024): compile the configured
		// extras once and merge them with the built-ins for this scan.
		// Built-ins always run regardless; a bad regex fails the review
		// here (before any provider call) with the offending pattern named.
		extra, compileErr := guard.CompileUserPatterns(toUserSecretPatterns(app.Config.Guard.SecretPatterns))
		if compileErr != nil {
			prog.Resume()
			return errors.New(app.Catalog.T("guard.secret_patterns.invalid", compileErr.Error()))
		}
		var matches []guard.SecretMatch
		matches = append(matches, guard.ScanForSecretsWith(diffText, extra)...)
		if loaded.Source != rules.SourceDefault {
			matches = append(matches, guard.ScanTextWith(loaded.Content, extra)...)
		}
		if outputLoaded.Source != rules.SourceDefault {
			matches = append(matches, guard.ScanTextWith(outputLoaded.Content, extra)...)
		}
		if len(matches) > 0 {
			if abort := handleSecretMatches(cmd, app, matches, stdinReader); abort {
				return errors.New(app.Catalog.T("guard.secrets.aborted_user"))
			}
		}
	}

	// Prompt-injection scan of user rules (ADR-0025). A non-default
	// COMMITBRIEF.md / OUTPUT.md is the user's own file and joins the
	// system prompt; if it contains injection-shaped phrasing ("ignore
	// previous instructions", "you are now", ...) we WARN — never abort.
	// This is defense-in-depth visibility next to the passive
	// <project_rules> immutability wrap (internal/prompt/build.go); the
	// trusted embedded defaults are skipped. On by default
	// (guard.injection_scan); the warning is non-blocking, so it does not
	// share the secret scanner's prompt machinery.
	if app.Config.Guard.InjectionScan {
		if loaded.Source != rules.SourceDefault {
			warnInjection(cmd, app, rules.Filename, guard.ScanForInjection(loaded.Content))
		}
		if outputLoaded.Source != rules.SourceDefault {
			warnInjection(cmd, app, rules.OutputFilename, guard.ScanForInjection(outputLoaded.Content))
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
	// --with-context (ADR-0017) only means anything for a CLI-backed
	// provider: an API provider has no filesystem to read, so the flag is
	// inert there. Reject it before any provider call rather than silently
	// ignoring it. Fail-fast: diff fetch above is local/free, so this
	// still fires before the cost preflight and the paid round-trip.
	if global.withContext && !plainText {
		ctxErr := errors.New(app.Catalog.T("context.cli_only"))
		prog.Fail(ctxErr)
		return ctxErr
	}
	// Security caution (ADR-0017): the flag is the user's consent, but
	// surface — on every context run, TTY or not — that the agent may read
	// files beyond the diff (incl. untracked secrets) and that the pre-send
	// secret scan covers the diff only. Not a blocking prompt.
	if global.withContext {
		prog.Info(app.Catalog.T("context.warning"))
	}
	// The model sees the line-numbered diff so it can copy line numbers
	// instead of counting them; the cache key and secret scan keep using
	// the plain diffText (numberedDiff is a deterministic function of it,
	// so the cache identity is unchanged).
	numberedDiff := parsed.NumberedString()
	var p prompt.Prompt
	if plainText {
		p = prompt.BuildPlainText(loaded, app.Lang, numberedDiff, global.withContext)
	} else {
		p = prompt.Build(loaded, app.Lang, numberedDiff)
	}

	// --show-prompt: emit the exact system + user prompt that would be sent
	// and stop here. No provider call, no cache lookup, no cost — a pure
	// transparency inspector that reflects every prompt-shaping flag
	// (scope, --with-context, --cli/--provider, --lang) because it prints
	// the already-assembled prompt. Placed after the guard/secret scan so a
	// secret in what you're about to dump is still surfaced first.
	if global.showPrompt {
		prog.Finish()
		prog.Clear()
		return showPromptOutput(cmd, p)
	}

	// Deterministic flaky-test pre-pass (ADR-0022). Static, provider-free
	// findings from the filtered diff, merged into the structured results in
	// both the cache-hit and fresh paths below. Skipped for plain-text/CLI
	// providers (their output isn't structured) and when disabled via
	// review.flaky=false or --no-flaky.
	var flakyFindings []render.Finding
	if app.Config.Review.Flaky && !global.noFlaky && !plainText {
		flakyFindings = flaky.New(app.Catalog).Detect(parsed)
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
		WithContext:  global.withContext,
	})

	cacheStore, err := openCache(app.RepoRoot, app.Config.Cache)
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
				Cost:         resolvePricing(app.Config, prov, model).Cost(usage),
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
			findings = mergeFlaky(findings, flakyFindings)
			// Signal-control stage (ADR-0027): baseline + inline suppression
			// are TRUE removals applied before fail-on / render, even on a
			// cache hit (the cached body is the provider response; what the
			// developer chose to baseline/suppress still applies this run).
			var baselined, suppressed int
			findings, baselined, suppressed, err = applyActionableFilters(cmd, app, parsed, findings)
			if err != nil {
				return err
			}
			meta.Baselined, meta.Suppressed = baselined, suppressed
			if entry.Result.Format == cache.FormatPlainText {
				// CLI-emitted output: stream the cached body verbatim to
				// stdout instead of going through the cards renderer.
				if err := emitPlainText(cmd, entry.Result.Content); err != nil {
					return err
				}
			} else if err := renderResult(cmd, entry.Result.Content, outputLoaded.Content, findings, meta); err != nil {
				return err
			}
			signalControlFooter(cmd, app, baselined, suppressed)
			if err := suggestCommitMessage(ctx, cmd, app, prov, model, diffText); err != nil {
				return err
			}
			handleCopyFlag(cmd, app, findings)
			return applyFailOn(cmd, app, findings)
		}
	}

	prog.Finish() // Preparing → done

	// Token preflight (ADR-0003, opt-in via guard.token_preflight). We're
	// past the cache lookup and about to spend tokens; if the estimated
	// prompt overflows the provider's context window, catch it here with a
	// friendly confirm/abort instead of letting the provider reject it with
	// a raw 400. Off by default — the estimate is a chars/4 heuristic and a
	// false positive shouldn't block a review nobody asked to guard.
	if app.Config.Guard.TokenPreflight {
		prog.Pause()
		if abort := handleTokenPreflight(cmd, app, prov, p, model, stdinReader); abort {
			return errors.New(app.Catalog.T("guard.tokens.aborted_user"))
		}
		prog.Resume()
	}

	// Cost preflight (11.5.6): we're past the cache lookup and about to
	// spend real tokens. If the estimated cost exceeds the configured
	// threshold, prompt the user (TTY) or abort (non-TTY). The only
	// bypass is --no-cost-check (or raising cost.warn_threshold_usd in
	// config); --yes deliberately does NOT bypass — see the rationale
	// on handleCostPreflight. Pause the progress animation around the
	// prompt so the user sees a clean y/N question.
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

	prog.Start(app.Catalog.T("progress.thinking"))
	start := time.Now()
	req := provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
		// --with-context (ADR-0017): inert for API providers (they ignore
		// ProviderOpts); the clireview backend reads it to grant read tools
		// and run in the repo root. Only meaningful when plainText is true,
		// which the validation above already guaranteed for withContext.
		ProviderOpts: provider.ContextOptions{
			Enabled:  global.withContext,
			RepoRoot: app.RepoRoot,
		},
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
	findings = mergeFlaky(findings, flakyFindings)
	// Signal-control stage (ADR-0027): baseline + inline suppression are TRUE
	// removals (fail-on + JSON findings[] + display), inserted after the
	// findings are assembled and before applyFailOn / render — distinct from
	// the display-only filterMinSeverity. --update-baseline short-circuits
	// here to absorb the current findings instead of filtering this run.
	findings, baselined, suppressed, err := applyActionableFilters(cmd, app, parsed, findings)
	if err != nil {
		return err
	}

	respModel := model
	meta := render.Meta{
		Provider:     prov.Name(),
		Model:        respModel,
		Lang:         app.Lang.Code,
		Usage:        usage,
		Cost:         resolvePricing(app.Config, prov, respModel).Cost(usage),
		Latency:      latency,
		Timestamp:    time.Now().UTC(),
		Files:        parsed.FileCount(),
		LinesAdded:   parsed.AddedLines(),
		LinesRemoved: parsed.DeletedLines(),
		RulesLoaded:  loaded.Source != rules.SourceDefault,
		Baselined:    baselined,
		Suppressed:   suppressed,
	}

	if !global.noCache && cacheStore != nil {
		// UC-26: persist real SHA-256 hashes of the diff text and the
		// system prompt so the cache entry's KeyMeta is actually
		// debuggable (matches what docs/03-configuration.md advertises).
		// Pre-v1.0 this stored the first 16 hex chars of the composite
		// cache key for DiffHash and an empty string for
		// SystemPromptHash — neither was the value the field name
		// promised.
		diffSum := sha256.Sum256([]byte(diffText))
		promptSum := sha256.Sum256([]byte(p.System))
		_ = cacheStore.Put(cacheKey, cache.Entry{
			Key: cache.KeyMeta{
				DiffHash:         "sha256:" + hex.EncodeToString(diffSum[:]),
				SystemPromptHash: "sha256:" + hex.EncodeToString(promptSum[:]),
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
	signalControlFooter(cmd, app, baselined, suppressed)
	if err := suggestCommitMessage(ctx, cmd, app, prov, model, diffText); err != nil {
		return err
	}
	handleCopyFlag(cmd, app, findings)
	return applyFailOn(cmd, app, findings)
}

// mergeFlaky appends deterministic flaky-test findings (ADR-0022) to the LLM
// findings, skipping any whose (file, line) the LLM already reported so the
// same line isn't surfaced twice. Every LLM finding is preserved; flaky
// findings are additive. Returns llm unchanged when there are no flaky
// findings — the common case, and the plain-text/CLI path where the detector
// never ran.
func mergeFlaky(llm, flakyFindings []render.Finding) []render.Finding {
	if len(flakyFindings) == 0 {
		return llm
	}
	seen := make(map[string]struct{}, len(llm))
	for _, f := range llm {
		seen[f.File+":"+strconv.Itoa(f.Line)] = struct{}{}
	}
	out := make([]render.Finding, 0, len(llm)+len(flakyFindings))
	out = append(out, llm...)
	for _, f := range flakyFindings {
		if _, dup := seen[f.File+":"+strconv.Itoa(f.Line)]; dup {
			continue
		}
		out = append(out, f)
	}
	return out
}

// emitPlainText streams a CLI-provider's already-formatted output
// verbatim. We don't run it through the cards renderer or glamour —
// the host CLI's output is the final form the user wants to see, and
// double-rendering would just re-flow the formatting we asked the
// model to produce.
//
// UC-07: honour --output the same way the structured renderers do.
// Pre-v0.9.2 the CLI-provider path wrote straight to stdout even
// when --output X was set, which silently dropped the request.
func emitPlainText(cmd *cobra.Command, content string) error {
	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()
	if _, err := fmt.Fprint(w, wrapPlainText(content)); err != nil {
		return fmt.Errorf("emit plain-text: %w", err)
	}
	return nil
}

// plainTextRule mirrors the 20-hyphen separator the CLI-provider prompt
// instructs the model to emit between findings (internal/rules/prompt.go).
const plainTextRule = "--------------------"

// wrapPlainText brackets CLI-provider review output with the finding
// separator on the top and bottom edges too, so the whole block reads as
// one fenced unit when pasted into chat or piped to a file. The model is
// told to emit the rule only BETWEEN findings; the edge rules are added
// here deterministically — and any stray edge rule the model emitted
// anyway is stripped first so the edges never double up. The trailing
// blank line keeps the next shell prompt off the bottom rule.
func wrapPlainText(content string) string {
	body := strings.TrimSpace(content)
	if body == "" {
		return "\n"
	}
	body = strings.TrimSpace(strings.TrimPrefix(body, plainTextRule))
	body = strings.TrimSpace(strings.TrimSuffix(body, plainTextRule))
	return plainTextRule + "\n\n" + body + "\n\n" + plainTextRule + "\n\n"
}

// showPromptOutput writes the assembled system + user prompt to the output
// sink (stdout, or --output FILE) and is the terminal step of a
// --show-prompt run. The markers are deliberately fixed English (debug-
// grade, like the dry-run columns) so the dump is greppable and stable
// regardless of --lang. No trailing provider call happens after this.
func showPromptOutput(cmd *cobra.Command, p prompt.Prompt) error {
	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()
	if _, err := fmt.Fprintf(w, "===== SYSTEM PROMPT =====\n%s\n\n===== USER PROMPT =====\n%s\n", p.System, p.User); err != nil {
		return fmt.Errorf("show-prompt: write: %w", err)
	}
	return nil
}

// handleTokenPreflight warns when the estimated prompt overflows the
// provider's context window and asks the user to confirm. Returns true when
// the caller should abort. Only reached when guard.token_preflight is on.
// Mirrors handleCostPreflight: confirm on a TTY, abort non-interactively.
// The estimate is the chars/4 heuristic shared with dry-run; a provider
// that reports a non-positive window (ExceedsContext guards this) never
// triggers the prompt.
func handleTokenPreflight(cmd *cobra.Command, app *appContext, prov provider.Provider, p prompt.Prompt, model string, stdin *bufio.Reader) bool {
	window := prov.ContextWindow(model)
	if !p.ExceedsContext(window) {
		return false
	}

	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(w, app.Catalog.T("guard.tokens.exceeds", p.EstimatedTokens(), prov.Name(), window))
	if !ui.IsStdinTTY(os.Stdin) {
		_, _ = fmt.Fprintln(w, app.Catalog.T("guard.tokens.aborted_non_interactive"))
		return true
	}

	// Interactive is hardcoded true (not IsStdinTTY) because the abort
	// above already guarantees a TTY here. Do NOT soften it to
	// IsStdinTTY: that routes the non-TTY case through ui.Confirm's
	// line-based fallback, but non-TTY must abort (handled above),
	// never line-read a piped answer.
	ok, err := ui.Confirm(stdin, w, app.Catalog.T("guard.tokens.confirm_prompt"),
		ui.AskOptions{Interactive: true, Catalog: app.Catalog})
	return err != nil || !ok
}

// suggestCommitMessage runs a second, free-form provider call (ADR-0015)
// to produce a Conventional Commit message for the staged diff and prints
// it to stdout after the review. No-op unless --suggest-commit is set.
//
// The suggestion is NOT cached and skips the cost preflight: the review
// (the expensive call) is already cached and preflighted, and the
// commit-message prompt is small. Caching the suggestion is a follow-up.
// Works for every provider — FreeForm makes API providers return plain
// text, and PlainTextEmitter providers already do.
func suggestCommitMessage(ctx context.Context, cmd *cobra.Command, app *appContext, prov provider.Provider, model, diffText string) error {
	if !global.suggestCommit {
		return nil
	}
	// --suggest-commit keeps its original shape: one Conventional Commit
	// message. The richer --type / --generate surface lives on the
	// standalone `commit` command (ADR-0019).
	p := prompt.BuildCommitMessage(diffText, prompt.CommitOptions{
		Type:  prompt.CommitConventional,
		Count: 1,
	})
	resp, err := prov.Review(ctx, provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
		FreeForm:     true,
	})
	if err != nil {
		return fmt.Errorf("suggest-commit: %w", err)
	}
	block := "\n" + app.Catalog.T("commit.suggested_header") + "\n\n" +
		strings.TrimSpace(resp.Content) + "\n\n"
	if _, err := io.WriteString(cmd.OutOrStdout(), block); err != nil {
		return fmt.Errorf("suggest-commit: write: %w", err)
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
	// Copy honours --min-severity too — it's a display/share surface, so
	// the user only shares what they chose to see.
	findings = filterMinSeverity(findings)
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

// openCache resolves the on-disk store for the active repo, applying
// the cache.* knobs from config. UC-02 in PATCH_ROADMAP:
//   - cache.enabled=false now shortcircuits to (nil, nil) — the review
//     pipeline treats a nil store as "skip lookup + write" without
//     surfacing it as an error.
//   - cache.ttl_days is passed through as cache.Options.TTL; zero/unset
//     falls back to cache.DefaultTTL inside Open.
//
// `--no-cache` still wins above this (it's checked before each Get/Put
// call site); this function only governs whether a store exists at all.
func openCache(repoRoot string, cfg config.CacheConfig) (*cache.Cache, error) {
	if repoRoot == "" {
		return nil, errors.New("no repo root")
	}
	if !cfg.Enabled {
		return nil, nil
	}
	var ttl time.Duration
	if cfg.TTLDays > 0 {
		ttl = time.Duration(cfg.TTLDays) * 24 * time.Hour
	}
	// cache.max_size_mb bounds the on-disk cache (ADR-0008 size-bounded
	// eviction); <=0 means unlimited. MiB so a "50" in config matches the
	// human-readable byte formatting used by cache stats / clear / prune.
	var maxBytes int64
	if cfg.MaxSizeMB > 0 {
		maxBytes = int64(cfg.MaxSizeMB) * 1024 * 1024
	}
	return cache.Open(cache.Options{
		Dir:          filepath.Join(repoRoot, ".commitbrief", "cache"),
		RepoRoot:     repoRoot,
		TTL:          ttl,
		MaxSizeBytes: maxBytes,
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

	// JSON is machine output: emit it exactly (full, unfiltered) with no
	// trailing blank line. --min-severity is a display-only filter and
	// must not touch the machine contract.
	if global.json {
		return render.JSON(w, payload)
	}

	// Human render paths honour --min-severity (Cards / Markdown).
	payload.Findings = filterMinSeverity(payload.Findings)

	var rerr error
	switch {
	case global.markdown:
		rerr = render.Markdown(w, payload)
	case ui.ColorEnabled(w, ui.ParseColorMode(global.color)):
		// Cards is the default rich TTY layout: glamour-rendered body
		// framed by a styled header, status line, and footer (Phase 11
		// Stage A). Terminal (glamour-only, no frame) stays exported for
		// callers that want a thinner render but is no longer the default.
		rerr = render.Cards(w, payload)
	default:
		rerr = render.Markdown(w, payload)
	}
	if rerr != nil {
		return rerr
	}
	// Trailing blank line after the final output line so the result is
	// visually separated from the next shell prompt.
	_, err = io.WriteString(w, "\n")
	return err
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
// invisible until it actually has something to say. The only opt-out
// is --no-cost-check (or raising cost.warn_threshold_usd in config);
// --yes deliberately does NOT bypass this, because users routinely set
// --yes in CI to skip the guard prompt and we don't want that to also
// silently approve unbounded spend.
func handleCostPreflight(cmd *cobra.Command, app *appContext, estCost float64, stdin *bufio.Reader) bool {
	threshold := app.Config.Cost.WarnThresholdUSD
	if threshold <= 0 || estCost <= threshold {
		return false
	}

	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(w, app.Catalog.T("cost.estimate", estCost, threshold))
	if !ui.IsStdinTTY(os.Stdin) {
		_, _ = fmt.Fprintln(w, app.Catalog.T("cost.aborted_non_interactive"))
		return true
	}

	// Interactive is hardcoded true (not IsStdinTTY): the abort above
	// guarantees a TTY here. Do NOT soften it to IsStdinTTY — that would
	// route non-TTY through the line fallback and let piped input
	// auto-approve spend, reopening the hole UC-06 closed (non-TTY must
	// abort, --yes must not bypass; PRD §310).
	ok, err := ui.Confirm(stdin, w, app.Catalog.T("cost.confirm_prompt"),
		ui.AskOptions{Interactive: true, Catalog: app.Catalog})
	return err != nil || !ok
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

// toUserSecretPatterns adapts the config-layer SecretPatternConfig slice
// into the guard package's UserSecretPattern input shape (ADR-0024). The
// guard package stays a leaf — it never imports config — so the CLI does
// the field-for-field copy here. A nil/empty input yields nil.
func toUserSecretPatterns(specs []config.SecretPatternConfig) []guard.UserSecretPattern {
	if len(specs) == 0 {
		return nil
	}
	out := make([]guard.UserSecretPattern, len(specs))
	for i, s := range specs {
		out[i] = guard.UserSecretPattern{Name: s.Name, Regex: s.Regex}
	}
	return out
}

// warnInjection prints a single non-blocking warning to stderr when the
// prompt-injection scan (ADR-0025) flags one or more lines in a
// user-authored rules file, then returns — it NEVER aborts the review,
// because the file is the user's own. No-op on a clean scan (the common
// case). Only the file name and 1-based line numbers are printed; the
// matched text is never echoed (InjectionMatch carries labels + lines
// only). Honours --quiet via infof's underlying stream is intentionally
// NOT used here: this is a security advisory, so it always prints (like
// the --with-context caution), but it is a warning, not an error.
func warnInjection(cmd *cobra.Command, app *appContext, file string, matches []guard.InjectionMatch) {
	if len(matches) == 0 {
		return
	}
	lineStrs := make([]string, 0, len(matches))
	for _, l := range guard.InjectionLines(matches) {
		lineStrs = append(lineStrs, strconv.Itoa(l))
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(),
		app.Catalog.T("guard.injection.warning", file, strings.Join(lineStrs, ", ")))
}

// handleSecretMatches formats the pre-send secret-scan warnings and
// drives the y/N prompt (or non-interactive abort). Returns true when
// the caller should abort the review. The matched substring is *never*
// printed — only line numbers and pattern names — to keep the secret
// out of stderr and any captured CI log.
func handleSecretMatches(cmd *cobra.Command, app *appContext, matches []guard.SecretMatch, stdin *bufio.Reader) bool {
	w := cmd.ErrOrStderr()
	_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.detected", len(matches)))
	for _, m := range matches {
		_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.line", m.Line, strings.Join(m.Patterns, ", ")))
	}
	_, _ = fmt.Fprintln(w)

	if !ui.IsStdinTTY(os.Stdin) {
		_, _ = fmt.Fprintln(w, app.Catalog.T("guard.secrets.aborted_non_interactive"))
		return true
	}

	// Interactive is hardcoded true (not IsStdinTTY): the abort above
	// guarantees a TTY here. Do NOT soften it to IsStdinTTY — that would
	// route non-TTY through the line fallback and let piped input
	// auto-confirm a detected secret, reopening the hole UC-01 closed
	// (non-TTY must abort, --yes must not bypass; PRD §309).
	ok, err := ui.Confirm(stdin, w, app.Catalog.T("guard.secrets.prompt"),
		ui.AskOptions{Interactive: true, Catalog: app.Catalog})
	return err != nil || !ok
}
