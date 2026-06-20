// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/baseline"
	"github.com/CommitBrief/commitbrief/internal/diff"
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
		findings, baselined = baseline.Filter(findings, set)
	}

	// SC2 — inline suppression. Always active (it's an in-source, reviewer-
	// visible marker with no team-shared hide-vector risk, so no config gate).
	// Markers are read from the ADDED diff lines and matched to a finding's
	// line (or the line directly above).
	sup := suppress.ParseSuppressions(parsed)
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
