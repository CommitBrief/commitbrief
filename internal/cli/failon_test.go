package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// ---------- parseFailOn ----------

func TestParseFailOnAcceptedValues(t *testing.T) {
	cases := []struct {
		in            string
		wantEnabled   bool
		wantAny       bool
		wantThreshold render.Severity
	}{
		{"", false, false, ""},
		{"none", false, false, ""},
		{"NONE", false, false, ""}, // case-insensitive
		{"any", true, true, ""},
		{"ANY", true, true, ""},
		{"critical", true, false, render.SeverityCritical},
		{"high", true, false, render.SeverityHigh},
		{"medium", true, false, render.SeverityMedium},
		{"low", true, false, render.SeverityLow},
		{"info", true, false, render.SeverityInfo},
		{"  high  ", true, false, render.SeverityHigh}, // whitespace tolerated
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseFailOn(tc.in)
			if err != nil {
				t.Fatalf("parseFailOn(%q) returned error: %v", tc.in, err)
			}
			if got.enabled != tc.wantEnabled {
				t.Errorf("enabled = %v, want %v", got.enabled, tc.wantEnabled)
			}
			if got.anyMode != tc.wantAny {
				t.Errorf("anyMode = %v, want %v", got.anyMode, tc.wantAny)
			}
			if got.threshold != tc.wantThreshold {
				t.Errorf("threshold = %q, want %q", got.threshold, tc.wantThreshold)
			}
		})
	}
}

func TestParseFailOnRejectsTypos(t *testing.T) {
	bad := []string{"warn", "warning", "blocker", "fatal", "yes", "true", "1", "high-or-above"}
	for _, in := range bad {
		t.Run(in, func(t *testing.T) {
			_, err := parseFailOn(in)
			if err == nil {
				t.Errorf("parseFailOn(%q) should error; got nil", in)
			}
			if err != nil && !strings.Contains(err.Error(), "invalid --fail-on") {
				t.Errorf("error message should mention --fail-on; got %q", err.Error())
			}
		})
	}
}

// ---------- applyFailOn decision matrix ----------

// failOnCase models one row of the threshold matrix. Each test runs
// applyFailOn with the given flag value + findings list and asserts
// whether the resulting error is nil (review allowed) or non-nil
// (CI-style fail).
type failOnCase struct {
	name     string
	flag     string
	findings []render.Finding
	wantErr  bool
}

func TestApplyFailOnDecisionMatrix(t *testing.T) {
	mk := func(sev render.Severity) render.Finding {
		return render.Finding{Severity: sev, File: "x.go", Line: 1, Title: "t", Description: "d"}
	}
	cases := []failOnCase{
		{"disabled-empty", "", []render.Finding{mk(render.SeverityCritical)}, false},
		{"disabled-none", "none", []render.Finding{mk(render.SeverityCritical)}, false},
		// any: ANY finding triggers
		{"any-no-findings", "any", []render.Finding{}, false},
		{"any-info-only", "any", []render.Finding{mk(render.SeverityInfo)}, true},
		{"any-critical", "any", []render.Finding{mk(render.SeverityCritical)}, true},
		// critical: only critical findings trigger
		{"critical-vs-critical", "critical", []render.Finding{mk(render.SeverityCritical)}, true},
		{"critical-vs-high", "critical", []render.Finding{mk(render.SeverityHigh)}, false},
		{"critical-vs-info", "critical", []render.Finding{mk(render.SeverityInfo)}, false},
		// high: critical + high trigger
		{"high-vs-critical", "high", []render.Finding{mk(render.SeverityCritical)}, true},
		{"high-vs-high", "high", []render.Finding{mk(render.SeverityHigh)}, true},
		{"high-vs-medium", "high", []render.Finding{mk(render.SeverityMedium)}, false},
		// medium: critical/high/medium trigger
		{"medium-vs-medium", "medium", []render.Finding{mk(render.SeverityMedium)}, true},
		{"medium-vs-low", "medium", []render.Finding{mk(render.SeverityLow)}, false},
		// info: everything triggers (info is least urgent)
		{"info-vs-low", "info", []render.Finding{mk(render.SeverityLow)}, true},
		{"info-vs-info", "info", []render.Finding{mk(render.SeverityInfo)}, true},
		// mixed-severity findings — single qualifier is enough
		{"high-mixed", "high", []render.Finding{mk(render.SeverityLow), mk(render.SeverityCritical), mk(render.SeverityInfo)}, true},
		{"critical-mixed-no-crit", "critical", []render.Finding{mk(render.SeverityHigh), mk(render.SeverityMedium)}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetGlobalFlags(t)
			global.failOn = tc.flag
			cmd, _ := stubCmd(t)
			app := stubApp(t, 0)

			err := applyFailOn(cmd, app, tc.findings)
			gotErr := err != nil
			if gotErr != tc.wantErr {
				t.Errorf("applyFailOn(%q, %d findings): gotErr=%v, wantErr=%v (err=%v)",
					tc.flag, len(tc.findings), gotErr, tc.wantErr, err)
			}
		})
	}
}

