// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/leaks"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// `commitbrief leaks` (ADR-0036) — the standalone credential audit.
//
// The pre-send scanner in internal/guard is a gate on one diff. This is an
// audit of the repository: whole files in the working tree, plus the added
// lines of historical commits. Deterministic — the same eight built-in regexes
// plus any ADR-0024 user patterns, no provider call, no cache, no cost.
//
// Both halves run by default and each has its own off-switch, so a positional
// range narrows the history half without silently disabling the tree.

type leaksFlags struct {
	noWorktree bool
	noHistory  bool
	patterns   bool
}

// leaksDefaultMaxCommits bounds the history half of a bare run. Scanning every
// commit of a long-lived repo is minutes of work; 200 covers "recent work"
// without an explicit opt-in, and truncation is always reported.
const leaksDefaultMaxCommits = 200

func newLeaksCmd() *cobra.Command {
	var f leaksFlags

	cmd := &cobra.Command{
		Use:   "leaks [<git range>...]",
		Short: "Scan the working tree and git history for committed credentials",
		Long: "Report credential-shaped content in the repository: every tracked file " +
			"in the working tree, plus the lines added by historical commits.\n\n" +
			"This is the audit counterpart to the pre-send secret scanner, which only " +
			"ever sees the one diff about to be reviewed. A secret that was committed " +
			"and later removed is still in the history — and still reachable by anyone " +
			"who clones the repo — so the history half is what finds it.\n\n" +
			"Both halves run by default; --no-worktree and --no-history switch either " +
			"off. The commit filters (--author, --start-date, --end-date, --text) narrow " +
			"which commits the history half reads, and the path filters narrow which " +
			"files either half reads.\n\n" +
			"Deterministic: the same regex set the review path uses, no provider call, " +
			"no cost. Findings report a file, a line and the pattern names that " +
			"matched — never the matched text, so the report itself cannot leak.\n\n" +
			"Exits 1 when anything is found, so it gates CI out of the box; pass " +
			"--fail-on none to report without failing.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLeaks(cmd, f, args)
		},
	}
	flags := cmd.Flags()
	flags.BoolVar(&f.noWorktree, "no-worktree", false, "skip the working-tree half")
	flags.BoolVar(&f.noHistory, "no-history", false, "skip the commit-history half")
	flags.BoolVar(&f.patterns, "patterns", false, "list the effective pattern set (built-ins + configured) and exit")
	bindSarifFlagUsage(cmd, "emit the findings as SARIF 2.1.0 for GitHub Code Scanning (category commitbrief/leaks/; use -o to write a file; the human report goes to stderr; not with --json/--patterns, and refused on a partial scan: --no-worktree/--no-history, path or commit filters, a range, or a history window cut by --max-commits)")
	return cmd
}

