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
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/remote"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

type remotePRFlags struct {
	requestChangesOn string
	repo             string
	noPost           bool
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
			"See ADR-0016.",
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
	// --no-post (ADR-0016 §Update): use the PR diff purely as a review
	// source and render to the terminal like a local review — no GitHub
	// writes (no comments, no verdict), so the local-render and CLI flags
	// apply. Diverges enough from the posting flow to warrant its own path.
	if f.noPost {
		return runRemotePRLocal(cmd, prID, f, runner)
	}

	ctx := cmd.Context()

	app, err := resolveContext(false)
	if err != nil {
		return err
	}
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

	prov, err := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider])
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

	res, oid, err := reviewPRDiff(ctx, runner, prID, f, app, prov, model, loaded, meta.LastOID(), prog)
	if err != nil {
		prog.Fail(err)
		return err
	}

	if err := submitPRReview(ctx, runner, prID, f, meta, oid, res.findings, res.anchors, threshold, whoami, app, prog); err != nil {
		prog.Fail(err)
		return err
	}

	// Standard review footer line ("✓ Done in … · N findings · tokens ·
	// $cost"), printed below the now-finished tree (Close, not Clear, so the
	// tree stays on screen). Honors --quiet.
	prog.Close()
	if !global.quiet {
		footerMeta := render.Meta{
			Provider: prov.Name(),
			Model:    model,
			Usage:    res.usage,
			Cost:     resolvePricing(app.Config, prov, model).Cost(res.usage),
			Latency:  res.latency,
		}
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), render.FooterLine(footerMeta, res.findings))
	}
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
	ctx := cmd.Context()
	app, err := resolveContext(false)
	if err != nil {
		return err
	}
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
	if err := remote.EnsureGH(); err != nil {
		return errors.New(cat.T("remote.gh_missing"))
	}

	prov, err := provider.New(app.Config.Provider, app.Config.Providers[app.Config.Provider])
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
	parsed, err = diff.KeepPaths(parsed, global.files, global.dirs)
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

	var p prompt.Prompt
	if plainText {
		p = prompt.BuildPlainText(loaded, app.Lang, numbered, false)
	} else {
		p = prompt.Build(loaded, app.Lang, numbered)
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
				Files:        parsed.FileCount(),
				LinesAdded:   parsed.AddedLines(),
				LinesRemoved: parsed.DeletedLines(),
				RulesLoaded:  loaded.Source != rules.SourceDefault,
			}
			var findings []render.Finding
			switch entry.Result.Format {
			case cache.FormatJSON, "":
				findings, _ = render.ParseFindings(entry.Result.Content)
			}
			if entry.Result.Format == cache.FormatPlainText {
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
	prog.Finish()

	// Cost preflight, same as a local review (no-op for zero-priced CLI
	// providers). --no-cost-check bypasses; --yes deliberately does not.
	if !global.noCostCheck {
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
	)
	if plainText {
		resp, callErr := prov.Review(ctx, req)
		if callErr != nil {
			prog.Fail(callErr)
			return fmt.Errorf("provider %s: %w", prov.Name(), callErr)
		}
		content, usage, format = resp.Content, resp.Usage, cache.FormatPlainText
	} else {
		var callErr error
		content, usage, format, callErr = tryStructuredReview(ctx, prov, req, func() {
			prog.Soft()
			prog.Start(cat.T("progress.retrying"))
		})
		if callErr != nil {
			prog.Fail(callErr)
			return fmt.Errorf("provider %s: %w", prov.Name(), callErr)
		}
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

	meta := render.Meta{
		Provider:     prov.Name(),
		Model:        model,
		Lang:         app.Lang.Code,
		Usage:        usage,
		Cost:         resolvePricing(app.Config, prov, model).Cost(usage),
		Latency:      latency,
		Timestamp:    time.Now().UTC(),
		Files:        parsed.FileCount(),
		LinesAdded:   parsed.AddedLines(),
		LinesRemoved: parsed.DeletedLines(),
		RulesLoaded:  loaded.Source != rules.SourceDefault,
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

// prReviewResult bundles everything one PR review produces that the
// caller needs downstream: the findings, the anchor index to place them,
// and the provider usage + latency that feed the terminal footer line.
type prReviewResult struct {
	findings []render.Finding
	anchors  map[string]diff.FileAnchors
	usage    provider.Usage
	latency  time.Duration
}

// reviewPRDiff fetches the PR diff, runs one review, and guards against a
// race: if the PR head OID changed during the review it retries once,
// then aborts (ADR-0016 §7). Returns the review result plus the OID it
// was produced against (used to anchor inline comments).
func reviewPRDiff(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, app *appContext, prov provider.Provider, model string, loaded rules.Loaded, lastOID string, prog *ui.Progress) (prReviewResult, string, error) {
	for attempt := 0; ; attempt++ {
		res, err := reviewOnePRDiff(ctx, runner, prID, f, app, prov, model, loaded, prog)
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
func reviewOnePRDiff(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, app *appContext, prov provider.Provider, model string, loaded rules.Loaded, prog *ui.Progress) (prReviewResult, error) {
	rawDiff, err := remote.FetchDiff(ctx, runner, prID, f.repo)
	if err != nil {
		return prReviewResult{}, err
	}
	parsed, err := diff.Parse(git.Diff{Content: rawDiff, Origin: git.OriginDiff})
	if err != nil {
		return prReviewResult{}, err
	}
	parsed = diff.Filter(parsed, buildMatcher(app.RepoRoot))
	if parsed.Empty() {
		return prReviewResult{findings: []render.Finding{}, anchors: map[string]diff.FileAnchors{}}, nil
	}
	diffText := parsed.String()
	anchors := parsed.Anchors()

	// "analyzing N files · X added · Y removed [· COMMITBRIEF.md loaded]" —
	// the same status line the local review renders, emitted here as a tree
	// info note. It also finishes the active fetch stage (info auto-finishes
	// the stage above it), mirroring local review's diff-stats line.
	prog.Info(render.StatusLine(render.Meta{
		Files:        parsed.FileCount(),
		LinesAdded:   parsed.AddedLines(),
		LinesRemoved: parsed.DeletedLines(),
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
	p := prompt.Build(loaded, app.Lang, parsed.NumberedString())
	req := provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
	}
	start := time.Now()
	content, usage, format, err := tryStructuredReview(ctx, prov, req, func() {})
	if err != nil {
		return prReviewResult{}, err
	}
	latency := time.Since(start)
	if format != cache.FormatJSON {
		return prReviewResult{}, errors.New(app.Catalog.T("remote.degraded"))
	}
	findings, err := render.ParseFindings(content)
	if err != nil {
		return prReviewResult{}, errors.New(app.Catalog.T("remote.degraded"))
	}
	return prReviewResult{findings: findings, anchors: anchors, usage: usage, latency: latency}, nil
}

// submitPRReview posts the selected inline comments and the review-level
// verdict. Each finding is anchored to the side (RIGHT/LEFT) its line
// actually lives on; findings whose line is outside the diff — or whose
// POST is rejected — are not dropped but appended to the review summary
// so the signal survives (ADR-0016 §9). Per-comment failures never abort
// the verdict submission.
func submitPRReview(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, meta remote.PRMeta, oid string, findings []render.Finding, anchors map[string]diff.FileAnchors, threshold render.Severity, whoami string, app *appContext, prog *ui.Progress) error {
	cat := app.Catalog
	verdict := computeVerdict(findings, threshold)
	postable := selectPostable(findings, threshold)

	slug := meta.BaseSlug()
	posted, failed := 0, 0
	var unanchored []render.Finding
	var failures []string
	if len(postable) > 0 {
		prog.Start(cat.T("remote.posting_comments", len(postable)))
	}
	for _, fnd := range postable {
		fa, hasFile := anchors[fnd.File]
		side, ok := "", false
		if hasFile {
			side, ok = fa.Resolve(fnd.Line, preferLeftSide(fnd))
		}
		if !ok {
			// Line is not a postable position in the diff — a GitHub POST
			// would 422. Keep it for the summary instead of losing it.
			unanchored = append(unanchored, fnd)
			continue
		}
		err := remote.PostComment(ctx, runner, remote.CommentRequest{
			RepoSlug: slug,
			PRNumber: meta.Number,
			CommitID: oid,
			Path:     fnd.File,
			Line:     fnd.Line,
			Side:     side,
			Body:     remote.BuildCommentBody(fnd, whoami),
		})
		if err != nil {
			failed++
			unanchored = append(unanchored, fnd)
			// Collect rather than emit mid-loop: prog.Info would terminate
			// the active posting stage on the first failure.
			failures = append(failures, cat.T("remote.comment_failed", fnd.PathRef(), err.Error()))
			continue
		}
		posted++
	}
	if len(postable) > 0 {
		prog.Finish() // posting stage done
	}
	for _, msg := range failures {
		prog.Info(msg)
	}
	if posted+failed > 0 {
		prog.Info(cat.T("remote.posted_summary", posted, posted+failed, failed))
	}
	if len(unanchored) > 0 {
		prog.Info(cat.T("remote.unanchored_appended", len(unanchored)))
	}

	body := remote.BuildReviewBody(verdict, whoami)
	if section := remote.BuildUnanchoredSection(unanchored); section != "" {
		body += "\n\n---\n\n" + section
	}
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
