// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/policy"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// guardFlags are the `guard`-local flags. Provider/model/no-flaky come from the
// persistent root flags (global.*); only the policy path, the consume-mode
// source, and the run-mode scope live here.
type guardFlags struct {
	policy   string
	fromJSON string
	unstaged bool
	diff     []string
}

var guardOpts guardFlags

// newGuardCmd is the `commitbrief guard` entry point (ADR-0029): a declarative
// merge-policy gate. It evaluates a review's actionable findings against
// `.commitbrief/policy.yml` and exits non-zero when a per-severity cap (or the
// total cap) is exceeded — a richer, opt-in alternative to the single
// `--fail-on=<severity>` threshold, aimed at gating high-volume (AI-authored)
// pull requests.
//
// Two input modes:
//   - run-mode (default): runs the standard review pipeline (the same one as
//     `commitbrief --json`, via the shared seam) on the resolved diff, then
//     evaluates the result;
//   - consume-mode (--from-json <file|->): reads a previously produced schema-v1
//     review document and evaluates it WITHOUT calling a provider — so an
//     agent's earlier self-review (e.g. from the MCP `review` tool) can be gated
//     cheaply.
//
// The gate sees the findings that survive baseline + inline suppression (signal
// control, ADR-0027), because that is exactly what `--json` emits.
func newGuardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "guard",
		Short: "Gate a review against a declarative policy (.commitbrief/policy.yml)",
		Long: "Evaluate a review's actionable findings against a declarative policy " +
			"and exit non-zero when it is breached — a richer, opt-in alternative to " +
			"--fail-on for gating (often AI-authored) pull requests.\n\n" +
			"The policy (.commitbrief/policy.yml) caps how many findings of each " +
			"severity a change may carry (plus an optional total cap), and can deny " +
			"or waive findings by rule id (rules.deny / rules.allow). Unlike " +
			"--fail-on (a single threshold), guard enforces a per-severity budget and " +
			"can consume a prior review's JSON via --from-json without re-running the " +
			"provider. It evaluates the set that survives baseline + suppression.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGuard(cmd)
		},
	}
	f := cmd.Flags()
	f.StringVar(&guardOpts.policy, "policy", ".commitbrief/policy.yml", "path to the policy file")
	f.StringVar(&guardOpts.fromJSON, "from-json", "", "evaluate a prior schema-v1 review JSON (a file path, or - for stdin) instead of running a review")
	f.BoolVar(&guardOpts.unstaged, "unstaged", false, "run-mode: review the unstaged working tree instead of the staged index")
	f.StringSliceVar(&guardOpts.diff, "diff", nil, "run-mode: review an arbitrary `git diff` range (e.g. main...HEAD); repeatable")
	bindSarifFlagUsage(cmd, "emit the findings that breach the policy as SARIF 2.1.0 for GitHub Code Scanning (category commitbrief/guard/; use -o to write a file; the verdict goes to stderr; not with --json/--markdown/--suggest-commit/--cli/--show-prompt)")
	return cmd
}

