// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestReviewedMarkerRoundTrip(t *testing.T) {
	body := WithReviewedMarker("review text", "abcdef0")
	if !strings.HasSuffix(body, "<!-- commitbrief:reviewed=abcdef0 -->") {
		t.Fatalf("body = %q", body)
	}
	if oid, ok := ParseReviewedMarker(body); !ok || oid != "abcdef0" {
		t.Fatalf("parse = %q, %v", oid, ok)
	}
	if got := WithReviewedMarker("x", ""); got != "x" {
		t.Fatalf("empty oid changed body: %q", got)
	}
}

func TestParseReviewedMarker(t *testing.T) {
	cases := []struct {
		name, body, want string
		ok               bool
	}{
		{"none", "plain review", "", false},
		{"too short", "<!-- commitbrief:reviewed=abc -->", "", false},
		{"not hex", "<!-- commitbrief:reviewed=zzzzzzzz -->", "", false},
		{"last wins", "<!-- commitbrief:reviewed=aaaaaaa1 -->\n<!-- commitbrief:reviewed=bbbbbbb2 -->", "bbbbbbb2", true},
		{"sha256 length", "<!--commitbrief:reviewed=" + strings.Repeat("a", 64) + "-->", strings.Repeat("a", 64), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ParseReviewedMarker(c.body)
			if got != c.want || ok != c.ok {
				t.Fatalf("got %q, %v want %q, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

const reviewsJSON = `{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[
 {"body":"old <!-- commitbrief:reviewed=aaaaaaa1 -->","viewerDidAuthor":true},
 {"body":"newer <!-- commitbrief:reviewed=bbbbbbb2 -->","viewerDidAuthor":true},
 {"body":"someone else <!-- commitbrief:reviewed=ccccccc3 -->","viewerDidAuthor":false},
 {"body":"human review, no marker","viewerDidAuthor":true}
]}}}}}`

func TestFetchLastReviewedOID(t *testing.T) {
	t.Run("newest own marked review", func(t *testing.T) {
		fr := &fakeRunner{out: [][]byte{[]byte(reviewsJSON)}}
		oid, err := FetchLastReviewedOID(context.Background(), fr, "o/r", 7)
		if err != nil || oid != "bbbbbbb2" {
			t.Fatalf("oid=%q err=%v", oid, err)
		}
	})
	t.Run("no marker", func(t *testing.T) {
		fr := &fakeRunner{out: [][]byte{[]byte(`{"data":{"repository":{"pullRequest":{"reviews":{"nodes":[]}}}}}`)}}
		oid, err := FetchLastReviewedOID(context.Background(), fr, "o/r", 7)
		if err != nil || oid != "" {
			t.Fatalf("oid=%q err=%v", oid, err)
		}
	})
	t.Run("pull request missing", func(t *testing.T) {
		fr := &fakeRunner{out: [][]byte{[]byte(`{"data":{"repository":{"pullRequest":null}}}`)}}
		if _, err := FetchLastReviewedOID(context.Background(), fr, "o/r", 7); err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("graphql error", func(t *testing.T) {
		fr := &fakeRunner{out: [][]byte{[]byte(`{"errors":[{"message":"boom"}]}`)}}
		if _, err := FetchLastReviewedOID(context.Background(), fr, "o/r", 7); err == nil || !strings.Contains(err.Error(), "boom") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("runner error and bad slug", func(t *testing.T) {
		fr := &fakeRunner{errs: []error{errors.New("gh failed")}}
		if _, err := FetchLastReviewedOID(context.Background(), fr, "o/r", 7); err == nil {
			t.Fatal("want runner error")
		}
		if _, err := FetchLastReviewedOID(context.Background(), fr, "bad", 7); err == nil {
			t.Fatal("want slug error")
		}
	})
}

func TestFetchCompare(t *testing.T) {
	t.Run("pages are merged", func(t *testing.T) {
		page1 := `{"status":"ahead","total_commits":3,"commits":[{"sha":"a1","parents":[{"sha":"p"}]},{"sha":"a2","parents":[{"sha":"a1"}]}]}`
		page2 := `{"status":"ahead","total_commits":3,"commits":[{"sha":"a3","parents":[{"sha":"a2"}]}]}`
		fr := &fakeRunner{out: [][]byte{[]byte(page1 + "\n" + page2)}}
		c, err := FetchCompare(context.Background(), fr, "o/r", "base", "head")
		if err != nil {
			t.Fatal(err)
		}
		if !c.Ahead() || len(c.Commits) != 3 || !c.Complete() || c.HasMerge() {
			t.Fatalf("compare = %+v", c)
		}
		if got := fr.lastCall(); !argsContain(got, "repos/o/r/compare/base...head?per_page=100") || !argsContain(got, "--paginate") {
			t.Fatalf("call = %v", got)
		}
	})
	t.Run("merge commit", func(t *testing.T) {
		fr := &fakeRunner{out: [][]byte{[]byte(`{"status":"ahead","total_commits":1,"commits":[{"sha":"m","parents":[{"sha":"x"},{"sha":"y"}]}]}`)}}
		c, err := FetchCompare(context.Background(), fr, "o/r", "b", "h")
		if err != nil || !c.HasMerge() {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})
	t.Run("diverged and incomplete", func(t *testing.T) {
		fr := &fakeRunner{out: [][]byte{[]byte(`{"status":"diverged","total_commits":300,"commits":[]}`)}}
		c, err := FetchCompare(context.Background(), fr, "o/r", "b", "h")
		if err != nil || c.Ahead() || c.Complete() {
			t.Fatalf("c=%+v err=%v", c, err)
		}
	})
	t.Run("errors", func(t *testing.T) {
		if _, err := FetchCompare(context.Background(), &fakeRunner{errs: []error{errors.New("404")}}, "o/r", "b", "h"); err == nil {
			t.Fatal("want runner error")
		}
		if _, err := FetchCompare(context.Background(), &fakeRunner{out: [][]byte{[]byte("not json")}}, "o/r", "b", "h"); err == nil {
			t.Fatal("want decode error")
		}
		if _, err := FetchCompare(context.Background(), &fakeRunner{out: [][]byte{nil}}, "o/r", "b", "h"); err == nil {
			t.Fatal("want empty-output error")
		}
	})
}

func TestFetchCompareDiff(t *testing.T) {
	fr := &fakeRunner{out: [][]byte{[]byte("diff --git a/x b/x\n")}}
	d, err := FetchCompareDiff(context.Background(), fr, "o/r", "b", "h")
	if err != nil || d != "diff --git a/x b/x\n" {
		t.Fatalf("d=%q err=%v", d, err)
	}
	if got := fr.lastCall(); !argsContain(got, "Accept: application/vnd.github.diff") || !argsContain(got, "repos/o/r/compare/b...h") {
		t.Fatalf("call = %v", got)
	}
	if _, err := FetchCompareDiff(context.Background(), &fakeRunner{errs: []error{errors.New("too large")}}, "o/r", "b", "h"); err == nil {
		t.Fatal("want error")
	}
}

// runIncremental runs Sync as a --since-last-review run whose delta touches
// only the given paths.
func (s *ghSim) runIncremental(delta []string, groups ...Group) Result {
	snap, err := FetchThreads(context.Background(), s, "o/r", 7)
	if err != nil {
		s.t.Fatalf("FetchThreads: %v", err)
	}
	s.reset()
	in := map[string]bool{}
	for _, p := range delta {
		in[p] = true
	}
	return Sync(context.Background(), SyncParams{
		Runner: s, Slug: "o/r", PRNumber: 7, CommitID: "abcdef0123456789",
		Threads: snap, Groups: groups, Incremental: true,
		InDiff: func(p string) bool { return in[p] },
	})
}

// ADR-0046 §A.7 G1 / §A.7b: an incremental run closes a gone finding only
// when its thread is outdated and its file is in the delta.
func TestRemotePRSyncIncrementalClosing(t *testing.T) {
	t.Run("outdated and in delta: resolved", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		th.outdated = true
		res := s.runIncremental([]string{"a.go"})
		wantWrites(t, s, "RESOLVE", "PATCH")
		if !th.resolved || res.Resolved != 1 || res.OpenOurs != 0 {
			t.Fatalf("res=%+v resolved=%v", res, th.resolved)
		}
	})
	t.Run("file outside the delta: untouched and counted open", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		th.outdated = true
		res := s.runIncremental([]string{"other.go"})
		wantWrites(t, s)
		if th.resolved || res.OpenOurs != 1 {
			t.Fatalf("res=%+v resolved=%v", res, th.resolved)
		}
	})
	t.Run("in delta but not outdated: untouched", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		res := s.runIncremental([]string{"a.go"})
		wantWrites(t, s)
		if th.resolved || res.OpenOurs != 1 {
			t.Fatalf("res=%+v resolved=%v", res, th.resolved)
		}
	})
	t.Run("held thread: no resolve, no reply, still open", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"), "state=reopened")
		th.outdated = true
		res := s.runIncremental([]string{"a.go"})
		wantWrites(t, s)
		if th.resolved || len(th.comments) != 1 || res.OpenOurs != 1 || res.Held != 1 {
			t.Fatalf("res=%+v comments=%d", res, len(th.comments))
		}
	})
	t.Run("nil InDiff treats no file as in the delta", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		th.outdated = true
		snap, err := FetchThreads(context.Background(), s, "o/r", 7)
		if err != nil {
			t.Fatal(err)
		}
		s.reset()
		Sync(context.Background(), SyncParams{
			Runner: s, Slug: "o/r", PRNumber: 7, CommitID: "abcdef0123456789",
			Threads: snap, Incremental: true,
		})
		wantWrites(t, s)
		if th.resolved {
			t.Fatal("resolved without a delta")
		}
	})
	t.Run("non-incremental run keeps closing every gone thread", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		res := s.run()
		wantWrites(t, s, "RESOLVE", "PATCH")
		if !th.resolved || res.OpenOurs != 0 {
			t.Fatalf("res=%+v", res)
		}
	})
}

func TestRemotePRSyncOpenOursScopedRun(t *testing.T) {
	s := newSim(t)
	out := s.active(grp("b", 7, "y"))
	out.path = "b.go"
	res := s.runScoped(func(p string) bool { return p == "a.go" })
	if res.OpenOurs != 1 {
		t.Fatalf("OpenOurs = %d, want 1 (out-of-scope open thread)", res.OpenOurs)
	}
}
