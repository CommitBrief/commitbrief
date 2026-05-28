// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

func TestParseMinSeverity(t *testing.T) {
	for _, raw := range []string{"", "none", "NONE", "  "} {
		if _, enabled, err := parseMinSeverity(raw); err != nil || enabled {
			t.Errorf("parseMinSeverity(%q) = (enabled=%v, %v), want disabled/no-err", raw, enabled, err)
		}
	}
	ok := map[string]render.Severity{
		"critical": render.SeverityCritical,
		"HIGH":     render.SeverityHigh,
		" medium ": render.SeverityMedium,
		"low":      render.SeverityLow,
		"info":     render.SeverityInfo,
	}
	for raw, want := range ok {
		got, enabled, err := parseMinSeverity(raw)
		if err != nil || !enabled || got != want {
			t.Errorf("parseMinSeverity(%q) = (%q, %v, %v), want (%q, true, nil)", raw, got, enabled, err, want)
		}
	}
	for _, bad := range []string{"blocker", "warn", "any", "1"} {
		if _, _, err := parseMinSeverity(bad); err == nil {
			t.Errorf("parseMinSeverity(%q) should error", bad)
		}
	}
}

func sevFindings(sevs ...render.Severity) []render.Finding {
	out := make([]render.Finding, 0, len(sevs))
	for i, s := range sevs {
		out = append(out, render.Finding{Severity: s, File: "f.go", Line: i + 1, Title: "t", Description: "d", Suggestion: "s"})
	}
	return out
}

func TestFilterMinSeverity(t *testing.T) {
	prev := global.minSeverity
	t.Cleanup(func() { global.minSeverity = prev })

	all := sevFindings(render.SeverityCritical, render.SeverityHigh, render.SeverityMedium, render.SeverityLow, render.SeverityInfo)

	// Disabled → unchanged.
	global.minSeverity = ""
	if got := filterMinSeverity(all); len(got) != 5 {
		t.Errorf("disabled filter should keep all 5, got %d", len(got))
	}

	// high → critical + high only.
	global.minSeverity = "high"
	got := filterMinSeverity(all)
	if len(got) != 2 || got[0].Severity != render.SeverityCritical || got[1].Severity != render.SeverityHigh {
		t.Errorf("min=high should keep [critical, high], got %v", severitiesOf(got))
	}

	// info → keep all (lowest threshold).
	global.minSeverity = "info"
	if got := filterMinSeverity(all); len(got) != 5 {
		t.Errorf("min=info should keep all 5, got %d", len(got))
	}

	// critical → only critical.
	global.minSeverity = "critical"
	if got := filterMinSeverity(sevFindings(render.SeverityHigh, render.SeverityCritical, render.SeverityLow)); len(got) != 1 || got[0].Severity != render.SeverityCritical {
		t.Errorf("min=critical should keep only critical, got %v", severitiesOf(got))
	}

	// nil → nil (degrade case).
	if got := filterMinSeverity(nil); got != nil {
		t.Errorf("nil findings should stay nil, got %v", got)
	}
}

func severitiesOf(fs []render.Finding) []render.Severity {
	out := make([]render.Severity, len(fs))
	for i, f := range fs {
		out[i] = f.Severity
	}
	return out
}

// The mock provider returns a single "info" finding titled "mock review
// output". --min-severity=high should hide it from the rendered output.
func TestMinSeverityHidesBelowThresholdInRender(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--min-severity=high"); err != nil {
		t.Fatalf("review: %v", err)
	}
	if strings.Contains(e.out.String(), "mock review output") {
		t.Errorf("info finding should be hidden by --min-severity=high; got:\n%s", e.out.String())
	}
}

// --fail-on must evaluate the FULL set: --min-severity=high hides the
// info finding from display, but --fail-on=info still trips the gate.
func TestMinSeverityDoesNotWeakenFailOn(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("--staged", "--min-severity=high", "--fail-on=info")
	if err == nil {
		t.Fatal("--fail-on=info must still fire on the hidden info finding (full set), but exit was 0")
	}
}

// --json is the machine contract and stays complete regardless of the
// display filter.
func TestMinSeverityDoesNotFilterJSON(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--min-severity=high", "--json"); err != nil {
		t.Fatalf("review --json: %v", err)
	}
	if !strings.Contains(e.out.String(), "mock review output") {
		t.Errorf("--json must keep the full finding set despite --min-severity; got:\n%s", e.out.String())
	}
}

// A typo fails fast (before the provider call) rather than silently
// disabling the filter.
func TestMinSeverityInvalidValueErrors(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--min-severity=hgih"); err == nil {
		t.Fatal("invalid --min-severity should error")
	}
}
