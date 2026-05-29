// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// fakeRunner records calls and replays scripted (stdout, error) pairs in
// order, so tests can drive sequential gh invocations (e.g. race-retry)
// without a live network.
type fakeRunner struct {
	calls [][]string
	out   [][]byte
	errs  []error
	idx   int
}

func (f *fakeRunner) Run(_ context.Context, args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	i := f.idx
	f.idx++
	var out []byte
	var err error
	if i < len(f.out) {
		out = f.out[i]
	}
	if i < len(f.errs) {
		err = f.errs[i]
	}
	return out, err
}

// lastCall returns the args of the most recent Run invocation.
func (f *fakeRunner) lastCall() []string {
	if len(f.calls) == 0 {
		return nil
	}
	return f.calls[len(f.calls)-1]
}

func argsContain(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func TestEnsureGHMissing(t *testing.T) {
	t.Setenv("PATH", "")
	if err := EnsureGH(); !errors.Is(err, ErrGHMissing) {
		t.Fatalf("want ErrGHMissing with empty PATH, got %v", err)
	}
}

func TestBuildCommentBody(t *testing.T) {
	f := render.Finding{
		Severity:    render.SeverityHigh,
		Title:       "Unvalidated input",
		Description: "The handler trusts the query param.",
		Suggestion:  "Validate and bound the id before use.",
	}
	got := BuildCommentBody(f, "octocat")
	want := "[HIGH] - Unvalidated input\n" +
		"The handler trusts the query param.\n" +
		"Validate and bound the id before use. @octocat by #CommitBrief"
	if got != want {
		t.Fatalf("comment body mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildReviewBody(t *testing.T) {
	cases := map[Verdict]string{
		VerdictApprove:        "@octocat by #CommitBrief",
		VerdictComment:        "It must be checked by the human eye. @octocat by #CommitBrief",
		VerdictRequestChanges: "We can revisit it after we've solved the problems. @octocat by #CommitBrief",
	}
	for v, want := range cases {
		if got := BuildReviewBody(v, "octocat"); got != want {
			t.Errorf("verdict %d body = %q, want %q", v, got, want)
		}
	}
}

func TestSubmitReviewUsesCorrectFlag(t *testing.T) {
	cases := map[Verdict]string{
		VerdictApprove:        "--approve",
		VerdictComment:        "--comment",
		VerdictRequestChanges: "--request-changes",
	}
	for v, flag := range cases {
		fr := &fakeRunner{}
		if err := SubmitReview(context.Background(), fr, "42", "", v, "body"); err != nil {
			t.Fatalf("SubmitReview: %v", err)
		}
		if !argsContain(fr.lastCall(), flag) {
			t.Errorf("verdict %d: args %v missing flag %q", v, fr.lastCall(), flag)
		}
	}
}

func TestSubmitReviewThreadsRepo(t *testing.T) {
	fr := &fakeRunner{}
	if err := SubmitReview(context.Background(), fr, "42", "owner/repo", VerdictApprove, "b"); err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	got := strings.Join(fr.lastCall(), " ")
	if !strings.Contains(got, "--repo owner/repo") {
		t.Errorf("expected --repo owner/repo in %q", got)
	}
}

func TestPostCommentSendsSideRight(t *testing.T) {
	fr := &fakeRunner{}
	c := CommentRequest{
		RepoSlug: "octo/demo", PRNumber: 7,
		CommitID: "deadbeef", Path: "main.go", Line: 12, Body: "x",
	}
	if err := PostComment(context.Background(), fr, c); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	args := fr.lastCall()
	if !argsContain(args, "side=RIGHT") {
		t.Errorf("missing unconditional side=RIGHT in %v", args)
	}
	if !argsContain(args, "/repos/octo/demo/pulls/7/comments") {
		t.Errorf("wrong endpoint in %v", args)
	}
	if !argsContain(args, "commit_id=deadbeef") {
		t.Errorf("missing commit_id in %v", args)
	}
}

func TestPostCommentSendsSideLeft(t *testing.T) {
	fr := &fakeRunner{}
	c := CommentRequest{
		RepoSlug: "octo/demo", PRNumber: 7,
		CommitID: "deadbeef", Path: "main.go", Line: 12, Side: "LEFT", Body: "x",
	}
	if err := PostComment(context.Background(), fr, c); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if !argsContain(fr.lastCall(), "side=LEFT") {
		t.Errorf("explicit Side=LEFT not propagated in %v", fr.lastCall())
	}
}

func TestBuildUnanchoredSection(t *testing.T) {
	if got := BuildUnanchoredSection(nil); got != "" {
		t.Errorf("empty input must yield empty section, got %q", got)
	}
	findings := []render.Finding{{
		Severity:    render.SeverityCritical,
		File:        "app/x.go",
		Line:        42,
		Title:       "Hardcoded secret",
		Description: "A token is committed in source.",
		Suggestion:  "Move it to an env var.",
	}}
	got := BuildUnanchoredSection(findings)
	for _, want := range []string{
		unanchoredHeading,
		"[CRITICAL] app/x.go:42",
		"Hardcoded secret",
		"A token is committed in source.",
		"Move it to an env var.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("section missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestPostCommentPropagatesError(t *testing.T) {
	fr := &fakeRunner{errs: []error{errors.New("422 Unprocessable Entity")}}
	err := PostComment(context.Background(), fr, CommentRequest{RepoSlug: "o/r", PRNumber: 1, Path: "f", Line: 1})
	if err == nil {
		t.Fatal("expected error to propagate so the caller can count/skip it")
	}
}

func TestFetchPRMetaUnmarshal(t *testing.T) {
	canned := []byte(`{
		"number": 42,
		"author": {"login": "contributor"},
		"url": "https://github.com/CommitBrief/web/pull/42",
		"headRepository": {"name": "web", "owner": {"login": "fork-user"}},
		"commits": [{"oid": "aaa"}, {"oid": "bbb"}]
	}`)
	fr := &fakeRunner{out: [][]byte{canned}}
	m, err := FetchPRMeta(context.Background(), fr, "42", "")
	if err != nil {
		t.Fatalf("FetchPRMeta: %v", err)
	}
	if m.Number != 42 {
		t.Errorf("number = %d, want 42", m.Number)
	}
	if m.AuthorLogin() != "contributor" {
		t.Errorf("author = %q, want contributor", m.AuthorLogin())
	}
	if got := m.BaseSlug(); got != "CommitBrief/web" {
		t.Errorf("base slug = %q, want CommitBrief/web", got)
	}
	if got := m.LastOID(); got != "bbb" {
		t.Errorf("last oid = %q, want bbb", got)
	}
}

func TestBaseSlug(t *testing.T) {
	cases := []struct {
		url, want string
	}{
		{"https://github.com/CommitBrief/web/pull/42", "CommitBrief/web"},
		{"https://github.com/greenglobaltr/BulutApi/pull/1652", "greenglobaltr/BulutApi"},
		{"https://ghe.example.com/org/repo/pull/3", "org/repo"},
		{"", ""},
		{"not-a-url", ""},
	}
	for _, c := range cases {
		if got := (PRMeta{URL: c.url}).BaseSlug(); got != c.want {
			t.Errorf("BaseSlug(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

func TestLastOIDEmpty(t *testing.T) {
	if got := (PRMeta{}).LastOID(); got != "" {
		t.Errorf("empty PRMeta LastOID = %q, want empty", got)
	}
}

func TestWhoamiTrimsNewline(t *testing.T) {
	fr := &fakeRunner{out: [][]byte{[]byte("octocat\n")}}
	got, err := Whoami(context.Background(), fr)
	if err != nil {
		t.Fatalf("Whoami: %v", err)
	}
	if got != "octocat" {
		t.Errorf("whoami = %q, want octocat", got)
	}
}
