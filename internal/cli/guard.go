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
			"severity a change may carry (plus an optional total cap). Unlike " +
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
	if err := renderGuardVerdict(cmd, app, verdict); err != nil {
		return err
	}
	if !verdict.Passed {
		return fmt.Errorf("%s", app.Catalog.T("guard.blocked", len(verdict.Violations)))
	}
	return nil
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
	}
	_, reviewJSON, err := runReviewForMCP(cmd.Context(), args)
	if err != nil {
		return "", err
	}
	return reviewJSON, nil
}

// renderGuardVerdict writes the verdict: the JSON object in --json mode, or a
// human summary otherwise. Both go to stdout (the command's primary output).
func renderGuardVerdict(cmd *cobra.Command, app *appContext, v policy.Verdict) error {
	out := cmd.OutOrStdout()
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
		for _, vio := range v.Violations {
			lines = append(lines, "  "+app.Catalog.T("guard.violation_line", vio.Severity, vio.Actual, vio.Allowed))
		}
	}
	lines = append(lines, fmt.Sprintf("%s (%s, total %d)", app.Catalog.T("guard.counts"), strings.Join(parts, " "), v.Total))
	if _, err := fmt.Fprintln(out, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("write verdict: %w", err)
	}
	return nil
}
