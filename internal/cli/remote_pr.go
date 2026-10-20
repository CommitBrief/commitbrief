// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/remote"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/suppress"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

type remotePRFlags struct {
	requestChangesOn string
	repo             string
	noPost           bool
	sinceLastReview  bool
}

func newRemotePRCmd() *cobra.Command {
	var f remotePRFlags
	cmd := &cobra.Command{
		Use:   "pr <PR-ID>",
		Short: "Review a GitHub pull request and post findings as inline comments",
		// Help text intentionally avoids raw <angle-bracket> placeholders: the
		// man-page generator runs Long/Use through a markdown→roff converter
		// that drops them as if they were HTML tags. The PR-ID/severity are
		// described in prose instead.
		Long: "Pulls a PR's diff via `gh`, runs the review pipeline, posts each\n" +
			"finding as an inline comment, and submits a verdict. By default the\n" +
			"verdict is only approve (no findings / info-only) or comment; a\n" +
			"request-changes verdict is opt-in via the --request-changes-on flag.\n" +
			"The PR ID accepts gh-native forms: 42, owner/repo#42, or a full URL.\n" +
			"With --since-last-review only the commits added since CommitBrief's\n" +
			"previous review of the PR are reviewed; it falls back to a full review\n" +
			"whenever that range cannot be determined safely. See ADR-0016.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemotePR(cmd, args[0], f, remote.NewRunner())
		},
	}
	cmd.Flags().StringVar(&f.requestChangesOn, "request-changes-on", "",
		"opt in to a request-changes verdict at/above this severity (critical|high|medium|low); "+
			"omitted, the verdict is never request-changes (comment / approve only)")
	cmd.Flags().StringVar(&f.repo, "repo", "", "target repository owner/repo (overrides git context)")
	cmd.Flags().BoolVar(&f.noPost, "no-post", false,
		"review the PR diff and print locally (no GitHub writes); enables --json/--markdown/--output/--copy/--cli like a local review")
	cmd.Flags().BoolVar(&f.sinceLastReview, "since-last-review", false,
		"review only the commits added since CommitBrief's last review of this PR (full review when that range is unknown)")
	return cmd
}

var (
	errRequestChangesInfo    = errors.New("request-changes-on: info is not a valid threshold")
	errRequestChangesInvalid = errors.New("request-changes-on: invalid severity")
)

// parseRequestChangesOn validates the --request-changes-on value. `info`
// is rejected (you cannot request-changes on an info-only review);
// anything outside the four actionable levels is invalid.
func parseRequestChangesOn(raw string) (render.Severity, error) {
	s := render.Severity(strings.ToLower(strings.TrimSpace(raw)))
	if s == render.SeverityInfo {
		return "", errRequestChangesInfo
	}
	switch s {
	case render.SeverityCritical, render.SeverityHigh, render.SeverityMedium, render.SeverityLow:
		return s, nil
	}
	return "", errRequestChangesInvalid
}

