// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/baseline"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/suppress"
)

// applyActionableFilters is the signal-control stage (ADR-0027). It runs
// after the findings are assembled (LLM + flaky merge) and BEFORE
// applyFailOn / render, so its removals are TRUE removals: they affect the
// --fail-on gate, the JSON findings[] array, and the rendered display alike —
// deliberately distinct from the display-only filterMinSeverity, which only
// hides findings from the human render.
//
// Order is baseline → suppression:
//
//	findings → [SC1 baseline] → [SC2 inline suppression] → fail-on + render
//
// Counts are returned so the caller can stamp them onto render.Meta (the
// additive JSON meta.baselined/meta.suppressed) and print the human footer —
// filtering is never silent (a trust-sensitive review tool must report what
// it removed).
//
// --update-baseline is handled here as a short-circuit: it ABSORBS the
// current findings into baseline.json and returns them unchanged (this run is
// not filtered — the user is re-baselining, so they want to see the full set
// once before future runs go quiet). It is mutually exclusive with
// --no-baseline at the flag layer.
//
// A nil findings slice (graceful degrade: the LLM produced unparseable
// output) is passed straight through untouched — there is nothing to
// fingerprint or suppress, and we must preserve the nil so downstream
// fail-on/JSON keep treating it as a degrade.
func applyActionableFilters(cmd *cobra.Command, app *appContext, parsed diff.Diff, findings []render.Finding) (kept []render.Finding, baselined, suppressed int, err error) {
	if findings == nil {
		return nil, 0, 0, nil
	}

	// --update-baseline: write the current fingerprints, report, return the
	// findings unfiltered. Honors --no-baseline being impossible here (flag
	// mutual exclusion) so we never write while also being told to ignore.
	if global.updateBaseline {
		n, werr := baseline.Write(app.RepoRoot, findings)
		if werr != nil {
			return nil, 0, 0, werr
		}
		infof("%s", app.Catalog.T("baseline.updated", n, baseline.Path(app.RepoRoot)))
		return findings, 0, 0, nil
	}

	// SC1 — baseline filter. Skipped entirely when review.baseline is off or
	// --no-baseline is set; a missing baseline.json is a transparent no-op
	// (Load returns an empty Set). Load errors (corrupt/unsupported file)
	// surface loudly rather than silently unhiding accepted findings.
	if app.Config.Review.Baseline && !global.noBaseline {
		set, lerr := baseline.Load(app.RepoRoot)
		if lerr != nil {
			return nil, 0, 0, lerr
		}
		if set.Legacy() {
			// A v1 file matches via the legacy title-based formula; tell the
			// user once per run how to migrate (the migration is lossy, so it
			// is never done silently).
			warnT(cmd, app, "baseline.legacy", baseline.Path(app.RepoRoot))
		}
		findings, baselined = baseline.Filter(findings, set)
	}

	// SC2 — inline suppression. Always active (it's an in-source, reviewer-
	// visible marker with no team-shared hide-vector risk, so no config gate).
	// Markers are read from the ADDED diff lines and matched to a finding's
	// line (or the line directly above) in the same file.
	sup := suppress.ParseSuppressions(parsed)
	for _, w := range sup.Warnings() {
		key := "suppress.unknown_scope"
		if w.UnknownRule {
			key = "suppress.unknown_rule"
		}
		warnT(cmd, app, key, w.Path, w.Line, w.Token)
	}
	findings, suppressed = suppress.Filter(findings, sup)

	return findings, baselined, suppressed, nil
}

// signalControlFooter writes the one-line "N baselined · M suppressed" human
// footer to stderr (so it never corrupts a piped --json/--markdown stdout),
// honoring --quiet. No-op when nothing was removed. The two halves are
// localized separately (baseline.filtered / suppress.filtered) and joined
// with a middot, so a run that only baselined prints just that half. Kept on
// stderr like the other info breadcrumbs.
func signalControlFooter(cmd *cobra.Command, app *appContext, baselined, suppressed int) {
	if baselined == 0 && suppressed == 0 {
		return
	}
	if global.quiet {
		return
	}
	var parts []string
	if baselined > 0 {
		parts = append(parts, app.Catalog.T("baseline.filtered", baselined))
	}
	if suppressed > 0 {
		parts = append(parts, app.Catalog.T("suppress.filtered", suppressed))
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), strings.Join(parts, " · "))
}

// warningSinkKey is the context key under which a caller of the shared review
// seam (runReviewForMCP) installs a warningTarget.
type warningSinkKey struct{}

// warningSink receives signal-control warnings as plain lines. MCP and guard
// install one because the seam forces --quiet and buffers stderr, which would
// otherwise swallow the warnings (the legacy-baseline migration notice is the
// only signal a v1 baseline user gets).
type warningSink func(line string)

// warningTarget is the installed sink plus the catalog its lines are rendered
// with. A nil catalog means the user's UI catalog (guard prints to a terminal);
// MCP passes the English catalog because its summary is intentionally English
// model-facing text (ADR-0028), not localized UI.
type warningTarget struct {
	sink    warningSink
	catalog *i18n.Catalog
}

func withWarningSink(ctx context.Context, sink warningSink, catalog *i18n.Catalog) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, warningSinkKey{}, warningTarget{sink: sink, catalog: catalog})
}

func warningTargetFrom(ctx context.Context) (warningTarget, bool) {
	if ctx == nil {
		return warningTarget{}, false
	}
	t, ok := ctx.Value(warningSinkKey{}).(warningTarget)
	return t, ok && t.sink != nil
}

// warnT renders the catalog message `key` and delivers it as a signal-control
// warning. It writes to the command's stderr (so it never corrupts a piped
// --json/--markdown stdout), honoring --quiet. When the command's context
// carries a warning target (MCP, guard run-mode) the line goes to its sink
// instead, regardless of --quiet: those callers force --quiet for the review
// pipeline but still want the warning delivered. The target may also pin the
// catalog (MCP: English); otherwise the app's UI catalog renders the line.
func warnT(cmd *cobra.Command, app *appContext, key string, args ...any) {
	if t, ok := warningTargetFrom(cmd.Context()); ok {
		cat := app.Catalog
		if t.catalog != nil {
			cat = t.catalog
		}
		t.sink(cat.T(key, args...))
		return
	}
	if global.quiet {
		return
	}
	_, _ = fmt.Fprintln(cmd.ErrOrStderr(), app.Catalog.T(key, args...))
}

// degradeProbeKey is the context key under which a caller of the shared review
// seam (runReviewForMCP) installs a *bool that the pipeline sets when the
// review it ran, or replayed from the cache, was a degraded one.
type degradeProbeKey struct{}

// withDegradeProbe returns a context carrying a degrade probe and the probe
// itself. Read the probe only after the review has returned.
//
// meta.degrade_reason is stamped on a live degrade and, since ADR-0031 §Update
// 2026-10-20, on the cache replay of the same degraded response. The probe is
// still what run mode trusts: the pipeline knows first-hand, so it does not
// depend on the document round-tripping through meta.
func withDegradeProbe(ctx context.Context) (context.Context, *bool) {
	if ctx == nil {
		ctx = context.Background()
	}
	probe := new(bool)
	return context.WithValue(ctx, degradeProbeKey{}, probe), probe
}

// markReviewDegraded records on the context's degrade probe, if there is one,
// that the review is degraded (the model response never parsed into findings).
func markReviewDegraded(ctx context.Context) {
	if ctx == nil {
		return
	}
	if probe, ok := ctx.Value(degradeProbeKey{}).(*bool); ok && probe != nil {
		*probe = true
	}
}