func TestApplyFailOnDegradeWarnsAndPasses(t *testing.T) {
	// findings == nil represents the markdown-fallback (graceful
	// degrade) case. The policy can't be enforced without parsed
	// findings, so the CLI must warn on stderr and let the run succeed.
	resetGlobalFlags(t)
	global.failOn = "critical" // any non-disabled value to enable the check
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)

	err := applyFailOn(cmd, app, nil)
	if err != nil {
		t.Errorf("degrade case should not error; got %v", err)
	}
	if !strings.Contains(errBuf.String(), "--fail-on skipped") {
		t.Errorf("expected stderr warning about skipped check; got:\n%s", errBuf.String())
	}
}

func TestApplyFailOnDisabledIgnoresDegrade(t *testing.T) {
	// When --fail-on is not set, the degrade case should be SILENT —
	// the warning only makes sense when the user opted into the check.
	resetGlobalFlags(t)
	global.failOn = "" // disabled
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)

	err := applyFailOn(cmd, app, nil)
	if err != nil {
		t.Errorf("disabled + degrade should not error; got %v", err)
	}
	if errBuf.Len() > 0 {
		t.Errorf("disabled --fail-on should not write to stderr; got:\n%s", errBuf.String())
	}
}

func TestApplyFailOnInvalidFlagReturnsError(t *testing.T) {
	// Typos in --fail-on should surface early — before the user
	// realizes the CI was silently letting everything through.
	resetGlobalFlags(t)
	global.failOn = "blocker" // not a valid level
	cmd, _ := stubCmd(t)
	app := stubApp(t, 0)

	err := applyFailOn(cmd, app, []render.Finding{})
	if err == nil {
		t.Errorf("invalid --fail-on value should return error, got nil")
	}
}

func TestApplyFailOnErrorMessageMentionsCountAndLabel(t *testing.T) {
	// The error message returned to cobra should be CI-actionable: how
	// many findings tripped the check and at what threshold.
	resetGlobalFlags(t)
	global.failOn = "high"
	cmd, _ := stubCmd(t)
	app := stubApp(t, 0)

	findings := []render.Finding{
		{Severity: render.SeverityCritical, File: "a.go", Line: 1, Title: "t", Description: "d"},
		{Severity: render.SeverityHigh, File: "b.go", Line: 1, Title: "t", Description: "d"},
		{Severity: render.SeverityLow, File: "c.go", Line: 1, Title: "t", Description: "d"},
	}
	err := applyFailOn(cmd, app, findings)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "2") {
		t.Errorf("expected count '2' in message; got %q", msg)
	}
	if !strings.Contains(msg, "high") {
		t.Errorf("expected threshold 'high' in message; got %q", msg)
	}
}

func TestApplyFailOnAnyModeLabelInError(t *testing.T) {
	// anyMode should surface "any" in the error message rather than
	// the (empty) threshold field — informative for the CI reader.
	resetGlobalFlags(t)
	global.failOn = "any"
	cmd, _ := stubCmd(t)
	app := stubApp(t, 0)

	err := applyFailOn(cmd, app, []render.Finding{
		{Severity: render.SeverityInfo, File: "x.go", Line: 1, Title: "t", Description: "d"},
	})
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	if !strings.Contains(err.Error(), "any") {
		t.Errorf("expected 'any' in error message; got %q", err.Error())
	}
}

// Cobra import guard — the package compiles only when cobra is in
// scope from other files; this `var _` keeps the dependency visible
// even if I rip out the only direct cobra reference accidentally.
var _ = cobra.Command{}
var _ = bytes.Buffer{}
