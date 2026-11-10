// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
	"github.com/CommitBrief/commitbrief/internal/remote"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/ui"
)

// ADR-0048: the posting-mode machine report (--json) and the verdict ceiling
// (--max-verdict). The provider is a scriptable fake so a test can choose the
// finding set, the usage and the call on which the provider fails.

var updateRemoteGolden = flag.Bool("update", false, "regenerate remote pr report golden files")

const reportFakeName = "remote-report-fake"

var (
	reportFakeOnce sync.Once
	reportFakeMu   sync.Mutex
	reportFake     reportFakeState
)

// reportFakeState scripts the fake provider for one test.
type reportFakeState struct {
	responses []string // one per call; the last repeats
	errOnCall int      // 1-based provider call that fails (0 = none)
	calls     int
}

type reportFakeProvider struct{ *mock.Provider }

func (p reportFakeProvider) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	reportFakeMu.Lock()
	reportFake.calls++
	n := reportFake.calls
	fail := reportFake.errOnCall == n
	content := mock.DefaultResponseContent
	if len(reportFake.responses) > 0 {
		i := n - 1
		if i >= len(reportFake.responses) {
			i = len(reportFake.responses) - 1
		}
		content = reportFake.responses[i]
	}
	reportFakeMu.Unlock()
	if fail {
		return provider.Response{}, errors.New("provider exploded")
	}
	p.ResponseContent = content
	return p.Provider.Review(ctx, req)
}

// useReportFake scripts the fake provider and points the run at it. Usage is
// 100 in / 50 out per call; the price makes that exactly $200 per call.
func useReportFake(t *testing.T, st reportFakeState) {
	t.Helper()
	reportFakeOnce.Do(func() {
		provider.Register(reportFakeName, func(config.ProviderConfig) (provider.Provider, error) {
			m := mock.New()
			m.PricingValue = provider.Pricing{InputPer1M: 1_000_000, OutputPer1M: 2_000_000}
			return reportFakeProvider{m}, nil
		})
	})
	reportFakeMu.Lock()
	reportFake = st
	reportFakeMu.Unlock()
	global.provider = reportFakeName
}

func reportFinding(sev string) string {
	return fmt.Sprintf(`{"severity":%q,"file":"mock.go","line":1,"title":"t-%s","description":"d","suggestion":"s"}`, sev, sev)
}

func reportFindings(sevs ...string) string {
	parts := make([]string, len(sevs))
	for i, s := range sevs {
		parts[i] = reportFinding(s)
	}
	return `{"findings":[` + strings.Join(parts, ",") + `]}`
}

// remoteDoc is the subset of the JSON document these tests assert on.
type remoteDoc struct {
	Schema   int               `json:"schema"`
	Content  string            `json:"content"`
	Findings []json.RawMessage `json:"findings"`
	Meta     struct {
		Provider      string  `json:"provider"`
		Cost          float64 `json:"cost_usd"`
		Cached        bool    `json:"cached"`
		Suppressed    int     `json:"suppressed"`
		RetryCount    int     `json:"retry_count"`
		DegradeReason string  `json:"degrade_reason"`
		Usage         struct {
			In  int `json:"input_tokens"`
			Out int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"meta"`
	Remote *render.RemoteReport `json:"remote"`
}

func (d remoteDoc) warning(code string) (render.RemoteWarning, bool) {
	if d.Remote == nil {
		return render.RemoteWarning{}, false
	}
	for _, w := range d.Remote.Warnings {
		if w.Code == code {
			return w, true
		}
	}
	return render.RemoteWarning{}, false
}

// runPosting drives posting-mode runRemotePR with the given flags and
// returns stdout, stderr and the error. Posting mode always runs with the
// fake provider selected by useReportFake.
func runPosting(t *testing.T, e *cliEnv, f remotePRFlags, r remote.Runner) (stdout, stderr string, err error) {
	t.Helper()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })
	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	err = runRemotePR(cmd, "42", f, r)
	return out.String(), errBuf.String(), err
}

func decodeDoc(t *testing.T, stdout string) remoteDoc {
	t.Helper()
	var d remoteDoc
	if err := json.Unmarshal([]byte(stdout), &d); err != nil {
		t.Fatalf("stdout is not a JSON document: %v\n%q", err, stdout)
	}
	if d.Schema != 1 {
		t.Fatalf("schema = %d, want 1", d.Schema)
	}
	return d
}

func newPostingGH(oid string) *fakeGH {
	return &fakeGH{
		whoami:     "tester",
		prMeta:     prMetaJSON("contributor", oid),
		commitsSeq: repeatedCommits(oid, 8),
		diff:       sampleDiff,
	}
}

var (
	timestampRe = regexp.MustCompile(`"timestamp": "[^"]*"`)
	latencyRe   = regexp.MustCompile(`"latency_ms": \d+`)
)

func TestRemotePRReportGolden(t *testing.T) {
	// runPosting chdirs into the temp repo until cleanup, so pin the golden
	// path to the package directory first.
	pkgDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})

	stdout, stderr, err := runPosting(t, e, remotePRFlags{maxVerdict: "comment"}, newPostingGH("stable"))
	if err != nil {
		t.Fatalf("remote pr --json: %v\n%s", err, stderr)
	}
	got := timestampRe.ReplaceAllString(stdout, `"timestamp": "2026-01-01T00:00:00Z"`)
	got = latencyRe.ReplaceAllString(got, `"latency_ms": 0`)

	golden := filepath.Join(pkgDir, "testdata", "remote_pr_report.golden.json")
	if *updateRemoteGolden {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated %s", golden)
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to create)", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if got != string(want) {
		t.Errorf("report differs from %s (re-run with -update after reviewing).\n\nGot:\n%s\n\nWant:\n%s", golden, got, want)
	}
}