func runRemotePR(cmd *cobra.Command, prID string, f remotePRFlags, runner remote.Runner) error {
	// The PR diff comes from `gh pr diff`, not from local git, so there is no
	// commit history here to walk. Reject the commit filters up front, on both
	// entry paths, rather than accepting flags that could not be honored.
	if commitFiltersRequested() {
		// pickErrorCatalog, not appContext: this check runs before either
		// path resolves its context, and it must fire the same way whether
		// or not the user is standing in a git repo.
		return errors.New(pickErrorCatalog().T("remote.commit_filter_unsupported", commitFilterFlags))
	}
	// --no-post (ADR-0016 §Update): use the PR diff purely as a review
	// source and render to the terminal like a local review — no GitHub
	// writes (no comments, no verdict), so the local-render and CLI flags
	// apply. Diverges enough from the posting flow to warrant its own path.
	if f.noPost {
		return runRemotePRLocal(cmd, prID, f, runner)
	}

	app, err := resolveContext(false)
	if err != nil {
		return err
	}
	ctx, cancel := app.withTimeout(cmd.Context())
	defer cancel()
	cmd.SetContext(ctx)
	cat := app.Catalog

	// Local-render flags have no meaning here — the output channel is GitHub.
	if global.json || global.markdown || global.output != "" || global.copy || global.compact {
		return errors.New(cat.T("remote.output_flags"))
	}
	if f.repo == "" && app.RepoRoot == "" {
		return errors.New(cat.T("remote.repo_required"))
	}

	// request-changes is opt-in: without --request-changes-on the threshold is
	// empty, and computeVerdict never escalates past comment. Only validate
	// when the user actually set it.
	var threshold render.Severity
	if f.requestChangesOn != "" {
		t, perr := parseRequestChangesOn(f.requestChangesOn)
		if perr != nil {
			if errors.Is(perr, errRequestChangesInfo) {
				return errors.New(cat.T("remote.request_changes_on_info"))
			}
			return errors.New(cat.T("remote.request_changes_on_invalid", f.requestChangesOn))
		}
		threshold = t
	}

	if err := remote.EnsureGH(); err != nil {
		return errors.New(cat.T("remote.gh_missing"))
	}

	prov, err := newProviderWithTimeout(app.Config.Provider, app.Config.Providers[app.Config.Provider], app.Timeout)
	if err != nil {
		return err
	}
	if _, plain := prov.(provider.PlainTextEmitter); plain {
		return errors.New(cat.T("remote.plain_text_provider"))
	}
	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}

	// Standard review header line ("commitbrief vX · provider · cache"),
	// printed above the progress tree exactly as the local review shows
	// it. remote pr does not consult the local cache, so it reports a
	// miss. Honors --quiet.
	if !global.quiet {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), render.HeaderLine(render.Meta{Provider: prov.Name(), Model: model}))
	}

	// Same staged-tree progress display the local review uses, so the
	// remote pipeline reads identically (stages animate on a TTY, degrade
	// to one line per transition in CI). All progress/warning output goes
	// through prog while it is live — a raw stderr write would corrupt the
	// animated redraw.
	prog := ui.NewProgress(cmd.ErrOrStderr(), ui.ParseColorMode(global.color), global.quiet)
	defer prog.Close()
	noteUndeclaredLimits(cmd, app, prov, model, prog, false)

	if global.failOn != "" {
		prog.Info(cat.T("remote.fail_on_ignored"))
	}

	whoami, err := remote.Whoami(ctx, runner)
	if err != nil {
		prog.Fail(err)
		return err
	}

	prog.Start(cat.T("remote.fetching_pr", prID))
	meta, err := remote.FetchPRMeta(ctx, runner, prID, f.repo)
	if err != nil {
		prog.Fail(err)
		return err
	}
	if strings.EqualFold(meta.AuthorLogin(), whoami) {
		err := errors.New(cat.T("remote.self_pr_blocked"))
		prog.Fail(err)
		return err
	}

	loaded, err := rules.Load(app.RepoRoot)
	if err != nil {
		prog.Fail(err)
		return err
	}

	// --since-last-review: find the head CommitBrief last reviewed. Anything
	// that stops us from using it (no marker, unreadable) means a full review
	// plus one info line; an unchanged head means there is nothing to review.
	prev := ""
	if f.sinceLastReview {
		recorded, lerr := remote.FetchLastReviewedOID(ctx, runner, meta.BaseSlug(), meta.Number)
		switch {
		case lerr != nil:
			prog.Info(cat.T("remote.since_lookup_failed", lerr.Error()))
		case recorded == "":
			prog.Info(cat.T("remote.since_no_marker"))
		case recorded == meta.LastOID():
			prog.Info(cat.T("remote.since_no_new_commits"))
			return nil
		default:
			prev = recorded
		}
	}

	res, oid, err := reviewPRDiff(ctx, runner, prID, f, app, prov, model, loaded, meta.LastOID(), meta.BaseSlug(), prev, prog)
	if err != nil {
		prog.Fail(err)
		return err
	}
	if res.noNewCommits {
		prog.Info(cat.T("remote.since_no_new_commits"))
		return nil
	}
	// Unknown-scope marker warnings print once, here, not per attempt of the
	// head-race retry loop. Progress has no warning channel (Info is the
	// tree's only note kind), and a raw stderr write would corrupt the live
	// redraw, so Info is the right sink.
	for _, w := range res.suppressWarnings {
		prog.Info(cat.T(suppressWarningKey(w), w.Path, w.Line, w.Token))
	}

	if err := submitPRReview(ctx, runner, prID, f, meta, oid, res, threshold, whoami, app, prog); err != nil {
		prog.Fail(err)
		return err
	}

	// Standard review footer line ("✓ Done in … · N findings · tokens ·
	// $cost"), printed below the now-finished tree (Close, not Clear, so the
	// tree stays on screen). Honors --quiet.
	prog.Close()
	if !global.quiet {
		footerMeta := render.Meta{
			Provider:    prov.Name(),
			Model:       model,
			Usage:       res.usage,
			Cost:        resolvePricing(app.Config, prov, model).Cost(res.usage),
			CostUnknown: costIsUnknown(app.Config, prov, model),
			Latency:     res.latency,
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), render.FooterLine(footerMeta, res.findings))
	}
	signalControlFooter(cmd, app, 0, res.suppressed)
	return nil
}