func runLeaks(cmd *cobra.Command, f leaksFlags, args []string) error {
	app, err := resolveContext(true)
	if err != nil {
		return err
	}
	if err := validateSarifFlags(app); err != nil {
		return err
	}
	if global.sarif && f.patterns {
		// --patterns prints a name list, not findings; there is nothing to
		// convert, and a CI step redirecting stdout would upload a non-SARIF file.
		return errors.New(app.Catalog.T("leaks.sarif_patterns_conflict"))
	}
	if global.sarif && leaksScanNarrowed(f, args) {
		// Fail closed (ADR-0047 §4): Code Scanning treats a result missing from
		// a category as fixed, and every leaks run shares one category, so a
		// partial scan would close alerts it never looked at.
		return errors.New(app.Catalog.T("leaks.sarif_narrowed"))
	}
	tctx, tcancel := app.withTimeout(cmd.Context())
	defer tcancel()
	cmd.SetContext(tctx)
	if f.noWorktree && f.noHistory {
		return errors.New(app.Catalog.T("leaks.nothing_to_scan"))
	}

	userPatterns := toUserSecretPatterns(app.Config.Guard.SecretPatterns)
	if f.patterns {
		return listLeakPatterns(cmd, userPatterns)
	}

	opts := leaks.Options{
		Patterns:     userPatterns,
		Matcher:      buildMatcher(app.RepoRoot),
		Files:        global.files,
		Dirs:         global.dirs,
		ExcludeFiles: global.excludeFiles,
		ExcludeDirs:  global.excludeDirs,
	}

	ctx := cmd.Context()
	var result leaks.Result

	if !f.noWorktree {
		tree, sErr := leaks.ScanWorktree(ctx, app.RepoRoot, opts)
		if sErr != nil {
			return leaksError(app, sErr)
		}
		result = result.Merge(tree)
	}

	if !f.noHistory {
		filter, fErr := buildWalkFilter(app.Catalog, args)
		if fErr != nil {
			return fErr
		}
		if filter.MaxCommits == 0 {
			filter.MaxCommits = leaksDefaultMaxCommits
		}
		sel, selErr := git.SelectCommits(ctx, app.RepoRoot, filter)
		if selErr != nil {
			return selErr
		}
		// Truncation is known once the selection returns; refuse the SARIF
		// log now instead of after walking every selected commit's patch.
		if global.sarif {
			if err := leaksSarifIncomplete(app, leaks.Result{Truncated: sel.Truncated, WalkTruncated: sel.WalkTruncated}); err != nil {
				return err
			}
		}
		hist, sErr := leaks.ScanHistory(ctx, app.RepoRoot, sel, opts)
		if sErr != nil {
			return leaksError(app, sErr)
		}
		result = result.Merge(hist)
	}
	result.Sort()

	if global.sarif {
		if err := leaksSarifIncomplete(app, result); err != nil {
			return err
		}
	}

	findings := leakFindings(result, userPatterns)

	if global.json {
		return emitLeaksJSON(cmd, app, findings, result)
	}
	reportOut := cmd.OutOrStdout()
	if global.sarif {
		if err := emitLeaksSarif(cmd, result, findings); err != nil {
			return err
		}
		// stdout (or -o) belongs to the SARIF document; the coverage line, the
		// skipped/truncated notices and the findings list move to stderr. The
		// gate below still decides the exit code.
		reportOut = cmd.ErrOrStderr()
	}
	if err := writeLeaksReport(reportOut, app, result, findings); err != nil {
		return err
	}
	return leaksGate(app, findings)
}

// leaksSarifIncomplete refuses a SARIF log for a history scan that did not
// reach every commit (same reasoning as the narrowing check: a commit that fell
// out of the scan would silently close its alert). The two causes need
// different advice: the --max-commits window can be raised, the commit walk's
// own ceiling cannot, so --max-commits would be a misleading hint there.
func leaksSarifIncomplete(app *appContext, res leaks.Result) error {
	switch {
	case res.WalkTruncated:
		return errors.New(app.Catalog.T("leaks.sarif_walk_truncated"))
	case res.Truncated:
		return errors.New(app.Catalog.T("leaks.sarif_truncated", leaksCommitLimit()))
	}
	return nil
}

// emitLeaksSarif writes the scan as SARIF under the leaks category (ADR-0036,
// ADR-0047). A clean scan writes results: [], which is the truthful "nothing
// found" for this category.
//
// The document never carries the matched text (ADR-0007): the message names
// the patterns and, for a history hit, the commit; the same facts are
// machine-readable in properties["commitbrief/patterns"] and
// properties["commitbrief/commit"]. The commit author is left out on purpose
// -- the SARIF file is uploaded to a code-hosting service, and an author name
// is personal data the alert does not need. No partial fingerprint is written:
// a leak has no diff symbol (see leakFindings), and upload-sarif derives its
// own location hash.
func emitLeaksSarif(cmd *cobra.Command, res leaks.Result, findings []render.Finding) error {
	// findings is leakFindings(res): the two slices are 1:1 and in the same
	// order, which the parallel extra slice below relies on. The titles and
	// descriptions are rewritten for SARIF, so work on a copy -- the human
	// report that follows still wants the originals.
	if len(findings) != len(res.Findings) {
		return errors.New("leaks: findings and scan results are out of step")
	}
	findings = append([]render.Finding(nil), findings...)
	extra := make([]map[string]any, len(findings))
	for i, origin := range res.Findings {
		patterns := append([]string(nil), origin.Patterns...)
		findings[i].Title = "Hardcoded secret (" + strings.Join(patterns, ", ") + ")"
		findings[i].Description = leakSarifDescription(origin)
		extra[i] = map[string]any{"commitbrief/patterns": patterns}
		if origin.FromHistory() {
			extra[i]["commitbrief/commit"] = origin.Short
		}
	}

	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()
	return render.SARIFWithCategory(w, findings, render.SARIFCategoryLeaks, extra)
}