// ADR-0048 §4 table: the ceiling only ever turns approve into comment;
// request-changes belongs to --request-changes-on alone.
func TestRemotePRMaxVerdictCeilingMatrix(t *testing.T) {
	cases := []struct {
		name        string
		rcOn        string
		max         string
		response    string
		wantFlag    string
		wantVerdict string
		wantUncap   string
	}{
		{"default, no findings", "", "", reportFindings(), "--approve", "approve", "approve"},
		{"explicit approve, no findings", "", "approve", reportFindings(), "--approve", "approve", "approve"},
		{"comment, no findings", "", "comment", reportFindings(), "--comment", "comment", "approve"},
		{"comment, only info", "", "comment", reportFindings("info"), "--comment", "comment", "approve"},
		{"comment, a low finding", "", "comment", reportFindings("low"), "--comment", "comment", "comment"},
		{"comment, critical, request-changes off", "", "comment", reportFindings("critical"), "--comment", "comment", "comment"},
		{"rc on high, approve, no findings", "high", "approve", reportFindings(), "--approve", "approve", "approve"},
		{"rc on high, comment, no findings", "high", "comment", reportFindings(), "--comment", "comment", "approve"},
		{"rc on high, comment, low finding", "high", "comment", reportFindings("low"), "--comment", "comment", "comment"},
		{"rc on high, comment, critical finding", "high", "comment", reportFindings("critical"), "--request-changes", "request-changes", "request-changes"},
		{"rc on high, approve, critical finding", "high", "approve", reportFindings("critical"), "--request-changes", "request-changes", "request-changes"},
		{"value is trimmed and case-folded", "", "  COMMENT ", reportFindings(), "--comment", "comment", "approve"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCLIEnv(t)
			stubGHOnPath(t)
			global.json = true
			useReportFake(t, reportFakeState{responses: []string{tc.response}})
			r := newPostingGH("stable")

			stdout, stderr, err := runPosting(t, e, remotePRFlags{requestChangesOn: tc.rcOn, maxVerdict: tc.max}, r)
			if err != nil {
				t.Fatalf("remote pr: %v\n%s", err, stderr)
			}
			for _, flagName := range []string{"--approve", "--comment", "--request-changes"} {
				want := 0
				if flagName == tc.wantFlag {
					want = 1
				}
				if n := r.callCount("pr review 42 " + flagName); n != want {
					t.Errorf("%s submitted %d times, want %d; calls=%v", flagName, n, want, r.calls)
				}
			}
			d := decodeDoc(t, stdout)
			if d.Remote.Verdict != tc.wantVerdict || d.Remote.VerdictUncapped != tc.wantUncap {
				t.Errorf("verdict = %q (uncapped %q), want %q (uncapped %q)",
					d.Remote.Verdict, d.Remote.VerdictUncapped, tc.wantVerdict, tc.wantUncap)
			}
			wantMax := "approve"
			if strings.Contains(strings.ToLower(tc.max), "comment") {
				wantMax = "comment"
			}
			if d.Remote.MaxVerdict != wantMax {
				t.Errorf("max_verdict = %q, want %q", d.Remote.MaxVerdict, wantMax)
			}
		})
	}
}

