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

func ruled(sev render.Severity, ruleID string) render.Finding {
	f := finding(sev)
	f.RuleID = ruleID
	return f
}

func TestParseAcceptsRulesBlock(t *testing.T) {
	p, err := Parse([]byte("version: 1\nrules:\n  deny: [sec.injection, \" SEC.* \"]\n  allow: [style.naming]\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(p.Rules.Deny) != 2 || p.Rules.Deny[0] != "sec.injection" || p.Rules.Deny[1] != "sec.*" {
		t.Errorf("deny = %v, want normalised [sec.injection sec.*]", p.Rules.Deny)
	}
	if len(p.Rules.Allow) != 1 || p.Rules.Allow[0] != "style.naming" {
		t.Errorf("allow = %v", p.Rules.Allow)
	}
}

func TestParseRejectsBadRuleEntries(t *testing.T) {
	cases := map[string]string{
		"unknown id":         "version: 1\nrules:\n  deny: [sec.nonsense]\n",
		"unknown category":   "version: 1\nrules:\n  allow: [nope.*]\n",
		"bare word":          "version: 1\nrules:\n  deny: [security]\n",
		"empty entry":        "version: 1\nrules:\n  deny: [\"\"]\n",
		"same id both lists": "version: 1\nrules:\n  deny: [sec.injection]\n  allow: [sec.injection]\n",
		"unknown rules key":  "version: 1\nrules:\n  block: [sec.injection]\n",
	}
	for name, body := range cases {
		if _, err := Parse([]byte(body)); err == nil {
			t.Errorf("%s: expected a parse error, got nil", name)
		}
	}
}

func TestEvaluateDenyBlocksOnSingleFinding(t *testing.T) {
	// No thresholds at all: a single denied-rule finding still fails.
	p := mustParse(t, "version: 1\nrules:\n  deny: [sec.injection]\n")
	v := p.Evaluate([]render.Finding{ruled(render.SeverityInfo, "sec.injection"), ruled(render.SeverityLow, "style.naming")})
	if v.Passed {
		t.Fatal("expected block on a denied rule")
	}
	if len(v.Violations) != 1 {
		t.Fatalf("violations = %+v, want exactly the rule violation", v.Violations)
	}
	got := v.Violations[0]
	if got.Severity != "rule:sec.injection" || got.Allowed != 0 || got.Actual != 1 {
		t.Fatalf("violation = %+v", got)
	}
}

func TestEvaluateDenyCategoryWildcard(t *testing.T) {
	p := mustParse(t, "version: 1\nrules:\n  deny: [sec.*]\n")
	v := p.Evaluate([]render.Finding{ruled(render.SeverityLow, "sec.xss"), ruled(render.SeverityLow, "sec.ssrf")})
	// Each matching finding is its own violation named after the finding's
	// rule id, not after the wildcard entry (ADR-0045 §6).
	want := []string{"rule:sec.ssrf", "rule:sec.xss"}
	if v.Passed || len(v.Violations) != len(want) {
		t.Fatalf("verdict = %+v", v)
	}
	for i, w := range want {
		got := v.Violations[i]
		if got.Severity != w || got.Allowed != 0 || got.Actual != 1 {
			t.Errorf("violation[%d] = %+v, want %s allowed 0 actual 1", i, got, w)
		}
	}
}

func TestEvaluateDenyOneViolationPerFinding(t *testing.T) {
	p := mustParse(t, "version: 1\nrules:\n  deny: [sec.*]\n")
	v := p.Evaluate([]render.Finding{ruled(render.SeverityLow, "sec.injection"), ruled(render.SeverityHigh, "sec.injection")})
	if len(v.Violations) != 2 {
		t.Fatalf("violations = %+v, want one per finding", v.Violations)
	}
	for i, got := range v.Violations {
		if got.Severity != "rule:sec.injection" || got.Actual != 1 {
			t.Errorf("violation[%d] = %+v", i, got)
		}
	}
}

func TestEvaluateOverlappingDenyEntriesYieldOneViolationPerFinding(t *testing.T) {
	// A wildcard and an exact entry both cover the finding: still one violation.
	p := mustParse(t, "version: 1\nrules:\n  deny: [sec.*, sec.injection]\n")
	v := p.Evaluate([]render.Finding{ruled(render.SeverityLow, "sec.injection")})
	if len(v.Violations) != 1 || v.Violations[0].Severity != "rule:sec.injection" || v.Violations[0].Actual != 1 {
		t.Fatalf("violations = %+v", v.Violations)
	}
}

func TestEvaluateDuplicateDenyEntriesDoNotInflateCount(t *testing.T) {
	p := mustParse(t, "version: 1\nrules:\n  deny: [sec.injection, SEC.injection, sec.injection]\n")
	if len(p.Rules.Deny) != 1 {
		t.Fatalf("deny = %v, want deduplicated to one entry", p.Rules.Deny)
	}
	v := p.Evaluate([]render.Finding{ruled(render.SeverityLow, "sec.injection")})
	if len(v.Violations) != 1 || v.Violations[0].Actual != 1 {
		t.Fatalf("violations = %+v", v.Violations)
	}
}

func TestEvaluateUnstampedFindingCountsAsGeneralOther(t *testing.T) {
	p := mustParse(t, "version: 1\nrules:\n  deny: [general.other]\n")
	v := p.Evaluate([]render.Finding{ruled(render.SeverityLow, "")})
	if v.Passed {
		t.Fatalf("a finding without a rule id must count as general.other, got %+v", v)
	}
}

func TestEvaluateAllowWaivesBelowThreshold(t *testing.T) {
	// Two high findings would breach `high: 1`; one is allow-listed, so the
	// remaining count (1) is within the cap.
	p := mustParse(t, "version: 1\nthresholds:\n  high: 1\ntotal: 1\nrules:\n  allow: [style.naming]\n")
	v := p.Evaluate([]render.Finding{
		ruled(render.SeverityHigh, "style.naming"),
		ruled(render.SeverityHigh, "sec.injection"),
	})
	if !v.Passed {
		t.Fatalf("expected pass once the waived finding is excluded, got %+v", v.Violations)
	}
	if v.Waived != 1 {
		t.Errorf("waived = %d, want 1", v.Waived)
	}
	if v.Total != 1 || v.Counts["high"] != 1 {
		t.Errorf("total=%d counts=%v: waived findings must not be counted", v.Total, v.Counts)
	}
}

func TestEvaluateDenyWinsOverAllow(t *testing.T) {
	// The category is allowed, one id inside it is denied: deny wins.
	p := mustParse(t, "version: 1\nrules:\n  allow: [sec.*]\n  deny: [sec.injection]\n")
	v := p.Evaluate([]render.Finding{ruled(render.SeverityLow, "sec.injection"), ruled(render.SeverityLow, "sec.xss")})
	if v.Passed {
		t.Fatal("deny must win over a broader allow")
	}
	if v.Waived != 1 {
		t.Errorf("waived = %d, want 1 (only sec.xss is waived)", v.Waived)
	}
}

func TestEvaluateRuleViolationOrdering(t *testing.T) {
	p := mustParse(t, "version: 1\nthresholds:\n  high: 0\ntotal: 0\nrules:\n  deny: [style.naming, sec.injection]\n")
	v := p.Evaluate([]render.Finding{
		ruled(render.SeverityHigh, "sec.injection"),
		ruled(render.SeverityHigh, "style.naming"),
	})
	want := []string{"high", "total", "rule:sec.injection", "rule:style.naming"}
	if len(v.Violations) != len(want) {
		t.Fatalf("violations = %+v, want %v", v.Violations, want)
	}
	for i, w := range want {
		if v.Violations[i].Severity != w {
			t.Errorf("violation[%d] = %q, want %q", i, v.Violations[i].Severity, w)
		}
	}
}

func TestVersion1PolicyWithoutRulesIsUnchanged(t *testing.T) {
	// A pre-`rules` policy must produce the same verdict it always did, with
	// nothing waived, whatever rule ids the findings carry.
	p := mustParse(t, "version: 1\nthresholds:\n  critical: 0\n  medium: 1\ntotal: 3\n")
	findings := []render.Finding{
		ruled(render.SeverityCritical, "sec.injection"),
		ruled(render.SeverityMedium, "style.naming"),
		finding(render.SeverityMedium),
		finding(render.SeverityLow),
	}
	v := p.Evaluate(findings)
	if v.Passed || v.Waived != 0 || v.Total != 4 {
		t.Fatalf("verdict = %+v", v)
	}
	want := []Violation{
		{Severity: "critical", Allowed: 0, Actual: 1},
		{Severity: "medium", Allowed: 1, Actual: 2},
		{Severity: "total", Allowed: 3, Actual: 4},
	}
	if len(v.Violations) != len(want) {
		t.Fatalf("violations = %+v, want %+v", v.Violations, want)
	}
	for i, w := range want {
		if v.Violations[i] != w {
			t.Errorf("violation[%d] = %+v, want %+v", i, v.Violations[i], w)
		}
	}
}
