// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

func TestRemotePRMarkerRoundTrip(t *testing.T) {
	fp := strings.Repeat("ab", 32)
	m := NewMarker(fp)
	if got := m.String(); got != "<!-- commitbrief:fp="+fp+" v=1 -->" {
		t.Fatalf("String = %q", got)
	}
	body := withMarker("[HIGH] - x\nbody", m)
	text, got, ok := SplitMarker(body)
	if !ok || got.FP != fp || text != "[HIGH] - x\nbody" {
		t.Fatalf("SplitMarker = %q %v %v", text, got, ok)
	}
}

func TestRemotePRMarkerForeignShapes(t *testing.T) {
	fp := strings.Repeat("a", 64)
	cases := map[string]string{
		"no marker":    "just text",
		"v=2":          "t\n<!-- commitbrief:fp=" + fp + " v=2 -->",
		"missing v":    "t\n<!-- commitbrief:fp=" + fp + " -->",
		"short fp":     "t\n<!-- commitbrief:fp=abc v=1 -->",
		"uppercase fp": "t\n<!-- commitbrief:fp=" + strings.Repeat("A", 64) + " v=1 -->",
		"garbled":      "t\n<!-- commitbrief:fp=" + fp + " v=1",
	}
	for name, body := range cases {
		if _, _, ok := SplitMarker(body); ok {
			t.Errorf("%s: treated as ours", name)
		}
	}
}

func TestRemotePRMarkerKeepsUnknownTokens(t *testing.T) {
	fp := strings.Repeat("c", 64)
	body := "text\n<!-- commitbrief:fp=" + fp + " v=1 future=yes state=resolved last=5 -->"
	_, m, ok := SplitMarker(body)
	if !ok || m.State() != "resolved" || m.Last() != 5 {
		t.Fatalf("m=%v ok=%v", m, ok)
	}
	re := m.WithState("reopened", 0)
	if !reflect.DeepEqual(re.Attrs, []string{"future=yes", "state=reopened"}) {
		t.Fatalf("attrs = %v", re.Attrs)
	}
	cleared := m.WithState("", 0)
	if !reflect.DeepEqual(cleared.Attrs, []string{"future=yes"}) {
		t.Fatalf("attrs = %v", cleared.Attrs)
	}
	// rewriteMarker leaves every other byte alone.
	raw := "human edited text  \r\n" + m.String() + "\ntrailer"
	out := rewriteMarker(raw, re)
	if !strings.HasPrefix(out, "human edited text  \r\n") || !strings.HasSuffix(out, "\ntrailer") {
		t.Fatalf("rewrite touched surrounding text: %q", out)
	}
	if _, got, _ := SplitMarker(out); got.State() != "reopened" {
		t.Fatalf("state = %q", got.State())
	}
}

func TestRemotePRMarkerLastMalformed(t *testing.T) {
	for _, v := range []string{"last=abc", "last=-3", "last=0", "last="} {
		m := Marker{Attrs: []string{v}}
		if m.Last() != 0 {
			t.Errorf("%s -> %d", v, m.Last())
		}
	}
}

func TestRemotePRMarkerLastWins(t *testing.T) {
	fp1, fp2 := strings.Repeat("1", 64), strings.Repeat("2", 64)
	body := "<!-- commitbrief:fp=" + fp1 + " v=1 -->\nquoted\n<!-- commitbrief:fp=" + fp2 + " v=1 -->"
	if _, m, _ := SplitMarker(body); m.FP != fp2 {
		t.Fatalf("fp = %s", m.FP)
	}
}

func TestRemotePRNormalizeText(t *testing.T) {
	if got := NormalizeText("a  \r\nb\t\r\n\r\n"); got != "a\nb" {
		t.Fatalf("got %q", got)
	}
}

func TestRemotePRStatusReplyKind(t *testing.T) {
	if statusReplyKind("x <!-- commitbrief:gone v=1 -->") != "gone" ||
		statusReplyKind("<!-- commitbrief:back v=1 -->") != "back" ||
		statusReplyKind("<!-- commitbrief:gone v=2 -->") != "" ||
		statusReplyKind("plain") != "" {
		t.Fatal("statusReplyKind mismatch")
	}
}

func TestRemotePRBuildCommentBodyMarker(t *testing.T) {
	f := render.Finding{Severity: render.SeverityHigh, Title: "t", Description: "d", Suggestion: "s", Fingerprint: strings.Repeat("e", 64)}
	body := BuildCommentBody(f, "me")
	text, m, ok := SplitMarker(body)
	if !ok || m.FP != f.Fingerprint || len(m.Attrs) != 0 || text != CommentText(f, "me") {
		t.Fatalf("body = %q", body)
	}
	f.Fingerprint = "not-a-fingerprint"
	if strings.Contains(BuildCommentBody(f, "me"), "commitbrief:fp") {
		t.Fatal("invalid fingerprint must not produce a marker")
	}
	f.Fingerprint = ""
	if BuildCommentBody(f, "me") != CommentText(f, "me") {
		t.Fatal("no fingerprint: body must equal visible text")
	}
}