// A capped review still posts its inline comments and records the reviewed
// marker: it reviewed the PR, it only declined to approve (ADR-0048 §4).
func TestRemotePRMaxVerdictCommentKeepsCommentsAndMarker(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
	r := newPostingGH("stable")
	if _, stderr, err := runPosting(t, e, remotePRFlags{maxVerdict: "comment"}, r); err != nil {
		t.Fatalf("remote pr: %v\n%s", err, stderr)
	}
	if r.callCount("/comments") != 1 {
		t.Errorf("inline comment must still post; calls=%v", r.calls)
	}
	if r.callCount(remote.ReviewedMarker("stable")) != 1 {
		t.Errorf("reviewed marker must still be written; calls=%v", r.calls)
	}
	if r.callCount("--approve") != 0 {
		t.Errorf("a capped review must never approve; calls=%v", r.calls)
	}
}

func TestRemotePRMaxVerdictRejectedBeforeAnyGHCall(t *testing.T) {
	cases := []struct{ value, want string }{
		{"request-changes", "not a ceiling"},
		{"  Request-Changes ", "not a ceiling"},
		{"block", "invalid --max-verdict"},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			e := newCLIEnv(t)
			stubGHOnPath(t)
			useReportFake(t, reportFakeState{})
			r := newPostingGH("stable")
			_, _, err := runPosting(t, e, remotePRFlags{maxVerdict: tc.value}, r)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if len(r.calls) != 0 {
				t.Errorf("a bad --max-verdict must fail before any gh call; calls=%v", r.calls)
			}
		})
	}
}

func TestRemotePRReportMarkdownCopyCompactStayRejected(t *testing.T) {
	for name, set := range map[string]func(){
		"markdown":    func() { global.markdown = true },
		"copy":        func() { global.copy = true },
		"compact":     func() { global.compact = true },
		"output only": func() { global.output = filepath.Join(t.TempDir(), "x.json") },
	} {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			stubGHOnPath(t)
			useReportFake(t, reportFakeState{})
			set()
			r := newPostingGH("stable")
			_, _, err := runPosting(t, e, remotePRFlags{}, r)
			if err == nil || !strings.Contains(err.Error(), "--no-post") {
				t.Fatalf("want the output_flags rejection, got %v", err)
			}
			if len(r.calls) != 0 {
				t.Errorf("rejection must precede any gh call; calls=%v", r.calls)
			}
		})
	}
}

