// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/remote"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/ui"
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
		// Empty threshold = --request-changes-on not set: never request-changes.
		{"critical, no threshold -> comment", findingsOf(render.SeverityCritical), "", remote.VerdictComment},
		{"high+critical, no threshold -> comment", findingsOf(render.SeverityHigh, render.SeverityCritical), "", remote.VerdictComment},
		{"only info, no threshold -> approve", findingsOf(render.SeverityInfo), "", remote.VerdictApprove},
		{"no findings, no threshold -> approve", nil, "", remote.VerdictApprove},
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
	whoami      string
	prMeta      string   // `pr view --json number,author,...`
	commitsSeq  []string // sequential `pr view --json commits` responses
	commitsIdx  int
	diff        string
	commentErr  error
	threads     string // `api graphql` review-thread snapshot (empty = none)
	reviews     string // `api graphql` reviews(last:100) response (empty = none)
	reviewsErr  error
	compare     string // `api repos/.../compare/a...b` JSON (empty = 404)
	compareErr  error
	compareDif  string // the same compare as a unified diff
	compareDErr error
	calls       []string
}

func (f *fakeGH) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch {
	case len(args) >= 2 && args[0] == "api" && args[1] == "user":
		return []byte(f.whoami + "\n"), nil
	case len(args) >= 2 && args[0] == "api" && anyContains(args, "/compare/"):
		if has(args, "Accept: application/vnd.github.diff") {
			return []byte(f.compareDif), f.compareDErr
		}
		if f.compare == "" && f.compareErr == nil {
			return nil, errors.New("gh: Not Found (HTTP 404)")
		}
		return []byte(f.compare), f.compareErr
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql" && anyContains(args, "reviews(last:100)"):
		if f.reviews == "" && f.reviewsErr == nil {
			return []byte(`{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[]}}}}}`), nil
		}
		return []byte(f.reviews), f.reviewsErr
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
	case len(args) >= 2 && args[0] == "api" && args[1] == "graphql":
		return []byte(f.threads), nil
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

func anyContains(args []string, sub string) bool {
	for _, a := range args {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
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

// Without --request-changes-on the verdict path must never escalate to
// request-changes, even end-to-end. (The default mock returns an info-only
// finding, so this asserts the absence of --request-changes; the
// critical→comment decision is covered by TestComputeVerdict.)
func TestRemotePRNoFlagNeverRequestsChanges(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       sampleDiff,
	}
	if err := e.callRemotePR("42", remotePRFlags{}, r); err != nil {
		t.Fatalf("no-flag flow: %v", err)
	}
	if r.callCount("--request-changes") != 0 {
		t.Errorf("request-changes must never be submitted without the flag; calls=%v", r.calls)
	}
}

func TestRemotePRPrintsHeaderAndFooter(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       sampleDiff,
	}
	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetErr(&errBuf)
	if err := runRemotePR(cmd, "42", remotePRFlags{requestChangesOn: "critical"}, r); err != nil {
		t.Fatalf("remote pr: %v", err)
	}

	// Segments are contiguous inside any lipgloss ANSI wrappers, so a raw
	// substring search is robust to the color profile.
	out := errBuf.String()
	// Header (above the tree), status (tree info line), footer (below).
	for _, want := range []string{"commitbrief", "provider:", "analyzing 1 file", "Done in"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected standard review line %q in remote output; got:\n%s", want, out)
		}
	}
}