// --- FetchThreads ---------------------------------------------------------

type staticGH struct {
	out  string
	err  error
	args []string
}

func (s *staticGH) Run(_ context.Context, args ...string) ([]byte, error) {
	s.args = args
	return []byte(s.out), s.err
}

const page1 = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
 {"id":"T1","isResolved":false,"isOutdated":true,"viewerCanResolve":true,"viewerCanUnresolve":false,"viewerCanReply":true,
  "comments":{"nodes":[{"databaseId":11,"body":"root1","viewerDidAuthor":true,"viewerCanUpdate":true}]},
  "recent":{"nodes":[{"databaseId":11,"body":"root1","viewerDidAuthor":true},{"databaseId":12,"body":"reply","viewerDidAuthor":false}]}},
 {"id":"EMPTY","comments":{"nodes":[]},"recent":{"nodes":[]}}
]}}}}}`

const page2 = `{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[
 {"id":"T2","isResolved":true,"comments":{"nodes":[{"databaseId":21,"body":"root2"}]},"recent":{"nodes":[{"databaseId":21,"body":"root2"}]}}
]}}}}}`

func TestRemotePRFetchThreadsPaginated(t *testing.T) {
	g := &staticGH{out: page1 + "\n" + page2}
	ths, err := FetchThreads(context.Background(), g, "o/r", 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(ths) != 2 || ths[0].NodeID != "T1" || ths[1].NodeID != "T2" {
		t.Fatalf("threads = %+v", ths)
	}
	t1 := ths[0]
	if !t1.Outdated || !t1.CanResolve || t1.CanUnresolve || t1.Root.ID != 11 || !t1.Root.ViewerDidAuthor || !t1.Root.ViewerCanUpdate || t1.LastID() != 12 {
		t.Fatalf("t1 = %+v", t1)
	}
	if !ths[1].Resolved || ths[1].LastID() != 21 {
		t.Fatalf("t2 = %+v", ths[1])
	}
	joined := strings.Join(g.args, " ")
	for _, want := range []string{"graphql", "--paginate", "owner=o", "name=r", "number=7"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
}

func TestRemotePRFetchThreadsEdgeCases(t *testing.T) {
	ctx := context.Background()
	if ths, err := FetchThreads(ctx, &staticGH{}, "o/r", 1); err != nil || len(ths) != 0 {
		t.Fatalf("empty output: %v %v", ths, err)
	}
	if _, err := FetchThreads(ctx, &staticGH{out: "{not json"}, "o/r", 1); err == nil {
		t.Fatal("want decode error")
	}
	if _, err := FetchThreads(ctx, &staticGH{out: `{"errors":[{"message":"boom"}]}`}, "o/r", 1); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v", err)
	}
	if _, err := FetchThreads(ctx, &staticGH{out: `{"data":{"repository":{"pullRequest":null}}}`}, "o/r", 1); err == nil {
		t.Fatal("want not-found error")
	}
	if _, err := FetchThreads(ctx, &staticGH{err: errors.New("net")}, "o/r", 1); err == nil {
		t.Fatal("want runner error")
	}
	if _, err := FetchThreads(ctx, &staticGH{}, "bad", 1); err == nil {
		t.Fatal("want slug error")
	}
}

func TestRemotePRThreadWriteCalls(t *testing.T) {
	ctx := context.Background()
	g := &staticGH{}
	if err := UpdateComment(ctx, g, "o/r", 5, "new body"); err != nil {
		t.Fatal(err)
	}
	j := strings.Join(g.args, " ")
	if !strings.Contains(j, "PATCH") || !strings.Contains(j, "/repos/o/r/pulls/comments/5") || !strings.Contains(j, "body=new body") {
		t.Fatalf("update args: %s", j)
	}
	if err := ReplyToComment(ctx, g, "o/r", 7, 5, "hi"); err != nil {
		t.Fatal(err)
	}
	j = strings.Join(g.args, " ")
	if !strings.Contains(j, "POST") || !strings.Contains(j, "/repos/o/r/pulls/7/comments/5/replies") {
		t.Fatalf("reply args: %s", j)
	}
	if err := ResolveThread(ctx, g, "NODE"); err != nil || !strings.Contains(strings.Join(g.args, " "), "resolveReviewThread") {
		t.Fatalf("resolve args: %v", g.args)
	}
	if err := UnresolveThread(ctx, g, "NODE"); err != nil || !strings.Contains(strings.Join(g.args, " "), "unresolveReviewThread") {
		t.Fatalf("unresolve args: %v", g.args)
	}
}