// runRemotePRLocal is the `remote pr <ID> --no-post` path: fetch the PR
// diff via gh and run it through the same review+render pipeline a local
// review uses, printing to the terminal instead of posting to GitHub.
// No GitHub writes happen, so the local-render flags (--json/--markdown/
// --output/--copy/--compact), CLI providers (--cli), and --fail-on all
// apply, and there is no self-PR restriction. It mirrors runReview's
// core (cache → cost preflight → call → render); the differences are the
// diff source (gh, not git) and that the secret scanner WARNS rather than
// aborts (you can't fix another author's PR locally, and aborting a
// read-only review is unhelpful), matching the posting path's posture.
func runRemotePRLocal(cmd *cobra.Command, prID string, f remotePRFlags, runner remote.Runner) error {
	app, err := resolveContext(false)
	if err != nil {
		return err
	}
	ctx, cancel := app.withTimeout(cmd.Context())
	defer cancel()
	cmd.SetContext(ctx)
	cat := app.Catalog

	if _, _, err := parseMinSeverity(global.minSeverity); err != nil {
		return err
	}
	if f.repo == "" && app.RepoRoot == "" {
		return errors.New(cat.T("remote.repo_required"))
	}
	// --request-changes-on only drives a GitHub verdict, which --no-post
	// never submits. Note it only when the user explicitly set it.
	if cmd.Flags().Changed("request-changes-on") {
		infof("%s", cat.T("remote.no_post_request_changes_ignored"))
	}
	if f.sinceLastReview {
		infof("%s", cat.T("remote.no_post_since_last_review_ignored"))
	}
	if err := remote.EnsureGH(); err != nil {
		return errors.New(cat.T("remote.gh_missing"))
	}

	prov, err := newProviderWithTimeout(app.Config.Provider, app.Config.Providers[app.Config.Provider], app.Timeout)
	if err != nil {
		return err
	}
	_, plainText := prov.(provider.PlainTextEmitter)
	// --with-context grounds a CLI review in the LOCAL working tree, which
	// need not match the PR's branch — combining it with a remote diff is
	// misleading, so it is not wired here. Note it if the user set it.
	if global.withContext {
		infof("%s", cat.T("remote.no_post_context_ignored"))
	}
	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}

	loaded, err := rules.Load(app.RepoRoot)
	if err != nil {
		return err
	}
	if loaded.Source == rules.SourceDefault {
		infof("%s", cat.T("rules.using_default"))
	}
	outputLoaded, err := rules.LoadOutput(app.RepoRoot, userHome())
	if err != nil {
		return err
	}
	if outputLoaded.Source == rules.SourceDefault {
		infof("%s", cat.T("rules.output.using_default"))
	} else if verr := render.ValidateOutputTemplate(outputLoaded.Content); verr != nil {
		return errors.New(cat.T("output.template.invalid", outputLoaded.Path, verr.Error()))
	}

	if !global.quiet {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), render.HeaderLine(render.Meta{Provider: prov.Name(), Model: model}))
	}
	prog := ui.NewProgress(cmd.ErrOrStderr(), ui.ParseColorMode(global.color), global.quiet)
	defer prog.Close()
	noteUndeclaredLimits(cmd, app, prov, model, prog, false)

	prog.Start(cat.T("remote.fetching_pr", prID))
	rawDiff, err := remote.FetchDiff(ctx, runner, prID, f.repo)
	if err != nil {
		prog.Fail(err)
		return err
	}
	parsed, err := diff.Parse(git.Diff{Content: rawDiff, Origin: git.OriginDiff})
	if err != nil {
		prog.Fail(err)
		return err
	}
	parsed = diff.Filter(parsed, buildMatcher(app.RepoRoot))
	parsed, err = keepAndDropPaths(parsed)
	if err != nil {
		err = errors.New(cat.T("filter.glob.invalid", err.Error()))
		prog.Fail(err)
		return err
	}
	if parsed.Empty() {
		prog.Finish()
		prog.Close()
		infof("%s", cat.T("review.no_changes"))
		return nil
	}
	prog.Info(render.StatusLine(render.Meta{
		Files:        parsed.FileCount(),
		LinesAdded:   parsed.AddedLines(),
		LinesRemoved: parsed.DeletedLines(),
		RulesLoaded:  loaded.Source != rules.SourceDefault,
	}))
	diffText := parsed.String()
	numbered := parsed.NumberedString()

	// Secret scanner warns (does not abort) — the diff is a remote PR's.
	if app.Config.Guard.SecretScan && !global.allowSecrets {
		if hits := guard.ScanForSecrets(diffText); len(hits) > 0 {
			prog.Info(cat.T("remote.secret_warn", len(hits)))
		}
	}

	// archContext "" — architecture-aware review (ADR-0030) is scoped to the
	// local review/dry-run paths for now; the remote PR path keeps its prompt
	// (and cache identity) unchanged. Extending it here is a future follow-up.
	var p prompt.Prompt
	if plainText {
		p = prompt.BuildPlainText(loaded, app.Lang, numbered, "", false)
	} else {
		p = prompt.Build(loaded, app.Lang, numbered, "")
	}

	cacheKey := cache.Compute(cache.ComputeArgs{
		Diff:         diffText,
		SystemPrompt: p.System,
		Provider:     prov.Name(),
		Model:        model,
		Lang:         app.Lang.Code,
	})
	cacheStore, cerr := openCache(app.RepoRoot, app.Config.Cache)
	if cerr != nil {
		infof("%s", cat.T("review.cache_disabled", cerr))
	}

	if !global.noCache && cacheStore != nil {
		if entry, hit := cacheStore.Get(cacheKey); hit {
			prog.Finish()
			prog.Clear()
			usage := provider.Usage{
				InputTokens:       entry.Result.Tokens.Input,
				OutputTokens:      entry.Result.Tokens.Output,
				CachedInputTokens: entry.Result.Tokens.Cached,
			}
			meta := render.Meta{
				Provider:     prov.Name(),
				Model:        model,
				Lang:         app.Lang.Code,
				Cached:       true,
				Timestamp:    entry.CreatedAt,
				Usage:        usage,
				Cost:         resolvePricing(app.Config, prov, model).Cost(usage),
				CostUnknown:  costIsUnknown(app.Config, prov, model),
				Files:        parsed.FileCount(),
				LinesAdded:   parsed.AddedLines(),
				LinesRemoved: parsed.DeletedLines(),
				RulesLoaded:  loaded.Source != rules.SourceDefault,
				// Same stamp as a local review (ADR-0031 §Update 2026-10-20).
				DegradeReason: cachedDegradeReason(entry.Result),
			}
			var findings []render.Finding
			switch entry.Result.Format {
			case cache.FormatJSON, "":
				findings, _ = render.ParseFindings(entry.Result.Content)
			}
			findings = stampIdentity(parsed, findings)
			// Inline suppression is a TRUE removal here too (same helper as
			// the posting path), so --fail-on and --json agree with what
			// `remote pr` would post. The cache holds the raw provider
			// response; what a marker hides is decided per run.
			var supWarnings []suppress.Warning
			findings, meta.Suppressed, supWarnings = suppressPRFindings(parsed, findings)
			for _, w := range supWarnings {
				warnT(cmd, app, suppressWarningKey(w), w.Path, w.Line, w.Token)
			}
			if entry.Result.Format == cache.FormatPlainText {
				if err := emitPlainText(cmd, entry.Result.Content); err != nil {
					return err
				}
			} else if err := renderResult(cmd, entry.Result.Content, outputLoaded.Content, findings, meta); err != nil {
				return err
			}
			signalControlFooter(cmd, app, 0, meta.Suppressed)
			handleCopyFlag(cmd, app, findings)
			return applyFailOn(cmd, app, findings)
		}
	}
	prog.Finish()

	// Cost preflight, same as a local review (no-op for zero-priced CLI
	// providers). --no-cost-check bypasses; --yes deliberately does not.
	// Skipped when the price is unknown (openai-compatible without a declared
	// price): there is no figure to confirm (D3).
	if !global.noCostCheck && !costIsUnknown(app.Config, prov, model) {
		estUsage := provider.Usage{
			InputTokens:  p.EstimatedTokens(),
			OutputTokens: estimateOutputTokens(p.EstimatedTokens()),
		}
		estCost := resolvePricing(app.Config, prov, model).Cost(estUsage)
		prog.Pause()
		if abort := handleCostPreflight(cmd, app, estCost, bufio.NewReader(os.Stdin)); abort {
			return errors.New(cat.T("cost.aborted_user"))
		}
		prog.Resume()
	}

	prog.Start(cat.T("remote.reviewing"))
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
		retries int
		degrade string
	)
	if plainText {
		resp, callErr := prov.Review(ctx, req)
		if callErr != nil {
			callErr = wrapTimeoutErr(ctx, fmt.Errorf("provider %s: %w", prov.Name(), callErr), cat, app.Timeout)
			prog.Fail(callErr)
			return callErr
		}
		content, usage, format = resp.Content, resp.Usage, cache.FormatPlainText
	} else {
		outcome, callErr := tryStructuredReview(ctx, prov, req, func() {
			prog.Soft()
			prog.Start(cat.T("progress.retrying"))
		})
		if callErr != nil {
			callErr = wrapTimeoutErr(ctx, fmt.Errorf("provider %s: %w", prov.Name(), callErr), cat, app.Timeout)
			prog.Fail(callErr)
			return callErr
		}
		content, usage, format = outcome.Content, outcome.Usage, outcome.Format
		retries, degrade = outcome.Retries, outcome.DegradeReason
	}
	prog.Finish()
	prog.Clear()
	latency := time.Since(start)

	var findings []render.Finding
	switch format {
	case cache.FormatJSON:
		findings, _ = render.ParseFindings(content)
	case cache.FormatMarkdownFallback:
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), cat.T("review.degraded"))
	}
	findings = stampIdentity(parsed, findings)
	// Inline suppression, same helper as the posting path and the cache-hit
	// branch above.
	findings, suppressed, supWarnings := suppressPRFindings(parsed, findings)
	for _, w := range supWarnings {
		warnT(cmd, app, suppressWarningKey(w), w.Path, w.Line, w.Token)
	}

	meta := render.Meta{
		Provider:      prov.Name(),
		Model:         model,
		Lang:          app.Lang.Code,
		Suppressed:    suppressed,
		Usage:         usage,
		Cost:          resolvePricing(app.Config, prov, model).Cost(usage),
		CostUnknown:   costIsUnknown(app.Config, prov, model),
		Latency:       latency,
		Timestamp:     time.Now().UTC(),
		Files:         parsed.FileCount(),
		LinesAdded:    parsed.AddedLines(),
		LinesRemoved:  parsed.DeletedLines(),
		RulesLoaded:   loaded.Source != rules.SourceDefault,
		Retries:       retries,
		DegradeReason: degrade,
	}

	if !global.noCache && cacheStore != nil {
		diffSum := sha256.Sum256([]byte(diffText))
		promptSum := sha256.Sum256([]byte(p.System))
		_ = cacheStore.Put(cacheKey, cache.Entry{
			Key: cache.KeyMeta{
				DiffHash:         "sha256:" + hex.EncodeToString(diffSum[:]),
				SystemPromptHash: "sha256:" + hex.EncodeToString(promptSum[:]),
				Provider:         prov.Name(),
				Model:            model,
				Lang:             app.Lang.Code,
			},
			Result: cache.Result{
				Content:       content,
				Format:        format,
				DegradeReason: degrade,
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
	signalControlFooter(cmd, app, 0, suppressed)
	handleCopyFlag(cmd, app, findings)
	return applyFailOn(cmd, app, findings)
}

// prReviewResult bundles everything one PR review produces that the
// caller needs downstream: the findings, the anchor index to place them,
// and the provider usage + latency that feed the terminal footer line.
type prReviewResult struct {
	findings []render.Finding
	anchors  map[string]diff.FileAnchors
	usage    provider.Usage
	latency  time.Duration
	// incremental is true when the model only saw the prev...head delta;
	// deltaPaths then holds the files that delta touched (the only threads
	// an incremental run may resolve, ADR-0046 §A.7 G1).
	incremental bool
	deltaPaths  map[string]bool
	// deltaAnchors indexes the prev...head delta the model actually read. Its
	// LEFT numbers are relative to prev, not to the PR base, so a finding on
	// them cannot be pinned on the PR diff's LEFT side (ADR-0046 §Update
	// 2026-10-19).
	deltaAnchors map[string]diff.FileAnchors
	// noNewCommits is set when the head moved back to the previously
	// reviewed commit while the review was running (nothing left to do).
	noNewCommits bool
	// suppressed counts findings dropped by an inline commitbrief-ignore
	// marker (ADR-0027); reported once in the stderr footer.
	suppressed int
	// suppressWarnings carries unknown-scope marker warnings out of the
	// (possibly retried) review so they print once, after the retry loop.
	suppressWarnings []suppress.Warning
}

// reviewPRDiff fetches the PR diff, runs one review, and guards against a
// race: if the PR head OID changed during the review it retries once,
// then aborts (ADR-0016 §7). Returns the review result plus the OID it
// was produced against (used to anchor inline comments).
func reviewPRDiff(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, app *appContext, prov provider.Provider, model string, loaded rules.Loaded, lastOID, slug, prev string, prog *ui.Progress) (prReviewResult, string, error) {
	for attempt := 0; ; attempt++ {
		if prev != "" && prev == lastOID {
			return prReviewResult{noNewCommits: true}, lastOID, nil
		}
		res, err := reviewOnePRDiff(ctx, runner, prID, f, app, prov, model, loaded, lastOID, slug, prev, prog)
		if err != nil {
			return prReviewResult{}, "", err
		}
		newOID, err := remote.FetchLastOID(ctx, runner, prID, f.repo)
		if err != nil {
			return prReviewResult{}, "", err
		}
		if newOID == lastOID {
			return res, lastOID, nil
		}
		if attempt >= 1 {
			return prReviewResult{}, "", errors.New(app.Catalog.T("remote.too_volatile"))
		}
		// Head moved: neutralize the just-finished review stage (it was
		// valid work, just superseded) and note the retry before looping.
		prog.Soft()
		prog.Info(app.Catalog.T("remote.race_retry"))
		lastOID = newOID
	}
}

// reviewOnePRDiff runs the structured review pipeline once against the
// PR's current diff. Bot-mode: the secret scanner warns but never aborts
// (ADR-0016 §3); the local-config guard and cost preflight are skipped.
func reviewOnePRDiff(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, app *appContext, prov provider.Provider, model string, loaded rules.Loaded, headOID, slug, prev string, prog *ui.Progress) (prReviewResult, error) {
	rawDiff, err := remote.FetchDiff(ctx, runner, prID, f.repo)
	if err != nil {
		return prReviewResult{}, err
	}
	parsed, err := diff.Parse(git.Diff{Content: rawDiff, Origin: git.OriginDiff})
	if err != nil {
		return prReviewResult{}, err
	}
	parsed = diff.Filter(parsed, buildMatcher(app.RepoRoot))
	// The --no-post path has always honored --file/--dir; the posting path
	// silently did not. Both go through the shared helper now so a narrowed
	// `remote pr` reviews the same file set whether or not it comments.
	parsed, err = keepAndDropPaths(parsed)
	if err != nil {
		return prReviewResult{}, errors.New(app.Catalog.T("filter.glob.invalid", err.Error()))
	}
	if parsed.Empty() {
		return prReviewResult{findings: []render.Finding{}, anchors: map[string]diff.FileAnchors{}}, nil
	}
	anchors := parsed.Anchors()

	// Incremental: the model reads only the prev...head delta, while anchoring
	// and identity stay on the full PR diff above (ADR-0046 §A.7b). Any doubt
	// about the range falls back to the full review with an info line.
	reviewed := parsed
	incremental := false
	var deltaPaths map[string]bool
	var deltaAnchors map[string]diff.FileAnchors
	if prev != "" {
		delta, ok, derr := incrementalDelta(ctx, runner, slug, headOID, prev, app, prog)
		if derr != nil {
			return prReviewResult{}, derr
		}
		if ok {
			if delta.Empty() {
				// Only filtered-out files changed since the last review.
				return prReviewResult{findings: []render.Finding{}, anchors: anchors, incremental: true, deltaPaths: map[string]bool{}}, nil
			}
			reviewed, incremental = delta, true
			deltaPaths = map[string]bool{}
			deltaAnchors = delta.Anchors()
			for path := range deltaAnchors {
				deltaPaths[path] = true
			}
		}
	}
	diffText := reviewed.String()

	// "analyzing N files · X added · Y removed [· COMMITBRIEF.md loaded]" —
	// the same status line the local review renders, emitted here as a tree
	// info note. It also finishes the active fetch stage (info auto-finishes
	// the stage above it), mirroring local review's diff-stats line.
	prog.Info(render.StatusLine(render.Meta{
		Files:        reviewed.FileCount(),
		LinesAdded:   reviewed.AddedLines(),
		LinesRemoved: reviewed.DeletedLines(),
		RulesLoaded:  loaded.Source != rules.SourceDefault,
	}))

	// Emit the secret warning before starting the review stage, so it lands
	// as a note rather than prematurely terminating the review stage.
	if app.Config.Guard.SecretScan && !global.allowSecrets {
		if hits := guard.ScanForSecrets(diffText); len(hits) > 0 {
			prog.Info(app.Catalog.T("remote.secret_warn", len(hits)))
		}
	}

	prog.Start(app.Catalog.T("remote.reviewing"))

	// The model sees the line-numbered diff so it copies line numbers
	// instead of estimating them (see review.go); anchors above are built
	// from the same parsed diff.
	p := prompt.Build(loaded, app.Lang, reviewed.NumberedString(), "")
	req := provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
	}
	start := time.Now()
	outcome, err := tryStructuredReview(ctx, prov, req, func() {})
	if err != nil {
		return prReviewResult{}, wrapTimeoutErr(ctx, err, app.Catalog, app.Timeout)
	}
	latency := time.Since(start)
	if outcome.Format != cache.FormatJSON {
		return prReviewResult{}, errors.New(app.Catalog.T("remote.degraded"))
	}
	findings, err := render.ParseFindings(outcome.Content)
	if err != nil {
		return prReviewResult{}, errors.New(app.Catalog.T("remote.degraded"))
	}
	findings = stampIdentity(parsed, findings)

	// Inline suppression (ADR-0027, ADR-0046 §Update 2026-10-20), shared with
	// the --no-post path. A suppressed finding is dropped before sync, so its
	// open thread from a previous run counts as gone and is resolved.
	findings, suppressed, supWarnings := suppressPRFindings(parsed, findings)
	return prReviewResult{
		findings: findings, anchors: anchors, usage: outcome.Usage, latency: latency,
		incremental: incremental, deltaPaths: deltaPaths, deltaAnchors: deltaAnchors,
		suppressed: suppressed, suppressWarnings: supWarnings,
	}, nil
}

// suppressPRFindings applies inline commitbrief-ignore suppression (ADR-0027)
// to a PR review's findings. It is the single implementation behind both the
// posting path and the --no-post path, so a finding silenced by a marker is
// silenced in both. Markers are read from parsed, the FULL (filtered) PR diff
// rather than the delta an incremental run showed the model, so a marker added
// in an earlier commit still counts. Baseline is deliberately not applied: it
// is a user-private local file, not part of the PR under review. Warnings are
// returned, not printed, because each caller owns its own stderr channel.
func suppressPRFindings(parsed diff.Diff, findings []render.Finding) (kept []render.Finding, suppressed int, warnings []suppress.Warning) {
	if findings == nil {
		return nil, 0, nil
	}
	sup := suppress.ParseSuppressions(parsed)
	kept, suppressed = suppress.Filter(findings, sup)
	return kept, suppressed, sup.Warnings()
}

// suppressWarningKey picks the i18n key for one marker warning.
func suppressWarningKey(w suppress.Warning) string {
	if w.UnknownRule {
		return "suppress.unknown_rule"
	}
	return "suppress.unknown_scope"
}

// incrementalDelta returns the parsed, filtered prev...head diff when the
// range can be reviewed on its own: prev must be an ancestor of head
// (status "ahead", so no force-push), no new commit may be a merge commit
// (a merge drags other people's code into the delta), the commit list must
// be complete, and the diff itself must be retrievable. Otherwise ok is false
// and one info line explains the fallback to a full review. An error is
// returned only for an invalid --file/--dir glob. Nothing here writes to
// local git (ADR-0019): everything comes from the GitHub API.
func incrementalDelta(ctx context.Context, runner remote.Runner, slug, headOID, prev string, app *appContext, prog *ui.Progress) (diff.Diff, bool, error) {
	cat := app.Catalog
	cmp, err := remote.FetchCompare(ctx, runner, slug, prev, headOID)
	switch {
	case err != nil:
		prog.Info(cat.T("remote.since_compare_failed", err.Error()))
		return diff.Diff{}, false, nil
	case !cmp.Ahead():
		prog.Info(cat.T("remote.since_not_ahead", cmp.Status))
		return diff.Diff{}, false, nil
	case !cmp.Complete():
		prog.Info(cat.T("remote.since_compare_incomplete"))
		return diff.Diff{}, false, nil
	case cmp.HasMerge():
		prog.Info(cat.T("remote.since_merge_commit"))
		return diff.Diff{}, false, nil
	}
	raw, err := remote.FetchCompareDiff(ctx, runner, slug, prev, headOID)
	if err != nil {
		prog.Info(cat.T("remote.since_diff_failed", err.Error()))
		return diff.Diff{}, false, nil
	}
	delta, err := diff.Parse(git.Diff{Content: raw, Origin: git.OriginDiff})
	if err != nil {
		prog.Info(cat.T("remote.since_diff_failed", err.Error()))
		return diff.Diff{}, false, nil
	}
	delta = diff.Filter(delta, buildMatcher(app.RepoRoot))
	delta, err = keepAndDropPaths(delta)
	if err != nil {
		return diff.Diff{}, false, errors.New(cat.T("filter.glob.invalid", err.Error()))
	}
	return delta, true, nil
}

// submitPRReview syncs the inline comments with the PR's existing review
// threads and submits the review-level verdict (ADR-0046, ADR-0016). Each
// fingerprint group is anchored to the side (RIGHT/LEFT) its line actually
// lives on; findings that cannot get an inline comment are not dropped but
// appended to the review summary so the signal survives (ADR-0016 §9).
// Per-comment failures never abort the verdict submission, and a failed
// thread read degrades to plain posting (no sync).
func submitPRReview(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, meta remote.PRMeta, oid string, review prReviewResult, threshold render.Severity, whoami string, app *appContext, prog *ui.Progress) error {
	cat := app.Catalog
	findings, anchors := review.findings, review.anchors
	verdict := computeVerdict(findings, threshold)

	slug := meta.BaseSlug()
	var res remote.Result
	var readErr error
	scope, skipSync := syncScope(anchors)
	if skipSync {
		// A narrowed run that kept no file reviewed nothing: it must not
		// post, update, resolve or reply (ADR-0046 §Update 2026-10-18).
		prog.Info(cat.T("remote.sync_skipped_empty_scope"))
	} else {
		groups := buildSyncGroups(findings, anchors, review.deltaAnchors, whoami)
		var threads []remote.Thread
		threads, readErr = remote.FetchThreads(ctx, runner, slug, meta.Number)
		if readErr != nil {
			prog.Info(cat.T("remote.sync_read_failed", readErr.Error()))
			threads = nil
		}
		if len(groups) > 0 || len(threads) > 0 {
			prog.Start(cat.T("remote.posting_comments", meta.Number))
		}
		params := remote.SyncParams{
			Runner:   runner,
			Slug:     slug,
			PRNumber: meta.Number,
			CommitID: oid,
			Threads:  threads,
			Groups:   groups,
			Select:   func(c []render.Finding) []render.Finding { return selectPostable(c, threshold) },
			InScope:  scope,
		}
		if review.incremental {
			// An incremental run only re-examined the delta: it resolves just
			// the gone findings whose file the delta touched (ADR-0046 §A.7).
			params.Incremental = true
			params.InDiff = func(path string) bool { return review.deltaPaths[path] }
		}
		res = remote.Sync(ctx, params)
		if len(groups) > 0 || len(threads) > 0 {
			prog.Finish() // sync stage done
		}
		// Warnings are collected by Sync and emitted after the stage: prog.Info
		// would terminate the active stage on the first one.
		for _, w := range res.Warnings {
			prog.Info(syncWarning(cat, w))
		}
		if res.StaleOpen > 0 {
			prog.Info(cat.T("remote.sync_stale_open", res.StaleOpen, staleReason(cat, res.StaleReason)))
		}
		if readErr != nil {
			if res.Posted+res.PostFailed > 0 {
				prog.Info(cat.T("remote.posted_summary", res.Posted, res.Posted+res.PostFailed, res.PostFailed))
			}
		} else {
			prog.Info(cat.T("remote.sync_summary", res.Posted, res.Updated, res.Unchanged, res.Resolved, res.Replied, res.Held))
		}
		if len(res.Unanchored) > 0 {
			prog.Info(cat.T("remote.unanchored_appended", len(res.Unanchored)))
		}
	}

	// An incremental run did not re-examine every file, so an approve is only
	// honest when no open CommitBrief thread is left anywhere (held ones
	// included). Without a readable thread list that cannot be known.
	if review.incremental && verdict == remote.VerdictApprove && (res.OpenOurs > 0 || readErr != nil) {
		verdict = remote.VerdictComment
	}

	body := remote.BuildReviewBody(verdict, whoami)
	if section := remote.BuildUnanchoredSection(res.Unanchored); section != "" {
		body += "\n\n---\n\n" + section
	}
	// K8: a review that covered the whole PR records the head it was produced
	// against, so the next --since-last-review run knows where to start. A
	// path-narrowed run (or one whose scope came out empty) saw only part of
	// the PR: stamping it would let the next incremental run skip code nobody
	// reviewed, so it leaves the marker to the last full review (ADR-0046
	// §Update 2026-10-19). An empty OID leaves the body unchanged.
	markerOID := oid
	if pathsNarrowed() || skipSync {
		markerOID = ""
	}
	body = remote.WithReviewedMarker(body, markerOID)
	prog.Start(cat.T("remote.submitting"))
	if err := remote.SubmitReview(ctx, runner, prID, f.repo, verdict, body); err != nil {
		return err
	}
	prog.Finish()
	switch verdict {
	case remote.VerdictApprove:
		prog.Info(cat.T("remote.action_approve", meta.Number))
	case remote.VerdictRequestChanges:
		prog.Info(cat.T("remote.action_request_changes", meta.Number))
	default:
		prog.Info(cat.T("remote.action_comment", meta.Number))
	}
	return nil
}

// syncScope decides which review threads a sync run may touch. Without a
// path-narrowing flag the whole PR was reviewed and every thread is in scope
// (nil). With one, only the files the run actually reviewed are in scope, and
// an empty reviewed set means the sync is skipped altogether.
func syncScope(anchors map[string]diff.FileAnchors) (scope func(string) bool, skip bool) {
	if !pathsNarrowed() {
		return nil, false
	}
	if len(anchors) == 0 {
		return nil, true
	}
	return func(path string) bool { _, ok := anchors[path]; return ok }, false
}

// staleReason renders why duplicate threads stayed open.
func staleReason(cat *i18n.Catalog, reason string) string {
	if reason == "permission" {
		return cat.T("remote.sync_stale_no_permission")
	}
	return reason
}

// syncWarning renders one sync warning for stderr.
func syncWarning(cat *i18n.Catalog, w remote.Warning) string {
	if w.Op == "post" && w.Err != nil {
		return cat.T("remote.comment_failed", w.Ref, w.Err.Error())
	}
	if w.Err == nil {
		return cat.T("remote.sync_no_permission", w.Op, w.Ref)
	}
	return cat.T("remote.sync_failed", w.Op, w.Ref, w.Err.Error())
}

// buildSyncGroups folds findings that share a fingerprint into one comment
// group (ADR-0046 §3): the comment is anchored on the lowest anchorable line
// and lists the other occurrences. A finding without a valid fingerprint is
// its own unmanaged group.
//
// delta is non-nil only for an incremental run: the model then read the
// prev...head delta, whose LEFT line numbers are relative to prev and mean
// nothing on the PR diff's LEFT side. A finding that lives on the delta's
// LEFT side is therefore left unanchored (it lands in the review body)
// rather than pinned to an unrelated line (ADR-0046 §Update 2026-10-19).
func buildSyncGroups(findings []render.Finding, anchors, delta map[string]diff.FileAnchors, whoami string) []remote.Group {
	type bucket struct {
		fp      string
		members []render.Finding
	}
	var order []*bucket
	byFP := map[string]*bucket{}
	for _, fnd := range findings {
		if !remote.ValidFingerprint(fnd.Fingerprint) {
			order = append(order, &bucket{members: []render.Finding{fnd}})
			continue
		}
		b := byFP[fnd.Fingerprint]
		if b == nil {
			b = &bucket{fp: fnd.Fingerprint}
			byFP[fnd.Fingerprint] = b
			order = append(order, b)
		}
		b.members = append(b.members, fnd)
	}

	groups := make([]remote.Group, 0, len(order))
	for _, b := range order {
		sort.SliceStable(b.members, func(i, j int) bool {
			if b.members[i].File != b.members[j].File {
				return b.members[i].File < b.members[j].File
			}
			return b.members[i].Line < b.members[j].Line
		})
		resolve := func(m render.Finding) (string, bool) {
			fa, ok := anchors[m.File]
			if !ok {
				return "", false
			}
			if delta != nil {
				if preferLeftSide(m) {
					return "", false
				}
				if da, ok := delta[m.File]; ok && da.Has(diff.SideLeft, m.Line) && !da.Has(diff.SideRight, m.Line) {
					return "", false
				}
			}
			side, ok := fa.Resolve(m.Line, preferLeftSide(m))
			if ok && delta != nil && side == diff.SideLeft {
				return "", false
			}
			return side, ok
		}
		g := remote.Group{FP: b.fp, Members: b.members, Rep: b.members[0], Severity: b.members[0].Severity}
		found := false
		for _, m := range b.members {
			if severityRank[m.Severity] < severityRank[g.Severity] {
				g.Severity = m.Severity
			}
			if !found {
				if side, ok := resolve(m); ok {
					g.Rep, g.Side, g.Anchored, found = m, side, true, true
				}
			}
		}
		// The header shows the group's severity, the one the cap ranks it by.
		head := g.Rep
		head.Severity = g.Severity
		g.Text = remote.CommentText(head, whoami)
		if len(b.members) > 1 {
			refs := make([]string, 0, len(b.members)-1)
			for _, m := range b.members {
				if m.File == g.Rep.File && m.Line == g.Rep.Line {
					continue
				}
				refs = append(refs, m.PathRef()+" ("+m.Title+")")
			}
			if len(refs) > 0 {
				g.Text += "\n\nAlso at: " + strings.Join(refs, ", ")
			}
		}
		g.Text = remote.NormalizeText(g.Text)
		groups = append(groups, g)
	}
	return groups
}

// preferLeftSide reports whether a finding reads as being about removed
// code, so a line number that is valid on both diff sides resolves to
// LEFT instead of RIGHT. Heuristic: the snippet carries at least one
// removed ("-") line and no added ("+") line. With no snippet we keep
// the RIGHT-first default — the common case is a finding about new code.
func preferLeftSide(f render.Finding) bool {
	if f.Snippet == "" {
		return false
	}
	minus, plus := 0, 0
	for _, ln := range strings.Split(f.Snippet, "\n") {
		switch {
		case strings.HasPrefix(ln, "-"):
			minus++
		case strings.HasPrefix(ln, "+"):
			plus++
		}
	}
	return minus > 0 && plus == 0
}

// computeVerdict maps findings + threshold to a GitHub review verdict
// (ADR-0016 §5). Severity rank: lower = more severe (severityRank).
func computeVerdict(findings []render.Finding, threshold render.Severity) remote.Verdict {
	if len(findings) == 0 {
		return remote.VerdictApprove
	}
	// An empty threshold means --request-changes-on was not set: request-changes
	// is opt-in, so we never escalate past comment regardless of severity. A
	// real threshold re-enables the at/above-rank escalation below.
	enabled := threshold != ""
	tr := severityRank[threshold]
	onlyInfo := true
	reached := false
	for _, fnd := range findings {
		if fnd.Severity != render.SeverityInfo {
			onlyInfo = false
		}
		if enabled && severityRank[fnd.Severity] <= tr {
			reached = true
		}
	}
	switch {
	case reached:
		return remote.VerdictRequestChanges
	case onlyInfo:
		return remote.VerdictApprove
	default:
		return remote.VerdictComment
	}
}

// selectPostable applies the loop-cap rule (ADR-0016 §6), sorted by
// severity desc then file+line. When the threshold is critical/high (or
// empty — request-changes disabled): critical+high are always posted,
// everything else is capped at 10. When the threshold is medium/low: post
// everything at/above it, no cap. Inline-comment selection is independent of
// the verdict, so disabling request-changes does not change which comments
// post — only the review-level verdict (see computeVerdict).
func selectPostable(findings []render.Finding, threshold render.Severity) []render.Finding {
	sorted := make([]render.Finding, len(findings))
	copy(sorted, findings)
	sort.SliceStable(sorted, func(i, j int) bool {
		ri, rj := severityRank[sorted[i].Severity], severityRank[sorted[j].Severity]
		if ri != rj {
			return ri < rj
		}
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].Line < sorted[j].Line
	})

	// Empty threshold (request-changes disabled) shares the critical/high cap
	// policy. Stated explicitly rather than leaning on severityRank[""] == 0.
	flagHigh := threshold == "" || severityRank[threshold] <= severityRank[render.SeverityHigh]
	out := make([]render.Finding, 0, len(sorted))
	below := 0
	for _, fnd := range sorted {
		if !flagHigh {
			if severityRank[fnd.Severity] <= severityRank[threshold] {
				out = append(out, fnd)
			}
			continue
		}
		if severityRank[fnd.Severity] <= severityRank[render.SeverityHigh] {
			out = append(out, fnd)
			continue
		}
		if below < 10 {
			out = append(out, fnd)
			below++
		}
	}
	return out
}