func TestRemotePRNoPostRendersLocallyWithoutWrites(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	// --no-post fetches the diff but performs NO GitHub writes. whoami /
	// prMeta are deliberately left set to the SAME login to prove the
	// self-PR block does not apply in this read-only mode (it never calls
	// whoami / pr view at all).
	r := &fakeGH{whoami: "tester", prMeta: prMetaJSON("tester", "stable"), diff: sampleDiff}

	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)

	if err := runRemotePR(cmd, "42", remotePRFlags{noPost: true, requestChangesOn: "critical"}, r); err != nil {
		t.Fatalf("--no-post: %v", err)
	}

	// The diff is still fetched...
	if r.callCount("diff") == 0 {
		t.Errorf("--no-post should fetch the PR diff; calls=%v", r.calls)
	}
	// ...but nothing is written to GitHub.
	for _, write := range []string{"/comments", "review", "--approve", "--request-changes", "--comment"} {
		if n := r.callCount(write); n != 0 {
			t.Errorf("--no-post must not write to GitHub (%q seen %d times); calls=%v", write, n, r.calls)
		}
	}
	// And the review is rendered locally to stdout.
	if out.Len() == 0 {
		t.Errorf("--no-post should render the review to stdout; stderr=%s", errBuf.String())
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

func TestSubmitPRReviewAnchorsAndFallsBack(t *testing.T) {
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	app := &appContext{Catalog: cat}
	parsed, err := diff.Parse(git.Diff{Content: sampleDiff, Origin: git.OriginDiff})
	if err != nil {
		t.Fatal(err)
	}
	anchors := parsed.Anchors()

	findings := []render.Finding{
		// Line 1 (context `package mock`) is a valid RIGHT position → posted.
		{Severity: render.SeverityInfo, File: "mock.go", Line: 1, Title: "anchored", Description: "d", Suggestion: "s"},
		// Line 999 is outside every hunk → falls back to the summary body.
		{Severity: render.SeverityInfo, File: "mock.go", Line: 999, Title: "floating", Description: "d", Suggestion: "s"},
	}

	r := &fakeGH{}
	meta := remote.PRMeta{Number: 42, URL: "https://github.com/o/r/pull/42"}
	prog := ui.NewProgress(io.Discard, ui.ColorNever, true) // silent
	if err := submitPRReview(context.Background(), r, "42", remotePRFlags{}, meta, "oid",
		prReviewResult{findings: findings, anchors: anchors}, render.SeverityCritical, "tester", app, prog); err != nil {
		t.Fatalf("submitPRReview: %v", err)
	}

	if got := r.callCount("/comments"); got != 1 {
		t.Errorf("want exactly 1 inline comment (only the anchored finding), got %d; calls=%v", got, r.calls)
	}
	// The unanchored finding must survive in the review body.
	var reviewBody string
	for _, c := range r.calls {
		if strings.Contains(c, "review") {
			reviewBody = c
		}
	}
	if !strings.Contains(reviewBody, "Findings that could not be attached") {
		t.Errorf("unanchored finding not appended to review body; review call=%q", reviewBody)
	}
	if !strings.Contains(reviewBody, "floating") {
		t.Errorf("unanchored finding title missing from body; review call=%q", reviewBody)
	}
}

func fpOfTest(c string) string { return strings.Repeat(c, 64) }

func TestRemotePRBuildSyncGroupsFoldsByFingerprint(t *testing.T) {
	parsed, err := diff.Parse(git.Diff{Content: sampleDiff, Origin: git.OriginDiff})
	if err != nil {
		t.Fatal(err)
	}
	anchors := parsed.Anchors()
	fp := fpOfTest("a")
	findings := []render.Finding{
		{Severity: render.SeverityInfo, File: "mock.go", Line: 999, Title: "t", Description: "d", Suggestion: "s", Fingerprint: fp},
		{Severity: render.SeverityHigh, File: "mock.go", Line: 1, Title: "t", Description: "d", Suggestion: "s", Fingerprint: fp},
		{Severity: render.SeverityInfo, File: "mock.go", Line: 1, Title: "u", Description: "d", Suggestion: "s"},
		{Severity: render.SeverityInfo, File: "mock.go", Line: 1, Title: "v", Description: "d", Suggestion: "s", Fingerprint: "bogus"},
	}
	groups := buildSyncGroups(findings, anchors, nil, "me")
	if len(groups) != 3 {
		t.Fatalf("want 3 groups (1 folded + 2 unmanaged), got %d", len(groups))
	}
	g := groups[0]
	if g.FP != fp || len(g.Members) != 2 || !g.Anchored || g.Rep.Line != 1 || g.Severity != render.SeverityHigh {
		t.Fatalf("folded group = %+v", g)
	}
	if !strings.Contains(g.Text, "Also at: mock.go:999 (t)") {
		t.Fatalf("group text lacks the other occurrence with its title: %q", g.Text)
	}
	if !strings.HasPrefix(g.Text, "[HIGH]") {
		t.Fatalf("header must carry the group severity: %q", g.Text)
	}
	if groups[1].FP != "" || groups[2].FP != "" {
		t.Fatalf("unmanaged groups must carry no fingerprint")
	}
}

func submitForTest(t *testing.T, r *fakeGH, findings []render.Finding) {
	t.Helper()
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := diff.Parse(git.Diff{Content: sampleDiff, Origin: git.OriginDiff})
	if err != nil {
		t.Fatal(err)
	}
	meta := remote.PRMeta{Number: 42, URL: "https://github.com/o/r/pull/42"}
	prog := ui.NewProgress(io.Discard, ui.ColorNever, true)
	if err := submitPRReview(context.Background(), r, "42", remotePRFlags{}, meta, "oid",
		prReviewResult{findings: findings, anchors: parsed.Anchors()}, render.SeverityCritical, "tester", &appContext{Catalog: cat}, prog); err != nil {
		t.Fatalf("submitPRReview: %v", err)
	}
}

func TestRemotePRSyncSkipsUnchangedAndResolvesGone(t *testing.T) {
	fpKept, fpGone := fpOfTest("a"), fpOfTest("b")
	f := render.Finding{Severity: render.SeverityInfo, File: "mock.go", Line: 1, Title: "kept", Description: "d", Suggestion: "s", Fingerprint: fpKept}
	keptBody := remote.BuildCommentBody(f, "tester")
	goneBody := "[INFO] - gone\nd\n\U0001F4A1 s @tester by #commitbrief\n<!-- commitbrief:fp=" + fpGone + " v=1 -->"
	node := func(id string, dbID int, body string) string {
		b, _ := json.Marshal(body)
		return `{"id":"` + id + `","isResolved":false,"isOutdated":false,"viewerCanResolve":true,"viewerCanUnresolve":true,"viewerCanReply":true,` +
			`"comments":{"nodes":[{"databaseId":` + strconv.Itoa(dbID) + `,"body":` + string(b) + `,"viewerDidAuthor":true,"viewerCanUpdate":true}]},` +
			`"recent":{"nodes":[{"databaseId":` + strconv.Itoa(dbID) + `,"body":` + string(b) + `,"viewerDidAuthor":true}]}}`
	}
	snapshot := `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` +
		node("KEPT", 1, keptBody) + `,` + node("GONE", 2, goneBody) + `]}}}}}`

	r := &fakeGH{threads: snapshot}
	submitForTest(t, r, []render.Finding{f})

	if got := r.callCount("/pulls/42/comments "); got != 0 {
		t.Errorf("unchanged finding must not be posted again; calls=%v", r.calls)
	}
	if r.callCount("resolveReviewThread") != 1 || r.callCount("id=GONE") != 1 {
		t.Errorf("want exactly one resolve for the vanished finding; calls=%v", r.calls)
	}
	if r.callCount("PATCH") != 1 || r.callCount("/pulls/comments/2") != 1 {
		t.Errorf("resolve must be recorded on the root comment; calls=%v", r.calls)
	}
	if r.callCount("id=KEPT") != 0 {
		t.Errorf("kept thread must not be touched; calls=%v", r.calls)
	}
}

func TestRemotePRSyncReadFailureDegradesToPosting(t *testing.T) {
	f := render.Finding{Severity: render.SeverityInfo, File: "mock.go", Line: 1, Title: "kept", Description: "d", Suggestion: "s", Fingerprint: fpOfTest("a")}
	r := &fakeGH{threads: "{garbage"}
	submitForTest(t, r, []render.Finding{f})
	if got := r.callCount("/comments"); got != 1 {
		t.Errorf("a failed thread read must still post the comment once; calls=%v", r.calls)
	}
	if r.callCount("resolveReviewThread") != 0 {
		t.Errorf("no thread writes without a snapshot; calls=%v", r.calls)
	}
}

// scopedSnapshot builds a thread snapshot whose threads carry a path.
func scopedSnapshot(t *testing.T, threads map[string]string) string {
	t.Helper()
	var nodes []string
	n := 0
	for path, fp := range threads {
		n++
		body, _ := json.Marshal("[INFO] - gone\nd\n\U0001F4A1 s @tester by #commitbrief\n<!-- commitbrief:fp=" + fp + " v=1 -->")
		id := "T" + path
		nodes = append(nodes, `{"id":"`+id+`","path":"`+path+`","isResolved":false,"isOutdated":false,"viewerCanResolve":true,"viewerCanUnresolve":true,"viewerCanReply":true,`+
			`"comments":{"nodes":[{"databaseId":`+strconv.Itoa(n)+`,"body":`+string(body)+`,"viewerDidAuthor":true,"viewerCanUpdate":true}]},`+
			`"recent":{"nodes":[{"databaseId":`+strconv.Itoa(n)+`,"body":`+string(body)+`,"viewerDidAuthor":true}]}}`)
	}
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` + strings.Join(nodes, ",") + `]}}}}}`
}

func withGlobal(t *testing.T, g globalFlags) {
	t.Helper()
	saved := global
	global = g
	t.Cleanup(func() { global = saved })
}

func TestRemotePRSyncNarrowedRunLeavesOutOfScopeThreadAlone(t *testing.T) {
	withGlobal(t, globalFlags{files: []string{"mock.go"}})
	snapshot := scopedSnapshot(t, map[string]string{"mock.go": fpOfTest("a"), "other.go": fpOfTest("b")})
	r := &fakeGH{threads: snapshot}
	submitForTest(t, r, nil)
	if r.callCount("id=Tmock.go") != 1 {
		t.Errorf("in-scope vanished thread must be resolved once; calls=%v", r.calls)
	}
	if r.callCount("id=Tother.go") != 0 {
		t.Errorf("out-of-scope thread must not be touched; calls=%v", r.calls)
	}
}

func TestRemotePRSyncNarrowedEmptyScopeSkipsSync(t *testing.T) {
	withGlobal(t, globalFlags{files: []string{"nothing-matches.go"}})
	r := &fakeGH{threads: scopedSnapshot(t, map[string]string{"mock.go": fpOfTest("a")})}
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	meta := remote.PRMeta{Number: 42, URL: "https://github.com/o/r/pull/42"}
	prog := ui.NewProgress(io.Discard, ui.ColorNever, true)
	f := render.Finding{Severity: render.SeverityInfo, File: "mock.go", Line: 1, Title: "t", Description: "d", Suggestion: "s", Fingerprint: fpOfTest("a")}
	err = submitPRReview(context.Background(), r, "42", remotePRFlags{}, meta, "oid",
		prReviewResult{findings: []render.Finding{f}, anchors: map[string]diff.FileAnchors{}}, render.SeverityCritical, "tester", &appContext{Catalog: cat}, prog)
	if err != nil {
		t.Fatalf("submitPRReview: %v", err)
	}
	if r.callCount("graphql") != 0 || r.callCount("/comments") != 0 || r.callCount("PATCH") != 0 {
		t.Errorf("an empty narrowed scope must skip the sync entirely; calls=%v", r.calls)
	}
}

// ---------- inline suppression (ADR-0027 / ADR-0046 §Update 2026-10-20) ----------

// markedMockDiff adds mock.go whose first line carries the given marker text.
// The mock provider's canned finding sits on mock.go:1, so a marker on that
// line is what silences it. Markers are only read from ADDED lines, hence a
// new-file diff rather than the context-only line 1 of sampleDiff.
func markedMockDiff(marker string) string {
	return "diff --git a/mock.go b/mock.go\n" +
		"new file mode 100644\n" +
		"index 0000000..2222222\n" +
		"--- /dev/null\n" +
		"+++ b/mock.go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+package mock // " + marker + "\n" +
		"+var X = 1\n"
}

// runRemotePRCapture drives runRemotePR and returns everything written to
// stderr, whether through the command's writer or the raw os.Stderr.
func runRemotePRCapture(t *testing.T, e *cliEnv, f remotePRFlags, r *fakeGH) (string, error) {
	t.Helper()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldErr := os.Stderr
	os.Stderr = pw
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(pr)
		done <- string(b)
	}()

	var errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetErr(&errBuf)
	runErr := runRemotePR(cmd, "42", f, r)

	os.Stderr = oldErr
	_ = pw.Close()
	return errBuf.String() + <-done, runErr
}

func TestRemotePRInlineSuppressSkipsComment(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       markedMockDiff("commitbrief-ignore: accepted"),
	}
	out, err := runRemotePRCapture(t, e, remotePRFlags{requestChangesOn: "critical"}, r)
	if err != nil {
		t.Fatalf("remote pr: %v", err)
	}
	if got := r.callCount("/comments"); got != 0 {
		t.Errorf("a finding on a commitbrief-ignore line must not be posted; calls=%v", r.calls)
	}
	if !strings.Contains(out, "1 suppressed") {
		t.Errorf("want the suppressed footer on stderr, got:\n%s", out)
	}
	if r.callCount("--approve") != 1 {
		t.Errorf("with nothing left to post the review still approves; calls=%v", r.calls)
	}
}

// Control: the same PR without the marker does post, so the test above is
// proving the marker and not a vacuous absence.
func TestRemotePRInlineSuppressControlPostsWithoutMarker(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       markedMockDiff("no marker here"),
	}
	out, err := runRemotePRCapture(t, e, remotePRFlags{requestChangesOn: "critical"}, r)
	if err != nil {
		t.Fatalf("remote pr: %v", err)
	}
	if r.callCount("/comments") != 1 {
		t.Errorf("want 1 inline comment without a marker; calls=%v", r.calls)
	}
	if strings.Contains(out, "suppressed") {
		t.Errorf("no suppressed footer expected, got:\n%s", out)
	}
}

func TestRemotePRInlineSuppressResolvesOpenThread(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	diffText := markedMockDiff("commitbrief-ignore: accepted")
	parsed, perr := diff.Parse(git.Diff{Content: diffText, Origin: git.OriginDiff})
	if perr != nil {
		t.Fatal(perr)
	}
	// The thread an earlier, marker-less run opened for the very same finding.
	fp := stampIdentity(parsed, []render.Finding{{File: "mock.go", Line: 1}})[0].Fingerprint
	r := &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", "stable"),
		commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
		diff:       diffText,
		threads:    scopedSnapshot(t, map[string]string{"mock.go": fp}),
	}
	if _, err := runRemotePRCapture(t, e, remotePRFlags{requestChangesOn: "critical"}, r); err != nil {
		t.Fatalf("remote pr: %v", err)
	}
	if r.callCount("/pulls/42/comments ") != 0 {
		t.Errorf("the suppressed finding must not be re-posted; calls=%v", r.calls)
	}
	if r.callCount("resolveReviewThread") != 1 || r.callCount("id=Tmock.go") != 1 {
		t.Errorf("the earlier thread must be resolved once; calls=%v", r.calls)
	}
}

func TestRemotePRInlineSuppressUnknownScopeWarns(t *testing.T) {
	newGH := func() *fakeGH {
		return &fakeGH{
			whoami:     "tester",
			prMeta:     prMetaJSON("contributor", "stable"),
			commitsSeq: []string{`{"commits":[{"oid":"stable"}]}`},
			diff:       markedMockDiff("commitbrief-ignore[bogus]: typo"),
		}
	}

	e := newCLIEnv(t)
	stubGHOnPath(t)
	out, err := runRemotePRCapture(t, e, remotePRFlags{}, newGH())
	if err != nil {
		t.Fatalf("remote pr: %v", err)
	}
	if !strings.Contains(out, "commitbrief-ignore[bogus]") || !strings.Contains(out, "mock.go:1") {
		t.Errorf("want the unknown-scope warning on stderr, got:\n%s", out)
	}

	// --quiet silences both the warning and the footer.
	withGlobal(t, globalFlags{color: "never", quiet: true})
	r := newGH()
	out, err = runRemotePRCapture(t, e, remotePRFlags{}, r)
	if err != nil {
		t.Fatalf("remote pr --quiet: %v", err)
	}
	if strings.Contains(out, "commitbrief-ignore") || strings.Contains(out, "suppressed") {
		t.Errorf("--quiet must silence the warning and footer, got:\n%s", out)
	}
	if r.callCount("/comments") != 0 {
		t.Errorf("an unknown-scope marker still suppresses (fail-open); calls=%v", r.calls)
	}
}