// --json does not imply --no-post: the run posts and submits, and writes the
// document. -o sends the document to a file instead of stdout.
func TestRemotePRReportJSONPostsAndWritesDocument(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{responses: []string{reportFindings("low")}})
	r := newPostingGH("stable")

	stdout, stderr, err := runPosting(t, e, remotePRFlags{}, r)
	if err != nil {
		t.Fatalf("remote pr --json: %v\n%s", err, stderr)
	}
	if r.callCount("pr review 42 --comment") != 1 || r.callCount("/comments") != 1 {
		t.Errorf("--json must still post comments and submit the verdict; calls=%v", r.calls)
	}
	d := decodeDoc(t, stdout)
	rep := d.Remote
	if rep == nil {
		t.Fatal("posting-mode document must carry the remote container")
	}
	if rep.Status != "submitted" || rep.PR.Repo != "CommitBrief/web" || rep.PR.Number != 42 || rep.HeadOID != "stable" {
		t.Errorf("remote header = %+v", rep)
	}
	if rep.Review.Mode != "full" || rep.Review.FullReason != "not_requested" || rep.Review.Attempts != 1 || !rep.Review.MarkerWritten {
		t.Errorf("review = %+v", rep.Review)
	}
	if rep.Sync == nil || rep.Sync.Mode != "synced" || rep.Sync.Posted != 1 {
		t.Errorf("sync = %+v", rep.Sync)
	}
	if rep.Warnings == nil || len(rep.Warnings) != 0 {
		t.Errorf("warnings = %#v, want a present, empty array", rep.Warnings)
	}
	if len(d.Findings) != 1 || d.Meta.Cached || d.Meta.Usage.In != 100 || d.Meta.Usage.Out != 50 || d.Meta.Cost != 200 {
		t.Errorf("envelope = findings %d cached %v usage %+v cost %v", len(d.Findings), d.Meta.Cached, d.Meta.Usage, d.Meta.Cost)
	}
	// "warnings":[] must be serialized, never null.
	if !strings.Contains(stdout, `"warnings": []`) {
		t.Errorf("warnings must serialize as [], got:\n%s", stdout)
	}

	// -o: same document, in the file, nothing on stdout.
	e2 := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	global.output = filepath.Join(t.TempDir(), "report.json")
	useReportFake(t, reportFakeState{responses: []string{reportFindings("low")}})
	stdout2, stderr2, err := runPosting(t, e2, remotePRFlags{}, newPostingGH("stable"))
	if err != nil {
		t.Fatalf("remote pr --json -o: %v\n%s", err, stderr2)
	}
	if stdout2 != "" {
		t.Errorf("stdout must be empty with -o, got %q", stdout2)
	}
	data, rerr := os.ReadFile(global.output)
	if rerr != nil {
		t.Fatalf("report file: %v", rerr)
	}
	if got := decodeDoc(t, string(data)); got.Remote == nil || got.Remote.Status != "submitted" {
		t.Errorf("report file remote = %+v", got.Remote)
	}
}