// runGuard resolves the policy, obtains the review JSON (run- or consume-mode),
// evaluates it, renders the verdict, and returns a non-nil error when blocked so
// Execute exits 1 (CI gate). A pass returns nil → exit 0. A usage/IO/parse
// failure also returns an error → exit 1; for a merge gate, "could not prove the
// change is within policy" must block just like an explicit breach.
func runGuard(cmd *cobra.Command) error {
	app, err := resolveContext(false)
	if err != nil {
		return err
	}
	// A misused --sarif fails before the policy is read or any provider call.
	if err := validateSarifFlags(app); err != nil {
		return err
	}
	// Bounds the whole gate, including the review it drives through the MCP
	// seam (which reads cmd.Context()).
	ctx, cancel := app.withTimeout(cmd.Context())
	defer cancel()
	cmd.SetContext(ctx)

	policyPath := resolveGuardPolicyPath(cmd, app.RepoRoot)
	pol, err := policy.Load(policyPath)
	if err != nil {
		if errors.Is(err, policy.ErrNotFound) {
			return fmt.Errorf("%s", app.Catalog.T("guard.no_policy", policyPath))
		}
		return err
	}

	reviewJSON, err := guardReviewJSON(cmd)
	if err != nil {
		return err
	}
	findings, err := render.ParseFindings(reviewJSON)
	if err != nil {
		return fmt.Errorf("%s: %w", app.Catalog.T("guard.bad_review"), err)
	}

	verdict := pol.Evaluate(findings)
	verdictOut := cmd.OutOrStdout()
	if global.sarif {
		// Fail closed (ADR-0047 §4): ParseFindings reads a degraded review as
		// "no findings", and an empty results[] would tell Code Scanning every
		// open guard alert is fixed. Refuse before anything is written.
		if reviewDocDegraded(reviewJSON, findings) {
			return sarifDegradeError(app)
		}
		if err := writeGuardSarif(cmd, pol.Offending(findings)); err != nil {
			return err
		}
		// stdout (or -o) belongs to the SARIF document; the human verdict
		// moves to stderr. The exit code still comes from the verdict.
		verdictOut = cmd.ErrOrStderr()
	}
	if err := renderGuardVerdict(verdictOut, app, verdict); err != nil {
		return err
	}
	if !verdict.Passed {
		return fmt.Errorf("%s", app.Catalog.T("guard.blocked", len(verdict.Violations)))
	}
	return nil
}

// writeGuardSarif writes the findings that cause a violation as SARIF under the
// guard category (stdout, or -o). A passing policy passes none and so writes
// results: [], which is the truthful "nothing to fix" for this category.
func writeGuardSarif(cmd *cobra.Command, offending []render.Finding) error {
	w, closer, err := openOutput(cmd)
	if err != nil {
		return err
	}
	defer closer()
	return render.SARIFWithCategory(w, offending, render.SARIFCategoryGuard, nil)
}

// reviewDocDegraded reports whether a schema-v1 review document is a degraded
// run rather than a clean one. render.JSON puts the raw model response in
// content only when the findings never parsed, and stamps meta.degrade_reason
// on a live degrade; ParseFindings ignores both, so a degraded document reads
// as an empty finding list. A document that carries findings is never degraded
// (older documents may keep a vestigial content next to them).
func reviewDocDegraded(reviewJSON string, findings []render.Finding) bool {
	var doc struct {
		Content string `json:"content"`
		Meta    struct {
			DegradeReason string `json:"degrade_reason"`
		} `json:"meta"`
	}
	if err := json.Unmarshal([]byte(reviewJSON), &doc); err != nil {
		return true // cannot prove it is clean; fail closed
	}
	if doc.Meta.DegradeReason != "" {
		return true
	}
	return len(findings) == 0 && strings.TrimSpace(doc.Content) != ""
}

// resolveGuardPolicyPath joins the default policy file to the repo root so
// `guard` works from any subdirectory, while an explicitly-set --policy is used
// verbatim (relative to the CWD).
func resolveGuardPolicyPath(cmd *cobra.Command, repoRoot string) string {
	changed := cmd.Flags().Changed("policy")
	if !changed && repoRoot != "" {
		return filepath.Join(repoRoot, ".commitbrief", "policy.yml")
	}
	return guardOpts.policy
}