// leakSarifDescription is the SARIF message body: where the hit was found, with
// no author and no matched text.
func leakSarifDescription(f leaks.Finding) string {
	if !f.FromHistory() {
		return "Matched in the working tree."
	}
	return fmt.Sprintf("Matched in commit %s. The line may no longer be in the working "+
		"tree, but it remains in the repository's history.", f.Short)
}

// leaksScanNarrowed reports whether the invocation scans less than the whole
// repository: a skipped half, a path or commit filter, or an explicit range.
// --max-commits is not a narrowing flag; it only sizes the history window, and
// a window that turns out too small is caught separately through Truncated.
func leaksScanNarrowed(f leaksFlags, args []string) bool {
	return f.noWorktree || f.noHistory || len(args) > 0 ||
		len(global.files) > 0 || len(global.dirs) > 0 ||
		len(global.excludeFiles) > 0 || len(global.excludeDirs) > 0 ||
		len(global.authors) > 0 || len(global.committers) > 0 ||
		strings.TrimSpace(global.startDate) != "" || strings.TrimSpace(global.endDate) != "" ||
		strings.TrimSpace(global.text) != ""
}

// leaksGate turns findings into the exit code.
//
// A scanner that exits 0 on a hit is useless in CI, but the exit-code contract
// has room for exactly two values — so instead of inventing a third, `leaks`
// defaults --fail-on to `any` and reuses the existing severity vocabulary.
// `--fail-on none` reports without failing. The report is written first, so
// the user gets the findings *and* the non-zero exit (the doctor/guard shape).
func leaksGate(app *appContext, findings []render.Finding) error {
	raw := strings.TrimSpace(global.failOn)
	if raw == "" {
		raw = "any"
	}
	policy, err := parseFailOn(raw)
	if err != nil {
		return err
	}
	if !policy.enabled || len(findings) == 0 {
		return nil
	}

	thresholdRank := severityRank[policy.threshold]
	matches := 0
	for _, f := range findings {
		rank, ok := severityRank[f.Severity]
		if !ok {
			continue
		}
		if policy.anyMode || rank <= thresholdRank {
			matches++
		}
	}
	if matches == 0 {
		return nil
	}
	// A short, distinct message: the report above already stated the count and
	// what to do about it, so repeating it verbatim as the error line would
	// just print the same sentence twice.
	return errors.New(app.Catalog.T("leaks.gate_failed", matches))
}

// leakFindings converts scan findings into the locked schema-v1 Finding shape,
// so `leaks --json` can be piped straight into `commitbrief guard --from-json`.
//
// Snippet is deliberately left empty: populating it would echo the secret and
// break the ADR-0007 invariant that the scanner never becomes a leak vector.
func leakFindings(res leaks.Result, userPatterns []guard.UserSecretPattern) []render.Finding {
	out := make([]render.Finding, 0, len(res.Findings))
	for _, f := range res.Findings {
		title := strings.Join(f.Patterns, ", ")
		out = append(out, render.Finding{
			Severity: leakSeverity(f, userPatterns),
			File:     f.File,
			Line:     f.Line,
			Title:    title,
			// Deterministic producer (ADR-0045): the scanner never asks a
			// model, so every hit is a hardcoded secret. No fingerprint is
			// stamped: a leak has no diff symbol, and (file, rule, "") would
			// collapse every secret in one file into a single identity.
			RuleID:      rules.RuleHardcodedSecret,
			Description: leakDescription(f),
			Suggestion: "Treat this credential as compromised: rotate it at the provider, " +
				"then remove it from the repository. For a hit in history, rotation is the " +
				"only reliable fix — rewriting history does not reach clones or forks that " +
				"already have the commit.",
		})
	}
	return out
}

// leakSeverity is critical unless every matching pattern is one we cannot
// assert criticality for (a JWT, which is often an expired fixture token, or a
// user-supplied house pattern).
func leakSeverity(f leaks.Finding, userPatterns []guard.UserSecretPattern) render.Severity {
	for _, p := range f.Patterns {
		if !leaks.IsHighNotCritical(p, userPatterns) {
			return render.SeverityCritical
		}
	}
	return render.SeverityHigh
}