// Without --json, stdout stays empty and the footer still reports the run.
func TestRemotePRReportAbsentWithoutJSON(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
	stdout, stderr, err := runPosting(t, e, remotePRFlags{}, newPostingGH("stable"))
	if err != nil {
		t.Fatalf("remote pr: %v\n%s", err, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout must stay empty without --json, got %q", stdout)
	}
	if !strings.Contains(stderr, "150 tokens") || !strings.Contains(stderr, "$200.0000") {
		t.Errorf("footer must report the run; got:\n%s", stderr)
	}
}

// --no-post --json is the plain v1 document: no remote container.
func TestRemotePRReportNoPostJSONHasNoRemoteContainer(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	withGlobal(t, globalFlags{color: "never", json: true, provider: reportFakeName, noCostCheck: true})
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
	stdout, stderr, err := runRemotePRNoPost(t, e, &fakeGH{diff: sampleDiff})
	if err != nil {
		t.Fatalf("--no-post --json: %v\n%s", err, stderr)
	}
	if strings.Contains(stdout, `"remote"`) {
		t.Errorf("--no-post must not emit the remote container:\n%s", stdout)
	}
}

// The footer and the document come from one accumulator, so a superseded
// race-retry attempt is counted in both and they agree (ADR-0048 §9).
func TestRemotePRReportRaceRetrySumsUsageAcrossAttempts(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
	r := newPostingGH("stable")
	r.commitsSeq = []string{`{"commits":[{"oid":"moved1"}]}`, `{"commits":[{"oid":"moved1"}]}`}

	stdout, stderr, err := runPosting(t, e, remotePRFlags{}, r)
	if err != nil {
		t.Fatalf("remote pr: %v\n%s", err, stderr)
	}
	d := decodeDoc(t, stdout)
	if d.Meta.Usage.In != 200 || d.Meta.Usage.Out != 100 || d.Meta.Cost != 400 {
		t.Errorf("meta must sum both attempts: usage %+v cost %v", d.Meta.Usage, d.Meta.Cost)
	}
	if d.Remote.Review.Attempts != 2 || d.Remote.HeadOID != "moved1" {
		t.Errorf("review.attempts = %d head_oid = %q; want 2, moved1", d.Remote.Review.Attempts, d.Remote.HeadOID)
	}
	if _, ok := d.warning("race_retry"); !ok {
		t.Errorf("race_retry warning missing: %+v", d.Remote.Warnings)
	}
	if !strings.Contains(stderr, "300 tokens") || !strings.Contains(stderr, "$400.0000") {
		t.Errorf("footer must show the same summed figures as meta; got:\n%s", stderr)
	}
}

func TestRemotePRReportTooVolatileStillReportsTheSpend(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
	r := newPostingGH("stable")
	r.commitsSeq = []string{`{"commits":[{"oid":"moved1"}]}`, `{"commits":[{"oid":"moved2"}]}`}

	stdout, _, err := runPosting(t, e, remotePRFlags{}, r)
	if err == nil || !strings.Contains(err.Error(), "twice during review") {
		t.Fatalf("want too-volatile abort, got %v", err)
	}
	d := decodeDoc(t, stdout)
	if d.Remote.Status != "too_volatile" || d.Remote.Review.Attempts != 2 || d.Meta.Cost != 400 {
		t.Errorf("status %q attempts %d cost %v", d.Remote.Status, d.Remote.Review.Attempts, d.Meta.Cost)
	}
	if d.Remote.Sync != nil || d.Remote.Verdict != "" || len(d.Findings) != 0 {
		t.Errorf("nothing was posted: sync %+v verdict %q findings %d", d.Remote.Sync, d.Remote.Verdict, len(d.Findings))
	}
	if r.callCount("pr review") != 0 {
		t.Errorf("no verdict on double race; calls=%v", r.calls)
	}
}

func TestRemotePRReportDegradedCarriesRawContent(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{responses: []string{"this is not json at all"}})
	r := newPostingGH("stable")

	stdout, _, err := runPosting(t, e, remotePRFlags{}, r)
	if err == nil || !strings.Contains(err.Error(), "did not return structured findings") {
		t.Fatalf("want degraded error, got %v", err)
	}
	d := decodeDoc(t, stdout)
	if d.Remote.Status != "degraded" || d.Content != "this is not json at all" {
		t.Errorf("status %q content %q", d.Remote.Status, d.Content)
	}
	if d.Meta.DegradeReason == "" || d.Meta.RetryCount != 1 {
		t.Errorf("degrade_reason %q retry_count %d", d.Meta.DegradeReason, d.Meta.RetryCount)
	}
	// Original call plus the ADR-0031 repair call are both billed.
	if d.Meta.Usage.In != 200 || d.Meta.Cost != 400 {
		t.Errorf("usage %+v cost %v; want both calls summed", d.Meta.Usage, d.Meta.Cost)
	}
	if len(d.Findings) != 0 || d.Remote.Sync != nil || r.callCount("pr review") != 0 {
		t.Errorf("nothing may be posted on a degraded run; calls=%v", r.calls)
	}
}

// Once a provider call returned, even a later provider failure writes the
// document, and the first call's usage is in it.
func TestRemotePRReportFailedAfterAProviderCallWritesDocument(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}, errOnCall: 2})
	r := newPostingGH("stable")
	r.commitsSeq = []string{`{"commits":[{"oid":"moved1"}]}`}

	stdout, _, err := runPosting(t, e, remotePRFlags{}, r)
	if err == nil || !strings.Contains(err.Error(), "provider exploded") {
		t.Fatalf("want the provider error, got %v", err)
	}
	d := decodeDoc(t, stdout)
	if d.Remote.Status != "failed" || d.Meta.Cost != 200 || d.Remote.Review.Attempts != 2 {
		t.Errorf("status %q cost %v attempts %d", d.Remote.Status, d.Meta.Cost, d.Remote.Review.Attempts)
	}
}

// Every ending before a provider call returned writes no document.
func TestRemotePRReportNoDocumentBeforeAProviderCallReturned(t *testing.T) {
	t.Run("self PR", func(t *testing.T) {
		e := newCLIEnv(t)
		stubGHOnPath(t)
		global.json = true
		useReportFake(t, reportFakeState{})
		r := &fakeGH{whoami: "tester", prMeta: prMetaJSON("tester", "stable")}
		stdout, _, err := runPosting(t, e, remotePRFlags{}, r)
		if err == nil || stdout != "" {
			t.Errorf("err = %v stdout = %q; want an error and no document", err, stdout)
		}
	})
	t.Run("first provider call fails", func(t *testing.T) {
		e := newCLIEnv(t)
		stubGHOnPath(t)
		global.json = true
		useReportFake(t, reportFakeState{errOnCall: 1})
		stdout, _, err := runPosting(t, e, remotePRFlags{}, newPostingGH("stable"))
		if err == nil || stdout != "" {
			t.Errorf("err = %v stdout = %q; want an error and no document", err, stdout)
		}
	})
}

