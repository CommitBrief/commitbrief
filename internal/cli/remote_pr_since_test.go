// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/provider/mock"
	"github.com/CommitBrief/commitbrief/internal/remote"
)

const (
	prevOID = "aaaaaaa1"
	headOID = "bbbbbbb2"
)

// newFileDiff renders a one-hunk diff that adds a file whose only content is
// the given marker line, so a test can tell from the provider prompt which
// files the review actually saw.
func newFileDiff(path, marker string) string {
	return "diff --git a/" + path + " b/" + path + "\n" +
		"new file mode 100644\n" +
		"index 0000000..3333333\n" +
		"--- /dev/null\n" +
		"+++ b/" + path + "\n" +
		"@@ -0,0 +1,2 @@\n" +
		"+package x\n" +
		"+var " + marker + " = 1\n"
}

// fullPRDiff is a PR of 40 commits' worth of files plus mock.go.
func fullPRDiff() string {
	var b strings.Builder
	b.WriteString(sampleDiff)
	for i := 0; i < 40; i++ {
		b.WriteString(newFileDiff(fmt.Sprintf("old%02d.go", i), fmt.Sprintf("FullOnly%02d", i)))
	}
	b.WriteString(newFileDiff("new.go", "DeltaOnly"))
	return b.String()
}

func reviewsJSON(oid string) string {
	body, _ := json.Marshal("looks fine\n" + remote.ReviewedMarker(oid))
	return `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[` +
		`{"body":"someone else","viewerDidAuthor":false},` +
		`{"body":` + string(body) + `,"viewerDidAuthor":true}]}}}}}`
}

// compareJSON builds a compare response; each parents entry is the parent
// count of one commit.
func compareJSON(status string, parents ...int) string {
	var commits []string
	for i, n := range parents {
		var ps []string
		for j := 0; j < n; j++ {
			ps = append(ps, fmt.Sprintf(`{"sha":"p%d_%d"}`, i, j))
		}
		commits = append(commits, fmt.Sprintf(`{"sha":"c%d","parents":[%s]}`, i, strings.Join(ps, ",")))
	}
	return fmt.Sprintf(`{"status":%q,"total_commits":%d,"commits":[%s]}`, status, len(parents), strings.Join(commits, ","))
}

// sinceThread renders one review-thread node CommitBrief owns.
func sinceThread(id, path, fp string, outdated bool, state string) string {
	marker := "<!-- commitbrief:fp=" + fp + " v=1"
	if state != "" {
		marker += " state=" + state
	}
	marker += " -->"
	body, _ := json.Marshal("[INFO] - old\nd\n\U0001F4A1 s @tester by #commitbrief\n" + marker)
	return fmt.Sprintf(`{"id":%q,"path":%q,"isResolved":false,"isOutdated":%t,`+
		`"viewerCanResolve":true,"viewerCanUnresolve":true,"viewerCanReply":true,`+
		`"comments":{"nodes":[{"databaseId":%d,"body":%s,"viewerDidAuthor":true,"viewerCanUpdate":true}]},`+
		`"recent":{"nodes":[{"databaseId":%d,"body":%s,"viewerDidAuthor":true}]}}`,
		id, path, outdated, len(id), body, len(id), body)
}

func threadsJSON(nodes ...string) string {
	return `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[` + strings.Join(nodes, ",") + `]}}}}}`
}

// sinceRun drives runRemotePR with --since-last-review and returns everything
// the user would have seen on stderr.
func sinceRun(t *testing.T, e *cliEnv, r *fakeGH) (string, error) {
	t.Helper()
	mock.ResetRequests()
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
	runErr := runRemotePR(cmd, "42", remotePRFlags{requestChangesOn: "critical", sinceLastReview: true}, r)

	os.Stderr = oldErr
	_ = pw.Close()
	return errBuf.String() + <-done, runErr
}

func repeatedCommits(oid string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = `{"commits":[{"oid":"` + oid + `"}]}`
	}
	return out
}