func leakDescription(f leaks.Finding) string {
	patterns := strings.Join(f.Patterns, ", ")
	if !f.FromHistory() {
		return fmt.Sprintf("%s matched in the working tree at %s:%d.", patterns, f.File, f.Line)
	}
	return fmt.Sprintf("%s matched in commit %s by %s, at %s:%d. The line may no longer be "+
		"in the working tree, but it remains in the repository's history.",
		patterns, f.Short, f.Author, f.File, f.Line)
}

// emitLeaksJSON writes the schema-v1 document. meta names a "builtin" provider
// rather than a model vendor: the scan is deterministic and cost-free, and the
// alternative — a second semver-locked schema — buys nothing a consumer wants
// (ADR-0036).
func emitLeaksJSON(cmd *cobra.Command, app *appContext, findings []render.Finding, res leaks.Result) error {
	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()

	if err := render.JSON(w, render.Payload{
		Findings: findings,
		Meta: render.Meta{
			Provider:     "builtin",
			Model:        "secret-scan",
			Lang:         app.Lang.Code,
			Files:        res.FilesScanned,
			LinesAdded:   0,
			LinesRemoved: 0,
		},
	}); err != nil {
		return err
	}
	return leaksGate(app, findings)
}

// writeLeaksReport renders the human view: a coverage line, then one line per
// finding. Debug-grade tabular output, English, matching dry-run and cache
// stats.
func writeLeaksReport(w io.Writer, app *appContext, res leaks.Result, findings []render.Finding) error {
	if !global.quiet {
		if _, err := fmt.Fprintln(w, app.Catalog.T("leaks.scanned",
			res.FilesScanned, res.CommitsScanned)); err != nil {
			return fmt.Errorf("leaks: write: %w", err)
		}
		if res.Skipped > 0 {
			// Never silent: a scan that passed over half the repo must say so,
			// or "no findings" is misleading.
			_, _ = fmt.Fprintln(w, app.Catalog.T("leaks.skipped", res.Skipped))
		}
		if res.Truncated {
			_, _ = fmt.Fprintln(w, app.Catalog.T("leaks.truncated", leaksCommitLimit()))
		}
	}

	if len(findings) == 0 {
		_, err := fmt.Fprintln(w, app.Catalog.T("leaks.clean"))
		return err
	}

	if _, err := fmt.Fprintln(w); err != nil {
		return fmt.Errorf("leaks: write: %w", err)
	}
	for i, f := range findings {
		where := fmt.Sprintf("%s:%d", f.File, f.Line)
		line := fmt.Sprintf("  %-9s %-40s %s", strings.ToUpper(string(f.Severity)), where, f.Title)
		if origin := res.Findings[i]; origin.FromHistory() {
			line += fmt.Sprintf("  (%s, %s)", origin.Short, origin.Author)
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return fmt.Errorf("leaks: write: %w", err)
		}
	}
	_, err := fmt.Fprintln(w, "\n"+app.Catalog.T("leaks.found", len(findings)))
	return err
}

// listLeakPatterns implements --patterns: the effective set, so a user can
// confirm their guard.secret_patterns actually loaded.
func listLeakPatterns(cmd *cobra.Command, userPatterns []guard.UserSecretPattern) error {
	extra, err := guard.CompileUserPatterns(userPatterns)
	if err != nil {
		return err
	}
	names := guard.AllPatternNames(extra)
	sort.Strings(names)

	w := cmd.OutOrStdout()
	for _, n := range names {
		if _, err := fmt.Fprintln(w, n); err != nil {
			return fmt.Errorf("leaks: write: %w", err)
		}
	}
	return nil
}

// leaksError wraps a scan failure. An invalid guard.secret_patterns regex is
// by far the most likely cause, and it deserves the same localized message the
// review path gives it.
func leaksError(app *appContext, err error) error {
	return errors.New(app.Catalog.T("guard.secret_patterns.invalid", err.Error()))
}

func leaksCommitLimit() int {
	if global.maxCommits > 0 {
		return global.maxCommits
	}
	return leaksDefaultMaxCommits
}