func TestRemotePRReportNoNewCommitsWritesZeroMetaDocument(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{})
	r := newSinceGH()
	r.reviews = reviewsJSON(headOID) // the head was already reviewed

	stdout, stderr, err := runPosting(t, e, remotePRFlags{sinceLastReview: true}, r)
	if err != nil {
		t.Fatalf("remote pr: %v\n%s", err, stderr)
	}
	d := decodeDoc(t, stdout)
	rep := d.Remote
	if rep.Status != "no_new_commits" || rep.HeadOID != headOID || rep.Review.Mode != "none" || rep.Review.Attempts != 0 {
		t.Errorf("remote = %+v", rep)
	}
	if rep.Sync != nil || rep.Verdict != "" || d.Meta.Cost != 0 || d.Meta.Usage.In != 0 || len(d.Findings) != 0 {
		t.Errorf("a pre-review no_new_commits run carries zero meta: %+v", d)
	}
	if r.callCount("pr review") != 0 {
		t.Errorf("nothing to submit; calls=%v", r.calls)
	}
}

func TestRemotePRReportIncrementalAndFallbackReasons(t *testing.T) {
	cases := []struct {
		name       string
		since      bool
		edit       func(*fakeGH)
		wantMode   string
		wantReason string
		wantPrev   string
	}{
		{"not requested", false, func(*fakeGH) {}, "full", "not_requested", ""},
		{"incremental", true, func(*fakeGH) {}, "incremental", "", prevOID},
		{"no marker", true, func(r *fakeGH) {
			r.reviews = `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[{"body":"x","viewerDidAuthor":true}]}}}}}`
		}, "full", "no_marker", ""},
		{"lookup failed", true, func(r *fakeGH) { r.reviewsErr = errors.New("graphql down") }, "full", "marker_lookup_failed", ""},
		{"compare failed", true, func(r *fakeGH) { r.compare = "" }, "full", "compare_failed", ""},
		{"not ahead", true, func(r *fakeGH) { r.compare = compareJSON("diverged", 1) }, "full", "not_ahead", ""},
		{"merge commit", true, func(r *fakeGH) { r.compare = compareJSON("ahead", 1, 2, 1) }, "full", "merge_commit", ""},
		{"diff failed", true, func(r *fakeGH) { r.compareDErr = errors.New("too large") }, "full", "diff_failed", ""},
		{"incomplete", true, func(r *fakeGH) {
			r.compare = `{"status":"ahead","total_commits":300,"commits":[{"sha":"c0","parents":[{"sha":"p0"}]}]}`
		}, "full", "compare_incomplete", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCLIEnv(t)
			stubGHOnPath(t)
			global.json = true
			useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
			r := newSinceGH()
			tc.edit(r)
			stdout, stderr, err := runPosting(t, e, remotePRFlags{sinceLastReview: tc.since}, r)
			if err != nil {
				t.Fatalf("remote pr: %v\n%s", err, stderr)
			}
			rv := decodeDoc(t, stdout).Remote.Review
			if rv.Mode != tc.wantMode || rv.FullReason != tc.wantReason || rv.PreviousOID != tc.wantPrev {
				t.Errorf("review = %+v; want mode %q reason %q previous %q", rv, tc.wantMode, tc.wantReason, tc.wantPrev)
			}
		})
	}
}