// guardReviewJSON returns the schema-v1 review document: from --from-json in
// consume-mode, or by running the review pipeline in run-mode (the shared
// runReviewForMCP seam, with no fail-on so the gate — not the review — decides).
func guardReviewJSON(cmd *cobra.Command) (string, error) {
	if guardOpts.fromJSON != "" {
		var data []byte
		var err error
		if guardOpts.fromJSON == "-" {
			data, err = io.ReadAll(cmd.InOrStdin())
		} else {
			data, err = os.ReadFile(guardOpts.fromJSON)
		}
		if err != nil {
			return "", fmt.Errorf("read review json: %w", err)
		}
		return string(data), nil
	}

	args := reviewToolArgs{
		Unstaged: guardOpts.unstaged,
		Diff:     guardOpts.diff,
		Provider: strings.TrimSpace(global.provider),
		Model:    strings.TrimSpace(global.model),
		NoFlaky:  global.noFlaky,
		// FailOn deliberately left empty: the policy gate decides the verdict,
		// not the review's own --fail-on.

		// The MCP seam wipes the global flag state, so the path (ADR-0026)
		// and commit (ADR-0035) filters have to be carried across explicitly
		// or a `guard --author alice` would gate on an unfiltered review.
		File:        global.files,
		Dir:         global.dirs,
		ExcludeFile: global.excludeFiles,
		ExcludeDir:  global.excludeDirs,
		Author:      global.authors,
		Committer:   global.committers,
		StartDate:   strings.TrimSpace(global.startDate),
		EndDate:     strings.TrimSpace(global.endDate),
		Text:        strings.TrimSpace(global.text),
		MaxCommits:  global.maxCommits,
		Merges:      global.merges,
	}
	// guard --unstaged together with a commit filter is left to fail in
	// buildCommitFilter, which reports the scope conflict with the same
	// message a review would — one rule, one message.
	// The seam forces --quiet, so hand it a sink that forwards signal-control
	// warnings (e.g. the legacy baseline notice) to this command's stderr,
	// honoring the user's own --quiet.
	quiet := global.quiet
	ctx := withWarningSink(cmd.Context(), func(line string) {
		if !quiet {
			_, _ = fmt.Fprintln(cmd.ErrOrStderr(), line)
		}
	}, nil)
	_, reviewJSON, err := runReviewForMCP(ctx, args)
	if err != nil {
		return "", err
	}
	return reviewJSON, nil
}

// renderGuardVerdict writes the verdict: the JSON object in --json mode, or a
// human summary otherwise. Both go to out: stdout normally, stderr when --sarif
// has claimed stdout for the SARIF document.
func renderGuardVerdict(out io.Writer, app *appContext, v policy.Verdict) error {
	if global.json {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(v); err != nil {
			return fmt.Errorf("encode verdict: %w", err)
		}
		return nil
	}
	if global.quiet {
		return nil
	}
	order := []render.Severity{
		render.SeverityCritical, render.SeverityHigh, render.SeverityMedium,
		render.SeverityLow, render.SeverityInfo,
	}
	parts := make([]string, 0, len(order))
	for _, s := range order {
		parts = append(parts, fmt.Sprintf("%s=%d", s, v.Counts[string(s)]))
	}
	var lines []string
	if v.Passed {
		lines = append(lines, app.Catalog.T("guard.pass"))
	} else {
		lines = append(lines, app.Catalog.T("guard.blocked", len(v.Violations)))
		// Rule violations are one per finding (ADR-0045 §6) and carry no
		// location, so identical ids would print as indistinguishable
		// lines; fold them into one line with a finding count.
		ruleCounts := map[string]int{}
		for _, vio := range v.Violations {
			if id, ok := strings.CutPrefix(vio.Severity, "rule:"); ok {
				ruleCounts[id]++
			}
		}
		for _, vio := range v.Violations {
			if id, ok := strings.CutPrefix(vio.Severity, "rule:"); ok {
				n, seen := ruleCounts[id]
				if !seen {
					continue
				}
				delete(ruleCounts, id)
				if n > 1 {
					lines = append(lines, "  "+app.Catalog.T("guard.rule_violation_count", id, n))
				} else {
					lines = append(lines, "  "+app.Catalog.T("guard.rule_violation_line", id))
				}
				continue
			}
			lines = append(lines, "  "+app.Catalog.T("guard.violation_line", vio.Severity, vio.Actual, vio.Allowed))
		}
	}
	if v.Waived > 0 {
		lines = append(lines, app.Catalog.T("guard.waived", v.Waived))
	}
	lines = append(lines, fmt.Sprintf("%s (%s, total %d)", app.Catalog.T("guard.counts"), strings.Join(parts, " "), v.Total))
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("write verdict: %w", err)
	}
	return nil
}
