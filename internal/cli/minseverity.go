// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// parseMinSeverity maps the raw --min-severity flag to a display
// threshold. "" / "none" disables the filter. The five canonical levels
// enable it; anything else is an error so a typo surfaces instead of
// silently showing everything. Mirrors parseFailOn's strictness.
func parseMinSeverity(raw string) (threshold render.Severity, enabled bool, err error) {
	switch s := render.Severity(strings.ToLower(strings.TrimSpace(raw))); s {
	case "", "none":
		return "", false, nil
	case render.SeverityCritical, render.SeverityHigh, render.SeverityMedium, render.SeverityLow, render.SeverityInfo:
		return s, true, nil
	default:
		return "", false, fmt.Errorf("invalid --min-severity value %q (expected: critical, high, medium, low, info, none)", raw)
	}
}

// filterMinSeverity returns the findings at or above the --min-severity
// threshold (lower severityRank = more severe, so "high" keeps critical
// + high). It is a DISPLAY-only filter: when off, invalid, or given a
// nil slice it returns the input unchanged. Callers validate the flag up
// front (runReview) so the invalid case never reaches here on the happy
// path, and --fail-on always evaluates the full, unfiltered set so the
// CI gate is never weakened by a display filter.
func filterMinSeverity(findings []render.Finding) []render.Finding {
	threshold, enabled, err := parseMinSeverity(global.minSeverity)
	if !enabled || err != nil || findings == nil {
		return findings
	}
	tr := severityRank[threshold]
	out := make([]render.Finding, 0, len(findings))
	for _, f := range findings {
		if rank, ok := severityRank[f.Severity]; ok && rank <= tr {
			out = append(out, f)
		}
	}
	return out
}