// Warnings are built from the run's conditions: they appear under --quiet,
// the secret warning carries a count and never the matched text, and a failed
// comment post surfaces as counters plus a warning (ADR-0048 §2).
func TestRemotePRReportWarningsAndSyncCounters(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	global.quiet = true
	global.failOn = "high"
	useReportFake(t, reportFakeState{responses: []string{reportFindings("low")}})
	secret := "AK" + "IA" + "EXAMPLE0000000Z123"
	r := newPostingGH("stable")
	r.diff = "diff --git a/mock.go b/mock.go\n" +
		"new file mode 100644\n" +
		"index 0000000..2222222\n" +
		"--- /dev/null\n" +
		"+++ b/mock.go\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+package mock // " + secret + "\n" +
		"+var X = 1\n"
	r.commentErr = errors.New("HTTP 422 unprocessable")

	stdout, stderr, err := runPosting(t, e, remotePRFlags{}, r)
	if err != nil {
		t.Fatalf("remote pr: %v\n%s", err, stderr)
	}
	if strings.Contains(stdout, secret) {
		t.Errorf("the document must never carry matched secret text")
	}
	d := decodeDoc(t, stdout)
	if w, ok := d.warning("secrets_detected"); !ok || w.Count != 1 || w.Detail != "" || w.Ref != "" {
		t.Errorf("secrets_detected = %+v (present %v)", w, ok)
	}
	if _, ok := d.warning("fail_on_ignored"); !ok {
		t.Errorf("fail_on_ignored missing under --quiet: %+v", d.Remote.Warnings)
	}
	w, ok := d.warning("comment_post_failed")
	if !ok || w.Ref != "mock.go:1" || !strings.Contains(w.Detail, "422") {
		t.Errorf("comment_post_failed = %+v (present %v)", w, ok)
	}
	if s := d.Remote.Sync; s == nil || s.PostFailed != 1 || s.Posted != 0 {
		t.Errorf("sync = %+v; want post_failed 1", s)
	}
	if d.Remote.Status != "submitted" {
		t.Errorf("a failed comment post must not abort the verdict; status %q", d.Remote.Status)
	}
}

// V8 (ADR-0048 §6): remote pr never executes review.sandbox_command. The gate
// is set for the whole run, on both paths, and restored afterwards.
type sandboxGateProbe struct {
	inner remote.Runner
	seen  []bool
}

func (p *sandboxGateProbe) Run(ctx context.Context, args ...string) ([]byte, error) {
	p.seen = append(p.seen, global.noSandbox)
	return p.inner.Run(ctx, args...)
}

func TestRemotePRNeverBuildsTheSandboxRunner(t *testing.T) {
	for _, noPost := range []bool{false, true} {
		name := "posting"
		if noPost {
			name = "no-post"
		}
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			stubGHOnPath(t)
			useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
			global.sandboxRerun = 5
			global.noCostCheck = true // the fake's price is deliberately huge

			built := 0
			prev := buildSandboxRunner
			buildSandboxRunner = func(*appContext) (*sandboxRunner, error) {
				built++
				return nil, errors.New("the sandbox runner must not be built by remote pr")
			}
			t.Cleanup(func() { buildSandboxRunner = prev })

			probe := &sandboxGateProbe{inner: newPostingGH("stable")}
			oldWd, _ := os.Getwd()
			_ = os.Chdir(e.repoRoot)
			t.Cleanup(func() { _ = os.Chdir(oldWd) })
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := runRemotePR(cmd, "42", remotePRFlags{noPost: noPost}, probe); err != nil {
				t.Fatalf("remote pr: %v", err)
			}

			if built != 0 {
				t.Errorf("buildSandboxRunner called %d times", built)
			}
			if len(probe.seen) == 0 {
				t.Fatal("the probe saw no gh call")
			}
			for i, on := range probe.seen {
				if !on {
					t.Errorf("gh call %d ran with global.noSandbox = false; the gate must hold for the whole run", i)
				}
			}
			if global.noSandbox {
				t.Errorf("global.noSandbox leaked past runRemotePR")
			}
			app := sandboxTestApp(t)
			app.Config.Review.SandboxRerun = 5
			if sandboxRerunCount(bareCmd(), app) != 5 {
				t.Errorf("after the run the gate must be released")
			}
		})
	}
}