func newSinceGH() *fakeGH {
	return &fakeGH{
		whoami:      "tester",
		prMeta:      prMetaJSON("contributor", headOID),
		diff:        fullPRDiff(),
		reviews:     reviewsJSON(prevOID),
		compare:     compareJSON("ahead", 1),
		compareDif:  newFileDiff("new.go", "DeltaOnly"),
		commitsSeq:  repeatedCommits(headOID, 6),
		threads:     threadsJSON(),
		compareErr:  nil,
		compareDErr: nil,
	}
}

func lastPrompt(t *testing.T) string {
	t.Helper()
	reqs := mock.Requests()
	if len(reqs) == 0 {
		t.Fatal("provider was never called")
	}
	return reqs[len(reqs)-1].UserPrompt
}

func TestSinceLastReviewReviewsOnlyTheNewCommit(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatalf("since-last-review: %v", err)
	}
	if n := len(mock.Requests()); n != 1 {
		t.Fatalf("want exactly one provider call, got %d", n)
	}
	p := lastPrompt(t)
	if !strings.Contains(p, "DeltaOnly") {
		t.Errorf("the new commit's diff must reach the provider")
	}
	for _, leaked := range []string{"FullOnly00", "FullOnly39", "var X = 1"} {
		if strings.Contains(p, leaked) {
			t.Errorf("the provider saw earlier-reviewed code (%q)", leaked)
		}
	}
	if r.callCount("compare/"+prevOID+"..."+headOID) == 0 {
		t.Errorf("compare was never requested; calls=%v", r.calls)
	}
}

func TestSinceLastReviewStampsReviewedMarker(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatalf("since-last-review: %v", err)
	}
	if r.callCount(remote.ReviewedMarker(headOID)) == 0 {
		t.Errorf("review body must carry the reviewed marker for the head OID; calls=%v", r.calls)
	}
}

func TestSinceLastReviewFallsBackToFullReview(t *testing.T) {
	cases := []struct {
		name string
		edit func(*fakeGH)
		info string
	}{
		{"no marker", func(r *fakeGH) {
			r.reviews = `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[{"body":"x","viewerDidAuthor":true}]}}}}}`
		}, "no earlier CommitBrief review"},
		{"force-push diverged", func(r *fakeGH) { r.compare = compareJSON("diverged", 1) }, "not an ancestor"},
		{"force-push behind", func(r *fakeGH) { r.compare = compareJSON("behind", 1) }, "not an ancestor"},
		{"compare 404", func(r *fakeGH) { r.compare = "" }, "could not compare"},
		{"merge commit", func(r *fakeGH) { r.compare = compareJSON("ahead", 1, 2, 1) }, "merge commit"},
		{"delta diff fails", func(r *fakeGH) { r.compareDErr = fmt.Errorf("diff too large") }, "could not fetch the diff"},
		{"review lookup fails", func(r *fakeGH) { r.reviewsErr = fmt.Errorf("graphql down") }, "could not read earlier reviews"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newCLIEnv(t)
			stubGHOnPath(t)
			r := newSinceGH()
			tc.edit(r)
			out, err := sinceRun(t, e, r)
			if err != nil {
				t.Fatalf("fallback must still succeed: %v", err)
			}
			if !strings.Contains(out, tc.info) {
				t.Errorf("want info line containing %q, got:\n%s", tc.info, out)
			}
			p := lastPrompt(t)
			if !strings.Contains(p, "FullOnly00") || !strings.Contains(p, "DeltaOnly") {
				t.Errorf("a fallback must review the whole PR diff")
			}
		})
	}
}

func TestSinceLastReviewSkipsWhenHeadUnchanged(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.reviews = reviewsJSON(headOID)
	out, err := sinceRun(t, e, r)
	if err != nil {
		t.Fatalf("an unchanged head is not an error: %v", err)
	}
	if !strings.Contains(out, "no new commits since last review") {
		t.Errorf("want the no-new-commits line, got:\n%s", out)
	}
	if n := len(mock.Requests()); n != 0 {
		t.Errorf("no provider call expected, got %d", n)
	}
	for _, write := range []string{"/comments", "--approve", "--comment", "--request-changes", "compare/"} {
		if r.callCount(write) != 0 {
			t.Errorf("nothing may be sent or compared (%q); calls=%v", write, r.calls)
		}
	}
}

