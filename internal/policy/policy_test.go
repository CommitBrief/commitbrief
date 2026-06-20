// SPDX-License-Identifier: GPL-3.0-or-later

package policy

import (
	"errors"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

func TestParseValid(t *testing.T) {
	p, err := Parse([]byte("version: 1\nthresholds:\n  critical: 0\n  medium: 5\n  low: ~\ntotal: 20\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Version != 1 {
		t.Errorf("version = %d, want 1", p.Version)
	}
	if got := p.Thresholds[render.SeverityCritical]; got == nil || *got != 0 {
		t.Errorf("critical threshold = %v, want 0", got)
	}
	if got, ok := p.Thresholds[render.SeverityLow]; !ok || got != nil {
		t.Errorf("low threshold = %v (ok=%v), want present+nil (unlimited)", got, ok)
	}
	if p.Total == nil || *p.Total != 20 {
		t.Errorf("total = %v, want 20", p.Total)
	}
}

func TestParseRejectsUnknownKey(t *testing.T) {
	_, err := Parse([]byte("version: 1\nthreshold:\n  critical: 0\n")) // "threshold" typo
	if err == nil {
		t.Fatal("expected error for unknown key, got nil")
	}
}

func TestParseRejectsBadVersion(t *testing.T) {
	if _, err := Parse([]byte("version: 2\n")); err == nil {
		t.Fatal("expected error for version 2, got nil")
	}
}

func TestParseRejectsUnknownSeverity(t *testing.T) {
	if _, err := Parse([]byte("version: 1\nthresholds:\n  blocker: 0\n")); err == nil {
		t.Fatal("expected error for unknown severity, got nil")
	}
}

func TestParseRejectsNegativeThreshold(t *testing.T) {
	if _, err := Parse([]byte("version: 1\nthresholds:\n  high: -1\n")); err == nil {
		t.Fatal("expected error for negative threshold, got nil")
	}
}

func finding(sev render.Severity) render.Finding {
	return render.Finding{Severity: sev, File: "a.go", Line: 1, Title: "t", Description: "d", Suggestion: "s"}
}

func TestEvaluatePass(t *testing.T) {
	p := mustParse(t, "version: 1\nthresholds:\n  critical: 0\n  medium: 2\n")
	v := p.Evaluate([]render.Finding{finding(render.SeverityMedium), finding(render.SeverityLow)})
	if !v.Passed {
		t.Fatalf("expected pass, got violations %+v", v.Violations)
	}
	if v.Total != 2 {
		t.Errorf("total = %d, want 2", v.Total)
	}
	if v.Counts["medium"] != 1 || v.Counts["low"] != 1 {
		t.Errorf("counts = %v", v.Counts)
	}
}

func TestEvaluateBlocksOnSeverityCap(t *testing.T) {
	p := mustParse(t, "version: 1\nthresholds:\n  critical: 0\n")
	v := p.Evaluate([]render.Finding{finding(render.SeverityCritical)})
	if v.Passed {
		t.Fatal("expected block on critical>0")
	}
	if len(v.Violations) != 1 || v.Violations[0].Severity != "critical" || v.Violations[0].Allowed != 0 || v.Violations[0].Actual != 1 {
		t.Fatalf("violations = %+v", v.Violations)
	}
}

func TestEvaluateBlocksOnTotal(t *testing.T) {
	p := mustParse(t, "version: 1\ntotal: 1\n")
	v := p.Evaluate([]render.Finding{finding(render.SeverityLow), finding(render.SeverityInfo)})
	if v.Passed {
		t.Fatal("expected block on total>1")
	}
	if v.Violations[len(v.Violations)-1].Severity != "total" {
		t.Fatalf("expected a total violation, got %+v", v.Violations)
	}
}

func TestEvaluateUnlimitedSeverity(t *testing.T) {
	// No thresholds + no total → everything passes.
	p := mustParse(t, "version: 1\n")
	v := p.Evaluate([]render.Finding{finding(render.SeverityCritical), finding(render.SeverityCritical)})
	if !v.Passed {
		t.Fatalf("expected pass with no limits, got %+v", v.Violations)
	}
}

func TestEvaluateOrdersViolations(t *testing.T) {
	p := mustParse(t, "version: 1\nthresholds:\n  high: 0\n  critical: 0\n  medium: 0\n")
	v := p.Evaluate([]render.Finding{finding(render.SeverityMedium), finding(render.SeverityCritical), finding(render.SeverityHigh)})
	if len(v.Violations) != 3 {
		t.Fatalf("want 3 violations, got %d", len(v.Violations))
	}
	want := []string{"critical", "high", "medium"}
	for i, w := range want {
		if v.Violations[i].Severity != w {
			t.Errorf("violation[%d] = %q, want %q", i, v.Violations[i].Severity, w)
		}
	}
}

func TestLoadNotFound(t *testing.T) {
	_, err := Load("/no/such/policy/file.yml")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func mustParse(t *testing.T, s string) *Policy {
	t.Helper()
	p, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("Parse(%q): %v", s, err)
	}
	return p
}
