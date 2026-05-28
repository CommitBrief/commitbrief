// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"

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

// ---------- remote pr orchestration (fake gh runner) ----------

// stubGHOnPath drops an executable `gh` stub into a temp dir and prepends
// it to PATH so remote.EnsureGH() passes. The stub is never executed —
// every gh call goes through the injected fakeGH — it only needs to
// satisfy exec.LookPath.
func stubGHOnPath(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("gh PATH stub is POSIX-only in this test")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// fakeGH dispatches gh invocations by their args, so call order (including
// the race-retry's repeated pr-view) doesn't matter.
type fakeGH struct {
	whoami     string
	prMeta     string   // `pr view --json number,author,...`
	commitsSeq []string // sequential `pr view --json commits` responses
	commitsIdx int
	diff       string
	commentErr error
	calls      []string
}

func (f *fakeGH) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case len(args) >= 2 && args[0] == "api" && args[1] == "user":
		return []byte(f.whoami + "\n"), nil
	case has(args, "view") && jsonField(args) == "commits":
		out := `{"commits":[{"oid":"stable"}]}`
		if f.commitsIdx < len(f.commitsSeq) {
			out = f.commitsSeq[f.commitsIdx]
		}
		f.commitsIdx++
		return []byte(out), nil
	case has(args, "view"):
		return []byte(f.prMeta), nil
	case has(args, "diff"):
		return []byte(f.diff), nil
	case len(args) > 0 && args[0] == "api" && has(args, "POST"):
		return nil, f.commentErr
	case has(args, "review"):
		return nil, nil
	}
	return nil, nil
}

func (f *fakeGH) callCount(substr string) int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, substr) {
			n++
		}
	}
	return n
}

func has(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func jsonField(args []string) string {
	for i, a := range args {
		if a == "--json" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func prMetaJSON(author, oid string) string {
	return `{"number":42,"author":{"login":"` + author + `"},` +
		`"url":"https://github.com/CommitBrief/web/pull/42",` +
		`"headRepository":{"name":"web","owner":{"login":"forker"}},` +
		`"commits":[{"oid":"` + oid + `"}]}`
}

const sampleDiff = "diff --git a/mock.go b/mock.go\n" +
	"index 1111111..2222222 100644\n" +
	"--- a/mock.go\n" +
	"+++ b/mock.go\n" +
	"@@ -1 +1,2 @@\n" +
	" package mock\n" +
	"+var X = 1\n"

// callRemotePR runs the orchestration inside the env (chdir into the repo
// so resolveContext detects it) with the injected fake runner.
func (e *cliEnv) callRemotePR(prID string, f remotePRFlags, r remote.Runner) error {
	e.t.Helper()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	e.t.Cleanup(func() { _ = os.Chdir(oldWd) })
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return runRemotePR(cmd, prID, f, r)
}

func TestRemotePRSelfPRBlocked(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := &fakeGH{whoami: "tester", prMeta: prMetaJSON("tester", "stable")}
	err := e.callRemotePR("42", remotePRFlags{requestChangesOn: "critical"}, r)
	if err == nil || !strings.Contains(err.Error(), "author of this PR") {
		t.Fatalf("want self-PR rejection, got %v", err)
	}
	if r.callCount("diff") != 0 || r.callCount("review") != 0 {
		t.Errorf("self-PR must abort before diff/review; calls=%v", r.calls)
	}
}

func TestRemotePRRejectsRequestChangesOnInfo(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := &fakeGH{whoami: "tester", prMeta: prMetaJSON("contributor", "stable")}
	err := e.callRemotePR("42", remotePRFlags{requestChangesOn: "info"}, r)
	if err == nil || !strings.Contains(err.Error(), "info") {
		t.Fatalf("want info-threshold rejection, got %v", err)
	}
	if len(r.calls) != 0 {
		t.Errorf("info rejection happens before any gh call; calls=%v", r.calls)
	}
}

func TestRemotePRApproveFlowPostsCommentThenApproves(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	// mock provider returns one info finding; threshold critical → approve,
	// and the info finding is posted (below critical/high, under the 10 cap).
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       sampleDiff,
	}
	if err := e.callRemotePR("42", remotePRFlags{requestChangesOn: "critical"}, r); err != nil {
		t.Fatalf("approve flow: %v", err)
	}
	if r.callCount("/comments") != 1 {
		t.Errorf("want 1 inline comment POST, got %d (calls=%v)", r.callCount("/comments"), r.calls)
	}
	if r.callCount("--approve") != 1 {
		t.Errorf("want a --approve verdict, calls=%v", r.calls)
	}
}

func TestRemotePRAbortsOnDoubleRace(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	// Initial OID "stable"; both post-review commit checks return a new
	// OID → head moved twice → too volatile, no verdict submitted.
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"moved1"}]}`, `{"commits":[{"oid":"moved2"}]}`},
		diff:       sampleDiff,
	}
	err := e.callRemotePR("42", remotePRFlags{requestChangesOn: "critical"}, r)
	if err == nil || !strings.Contains(err.Error(), "twice during review") {
		t.Fatalf("want too-volatile abort, got %v", err)
	}
	if r.callCount("review") != 0 {
		t.Errorf("no verdict on double race; calls=%v", r.calls)
	}
}