func TestSinceLastReviewDoesNotResolveThreadOutsideDelta(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.threads = threadsJSON(sinceThread("OUT", "old01.go", fpOfTest("a"), true, ""))
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("resolveReviewThread") != 0 {
		t.Errorf("a thread outside the delta must stay open; calls=%v", r.calls)
	}
}

func TestSinceLastReviewDoesNotResolveLiveThreadInDelta(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.threads = threadsJSON(sinceThread("LIVE", "new.go", fpOfTest("a"), false, ""))
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("resolveReviewThread") != 0 {
		t.Errorf("a thread whose anchor is not outdated must stay open; calls=%v", r.calls)
	}
}

func TestSinceLastReviewResolvesOutdatedThreadInDelta(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.threads = threadsJSON(
		sinceThread("GONE", "new.go", fpOfTest("a"), true, ""),
		sinceThread("OUT", "old01.go", fpOfTest("b"), true, ""),
	)
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("id=GONE") != 1 {
		t.Errorf("the outdated thread inside the delta must be resolved once; calls=%v", r.calls)
	}
	if r.callCount("id=OUT") != 0 {
		t.Errorf("the thread outside the delta must be left alone; calls=%v", r.calls)
	}
}

func TestSinceLastReviewNeverTouchesHeldThread(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.threads = threadsJSON(sinceThread("HELD", "new.go", fpOfTest("a"), true, "reopened"))
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("resolveReviewThread") != 0 {
		t.Errorf("a held thread must never be resolved; calls=%v", r.calls)
	}
	if r.callCount("/replies") != 0 {
		t.Errorf("a held thread must get no reply; calls=%v", r.calls)
	}
}

func TestSinceLastReviewOpenHeldThreadBlocksApprove(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.threads = threadsJSON(sinceThread("HELD", "old01.go", fpOfTest("a"), false, "reopened"))
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("--approve") != 0 {
		t.Errorf("an open held thread must block the approve; calls=%v", r.calls)
	}
	if r.callCount("--comment") != 1 {
		t.Errorf("the incremental verdict must drop to comment; calls=%v", r.calls)
	}
}

func TestSinceLastReviewApprovesWhenNothingIsOpen(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("--approve") != 1 {
		t.Errorf("with no open thread the clean incremental run may approve; calls=%v", r.calls)
	}
}

func TestSinceLastReviewIgnoredWithNoPost(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	mock.ResetRequests()
	oldWd, _ := os.Getwd()
	_ = os.Chdir(e.repoRoot)
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	var out, errBuf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&out)
	cmd.SetErr(&errBuf)
	if err := runRemotePR(cmd, "42", remotePRFlags{noPost: true, sinceLastReview: true}, r); err != nil {
		t.Fatalf("--no-post: %v", err)
	}
	if r.callCount("compare/") != 0 || r.callCount("reviews(last:100)") != 0 {
		t.Errorf("--no-post must not consult review history; calls=%v", r.calls)
	}
	if !strings.Contains(lastPrompt(t), "FullOnly00") {
		t.Errorf("--no-post reviews the whole PR")
	}
}

func TestSinceLastReviewThreadReadFailureBlocksApprove(t *testing.T) {
	e := newCLIEnv(t)
	stubGHOnPath(t)
	r := newSinceGH()
	r.threads = "{garbage"
	if _, err := sinceRun(t, e, r); err != nil {
		t.Fatal(err)
	}
	if r.callCount("--approve") != 0 || r.callCount("--comment") != 1 {
		t.Errorf("an unreadable thread list cannot prove nothing is open; calls=%v", r.calls)
	}
}
