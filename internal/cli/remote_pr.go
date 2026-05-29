// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"sort"
	"strings"

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
}

func newRemotePRCmd() *cobra.Command {
	var f remotePRFlags
	cmd := &cobra.Command{
		Use:   "pr <PR-ID>",
		Short: "Review a GitHub pull request and post findings as inline comments",
		Long: "Pulls a PR's diff via `gh`, runs the review pipeline, posts each\n" +
			"finding as an inline comment, and submits a verdict (approve / comment\n" +
			"/ request-changes). <PR-ID> accepts gh-native forms: 42, owner/repo#42,\n" +
			"or a full URL. See ADR-0016.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRemotePR(cmd, args[0], f, remote.NewRunner())
		},
	}
	cmd.Flags().StringVar(&f.requestChangesOn, "request-changes-on", "critical",
		"severity at/above which the verdict becomes request-changes (critical|high|medium|low)")
	cmd.Flags().StringVar(&f.repo, "repo", "", "target repository owner/repo (overrides git context)")
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

	threshold, perr := parseRequestChangesOn(f.requestChangesOn)
	if perr != nil {
		if errors.Is(perr, errRequestChangesInfo) {
			return errors.New(cat.T("remote.request_changes_on_info"))
		}
		return errors.New(cat.T("remote.request_changes_on_invalid", f.requestChangesOn))
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

	findings, anchors, oid, err := reviewPRDiff(ctx, runner, prID, f, app, prov, loaded, meta.LastOID(), prog)
	if err != nil {
		prog.Fail(err)
		return err
	}

	if err := submitPRReview(ctx, runner, prID, f, meta, oid, findings, anchors, threshold, whoami, app, prog); err != nil {
		prog.Fail(err)
		return err
	}
	return nil
}

// reviewPRDiff fetches the PR diff, runs one review, and guards against a
// race: if the PR head OID changed during the review it retries once,
// then aborts (ADR-0016 §7). Returns the findings, the per-file anchor
// index they map onto, and the OID they were produced against (both used
// to place inline comments).
func reviewPRDiff(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, app *appContext, prov provider.Provider, loaded rules.Loaded, lastOID string, prog *ui.Progress) ([]render.Finding, map[string]diff.FileAnchors, string, error) {
	for attempt := 0; ; attempt++ {
		findings, anchors, err := reviewOnePRDiff(ctx, runner, prID, f, app, prov, loaded, prog)
		if err != nil {
			return nil, nil, "", err
		}
		newOID, err := remote.FetchLastOID(ctx, runner, prID, f.repo)
		if err != nil {
			return nil, nil, "", err
		}
		if newOID == lastOID {
			return findings, anchors, lastOID, nil
		}
		if attempt >= 1 {
			return nil, nil, "", errors.New(app.Catalog.T("remote.too_volatile"))
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
// Returns the findings plus the anchor index of the (filtered) diff the
// model reviewed, so comments can be pinned to the correct side.
func reviewOnePRDiff(ctx context.Context, runner remote.Runner, prID string, f remotePRFlags, app *appContext, prov provider.Provider, loaded rules.Loaded, prog *ui.Progress) ([]render.Finding, map[string]diff.FileAnchors, error) {
	rawDiff, err := remote.FetchDiff(ctx, runner, prID, f.repo)
	if err != nil {
		return nil, nil, err
	}
	parsed, err := diff.Parse(git.Diff{Content: rawDiff, Origin: git.OriginDiff})
	if err != nil {
		return nil, nil, err
	}
	parsed = diff.Filter(parsed, buildMatcher(app.RepoRoot))
	if parsed.Empty() {
		return []render.Finding{}, map[string]diff.FileAnchors{}, nil
	}
	diffText := parsed.String()
	anchors := parsed.Anchors()

	// Emit the secret warning before starting the review stage, so it lands
	// as a note under the (now finished) fetch stage rather than prematurely
	// terminating the review stage.
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
	model := app.Config.Providers[app.Config.Provider].Model
	if model == "" {
		model = prov.DefaultModel()
	}
	req := provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         app.Lang.Code,
	}
	content, _, format, err := tryStructuredReview(ctx, prov, req, func() {})
	if err != nil {
		return nil, nil, err
	}
	if format != cache.FormatJSON {
		return nil, nil, errors.New(app.Catalog.T("remote.degraded"))
	}
	findings, err := render.ParseFindings(content)
	if err != nil {
		return nil, nil, errors.New(app.Catalog.T("remote.degraded"))
	}
	return findings, anchors, nil
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
	tr := severityRank[threshold]
	onlyInfo := true
	reached := false
	for _, fnd := range findings {
		if fnd.Severity != render.SeverityInfo {
			onlyInfo = false
		}
		if severityRank[fnd.Severity] <= tr {
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
// severity desc then file+line. When the threshold is critical/high:
// critical+high are always posted, everything else is capped at 10.
// When the threshold is medium/low: post everything at/above it, no cap.
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

	flagHigh := severityRank[threshold] <= severityRank[render.SeverityHigh]
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
