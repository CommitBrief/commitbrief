// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// failOnPolicy is the parsed shape of the --fail-on flag. enabled=false
// means the flag was empty or "none" — the CLI returns no error
// regardless of findings (the historical v0.7.0 behavior). anyMode
// means a single finding of any severity is enough to fail; otherwise
// threshold is the minimum-severity that triggers the failure.
type failOnPolicy struct {
	enabled   bool
	anyMode   bool
	threshold render.Severity
}

// severityRank lets us compare two severities numerically. Lower rank =
// more urgent. Kept private to the cli package because the comparison
// is policy-driven (user picks a flag), not a render-layer concern.
var severityRank = map[render.Severity]int{
	render.SeverityCritical: 0,
	render.SeverityHigh:     1,
	render.SeverityMedium:   2,
	render.SeverityLow:      3,
	render.SeverityInfo:     4,
}

// parseFailOn maps the raw --fail-on flag string to a policy. Accepts
// (case-insensitively): "", "none" (off); "any" (any finding fails);
// the five canonical severity levels. Any other value is an error so
// CI authors notice typos before deploying a green pipeline that was
// actually skipping the check.
func parseFailOn(raw string) (failOnPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "none":
		return failOnPolicy{}, nil
	case "any":
		return failOnPolicy{enabled: true, anyMode: true}, nil
	case "critical":
		return failOnPolicy{enabled: true, threshold: render.SeverityCritical}, nil
	case "high":
		return failOnPolicy{enabled: true, threshold: render.SeverityHigh}, nil
	case "medium":
		return failOnPolicy{enabled: true, threshold: render.SeverityMedium}, nil
	case "low":
		return failOnPolicy{enabled: true, threshold: render.SeverityLow}, nil
	case "info":
		return failOnPolicy{enabled: true, threshold: render.SeverityInfo}, nil
	default:
		return failOnPolicy{}, fmt.Errorf("invalid --fail-on value %q (expected: critical, high, medium, low, info, any, none)", raw)
	}
}

// applyFailOn enforces the --fail-on policy after the review is
// rendered. Returns nil on no-op (policy disabled, no qualifying
// findings); returns an error on threshold breach so cobra exits 1.
//
// Graceful-degrade is intentionally a pass: when findings is nil the
// LLM produced unparseable output and we don't know its content. Surf-
// ing a CI failure off that would be worse than letting the run
// succeed and surface the rendered fallback text. Emit a stderr
// warning so the user knows the policy was honored on the happy path
// but skipped here.
func applyFailOn(cmd *cobra.Command, app *appContext, findings []render.Finding) error {
	policy, err := parseFailOn(global.failOn)
	if err != nil {
		return err
	}
	if !policy.enabled {
		return nil
	}
	if findings == nil {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), app.Catalog.T("fail_on.degraded_skipped"))
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
	label := string(policy.threshold)
	if policy.anyMode {
		label = "any"
	}
	return errors.New(app.Catalog.T("fail_on.threshold_reached", matches, label))
}