// An unwritable --output must fail before anything is written to GitHub: a
// review that was already submitted must never end as "exit 1, empty stdout",
// which a consumer reads as "nothing happened" and retries.
func TestRemotePRReportUnwritableOutputFailsBeforeAnyGHCall(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	global.output = filepath.Join(t.TempDir(), "missing-dir", "report.json")
	useReportFake(t, reportFakeState{responses: []string{reportFindings("info")}})
	r := newPostingGH("stable")

	stdout, _, err := runPosting(t, e, remotePRFlags{}, r)
	if err == nil || !strings.Contains(err.Error(), "--output") {
		t.Fatalf("want an --output error, got %v", err)
	}
	if len(r.calls) != 0 || stdout != "" {
		t.Errorf("nothing may reach GitHub; calls=%v stdout=%q", r.calls, stdout)
	}
	if reportFake.calls != 0 {
		t.Errorf("the provider must not be called, got %d calls", reportFake.calls)
	}
}

// A run that writes no document never leaves a stale document behind: a file
// the preflight created is removed again, an existing one is emptied, so
// "empty file" means what "empty stdout" means.
func TestRemotePRReportOutputClearedWhenNoDocumentIsWritten(t *testing.T) {
	selfPR := func() *fakeGH { return &fakeGH{whoami: "tester", prMeta: prMetaJSON("tester", "stable")} }

	t.Run("created file is removed", func(t *testing.T) {
		e := newCLIEnv(t)
		stubGHOnPath(t)
		global.json = true
		global.output = filepath.Join(t.TempDir(), "report.json")
		useReportFake(t, reportFakeState{})
		if _, _, err := runPosting(t, e, remotePRFlags{}, selfPR()); err == nil {
			t.Fatal("self-PR must fail")
		}
		if _, err := os.Stat(global.output); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a document-less run must not leave --output behind; stat err = %v", err)
		}
	})
	t.Run("existing file is emptied", func(t *testing.T) {
		e := newCLIEnv(t)
		stubGHOnPath(t)
		global.json = true
		global.output = filepath.Join(t.TempDir(), "report.json")
		if err := os.WriteFile(global.output, []byte("previous"), 0o644); err != nil {
			t.Fatal(err)
		}
		useReportFake(t, reportFakeState{})
		if _, _, err := runPosting(t, e, remotePRFlags{}, selfPR()); err == nil {
			t.Fatal("self-PR must fail")
		}
		got, err := os.ReadFile(global.output)
		if err != nil {
			t.Fatalf("the existing --output must still exist: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("a stale document must not survive a document-less run: %q", got)
		}
	})
}

// --since-last-review found a marker, but the PR's filtered diff is empty, so
// the incremental range is never evaluated: a full review with its own reason.
func TestRemotePRReportEmptyDiffWithMarkerReportsEmptyDiff(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	global.json = true
	useReportFake(t, reportFakeState{})
	r := newSinceGH()
	r.diff = newFileDiff("go.sum", "Lockfile") // lockfiles are filtered out

	stdout, stderr, err := runPosting(t, e, remotePRFlags{sinceLastReview: true}, r)
	if err != nil {
		t.Fatalf("remote pr: %v\n%s", err, stderr)
	}
	d := decodeDoc(t, stdout)
	rv := d.Remote.Review
	if rv.Mode != "full" || rv.FullReason != "empty_diff" || rv.Attempts != 0 || rv.PreviousOID != "" {
		t.Errorf("review = %+v; want full / empty_diff / 0 attempts", rv)
	}
	if reportFake.calls != 0 || r.callCount("/compare/") != 0 {
		t.Errorf("an empty diff must not reach the provider or the compare API; calls=%v", r.calls)
	}
}

// The ceiling is a safety feature: a value that reaches submitPRReview
// unvalidated must fail closed (never approve), not lift the cap.
func TestRemotePRMaxVerdictUnvalidatedValueFailsClosed(t *testing.T) {
	r := &fakeGH{}
	submitFlagsForTest(t, r, remotePRFlags{maxVerdict: "comnent"})
	if r.callCount("--approve") != 0 || r.callCount("pr review 42 --comment") != 1 {
		t.Errorf("a malformed ceiling must still cap the review; calls=%v", r.calls)
	}
}

func submitFlagsForTest(t *testing.T, r *fakeGH, f remotePRFlags) {
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
	if err := submitPRReview(context.Background(), r, "42", f, meta, "oid",
		prReviewResult{findings: []render.Finding{}, anchors: parsed.Anchors()}, "", "tester", &appContext{Catalog: cat}, prog, nil); err != nil {
		t.Fatalf("submitPRReview: %v", err)
	}
}
