// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/remote"
	"github.com/CommitBrief/commitbrief/internal/render"
)

func TestParseRequestChangesOn(t *testing.T) {
	ok := map[string]render.Severity{
		"critical": render.SeverityCritical,
		"HIGH":     render.SeverityHigh,
		" medium ": render.SeverityMedium,
		"low":      render.SeverityLow,
	}
	for raw, want := range ok {
		got, err := parseRequestChangesOn(raw)
		if err != nil || got != want {
			t.Errorf("parseRequestChangesOn(%q) = (%q, %v), want (%q, nil)", raw, got, err, want)
		}
	}
	if _, err := parseRequestChangesOn("info"); !errors.Is(err, errRequestChangesInfo) {
		t.Errorf("info should map to errRequestChangesInfo, got %v", err)
	}
	for _, bad := range []string{"", "blocker", "warn", "none"} {
		if _, err := parseRequestChangesOn(bad); !errors.Is(err, errRequestChangesInvalid) {
			t.Errorf("parseRequestChangesOn(%q) should be invalid, got %v", bad, err)
		}
	}
}

func findingsOf(sevs ...render.Severity) []render.Finding {
	out := make([]render.Finding, 0, len(sevs))
	for i, s := range sevs {
		out = append(out, render.Finding{
			Severity:    s,
			File:        "f.go",
			Line:        i + 1,
			Title:       "t",
			Description: "d",
			Suggestion:  "s",
		})
	}
	return out
}

func repeatSeverity(s render.Severity, n int) []render.Severity {
	out := make([]render.Severity, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestComputeVerdict(t *testing.T) {
	cases := []struct {
		name      string
		findings  []render.Finding
		threshold render.Severity
		want      remote.Verdict
	}{
		{"no findings", nil, render.SeverityCritical, remote.VerdictApprove},
		{"only info under critical", findingsOf(render.SeverityInfo, render.SeverityInfo), render.SeverityCritical, remote.VerdictApprove},
		{"critical reaches critical", findingsOf(render.SeverityCritical), render.SeverityCritical, remote.VerdictRequestChanges},
		{"high under critical -> comment", findingsOf(render.SeverityHigh), render.SeverityCritical, remote.VerdictComment},
		{"high reaches high", findingsOf(render.SeverityHigh), render.SeverityHigh, remote.VerdictRequestChanges},
		{"low+info under critical -> comment", findingsOf(render.SeverityLow, render.SeverityInfo), render.SeverityCritical, remote.VerdictComment},
	}
	for _, c := range cases {
		if got := computeVerdict(c.findings, c.threshold); got != c.want {
			t.Errorf("%s: computeVerdict = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestSelectPostableCapsBelowCriticalHigh(t *testing.T) {
	// threshold=critical (flagHigh): critical+high always; the rest capped at 10.
	sevs := append([]render.Severity{render.SeverityCritical, render.SeverityHigh}, repeatSeverity(render.SeverityMedium, 12)...)
	got := selectPostable(findingsOf(sevs...), render.SeverityCritical)
	// 1 critical + 1 high + 10 medium (cap) = 12; 2 medium dropped.
	if len(got) != 12 {
		t.Fatalf("want 12 posted (critical+high+10 capped), got %d", len(got))
	}
	var crit, high, med int
	for _, f := range got {
		switch f.Severity {
		case render.SeverityCritical:
			crit++
		case render.SeverityHigh:
			high++
		case render.SeverityMedium:
			med++
		}
	}
	if crit != 1 || high != 1 || med != 10 {
		t.Errorf("want crit=1 high=1 med=10, got crit=%d high=%d med=%d", crit, high, med)
	}
}

func TestSelectPostableNoCapWhenThresholdBelowHigh(t *testing.T) {
	// threshold=medium (!flagHigh): post everything >= medium, no cap; low/info dropped.
	sevs := append(repeatSeverity(render.SeverityMedium, 20), render.SeverityLow, render.SeverityInfo)
	got := selectPostable(findingsOf(sevs...), render.SeverityMedium)
	if len(got) != 20 {
		t.Fatalf("threshold=medium should post all 20 medium (no cap, low/info excluded), got %d", len(got))
	}
	for _, f := range got {
		if f.Severity != render.SeverityMedium {
			t.Errorf("unexpected severity %q in postable set", f.Severity)
		}
	}
}

func TestSelectPostableSortsBySeverityDesc(t *testing.T) {
	got := selectPostable(findingsOf(render.SeverityLow, render.SeverityCritical, render.SeverityMedium), render.SeverityLow)
	if len(got) != 3 {
		t.Fatalf("want 3, got %d", len(got))
	}
	if got[0].Severity != render.SeverityCritical || got[2].Severity != render.SeverityLow {
		t.Errorf("not sorted by severity desc: %q ... %q", got[0].Severity, got[2].Severity)
	}
}
