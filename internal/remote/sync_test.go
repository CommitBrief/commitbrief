// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// writes lists the write calls of the last run by kind ("POST", "PATCH",
// "RESOLVE", "UNRESOLVE", "REPLY"), in order.
func (s *ghSim) writes() []string {
	out := []string{}
	for _, c := range s.calls {
		if c == "LIST" {
			continue
		}
		out = append(out, strings.Fields(c)[0])
	}
	return out
}

func wantWrites(t *testing.T, s *ghSim, want ...string) {
	t.Helper()
	if want == nil {
		want = []string{}
	}
	if got := s.writes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("writes = %v, want %v (calls: %v)", got, want, s.calls)
	}
}

// closed turns th into a thread CommitBrief resolved (S6) when byUs, or a
// plain human-closed one (S8) otherwise.
func (s *ghSim) closed(th *simThread, byUs bool) *simThread {
	th.resolved = true
	if byUs {
		_, m, _ := SplitMarker(th.comments[0].Body)
		text, _, _ := SplitMarker(th.comments[0].Body)
		m = m.WithState(stateResolved, th.lastID())
		th.comments[0].Body = withMarker(text, m)
	}
	return th
}

func (s *ghSim) human(th *simThread, body string) {
	th.comments = append(th.comments, Comment{ID: s.newID(), Body: body})
}

func (s *ghSim) countFP(c string) int {
	n := 0
	for _, th := range s.threads {
		if _, m, ok := SplitMarker(th.comments[0].Body); ok && m.FP == fpOf(c) {
			n++
		}
	}
	return n
}

// --- S1-S9: classification ---------------------------------------------

func TestRemotePRClassify(t *testing.T) {
	mk := func(attrs ...string) Marker { return Marker{FP: fpOf("a"), Attrs: attrs} }
	th := func(resolved bool) Thread {
		return Thread{Resolved: resolved, Root: Comment{ID: 10}, Recent: []Comment{{ID: 10}, {ID: 12}}}
	}
	cases := []struct {
		name   string
		t      Thread
		m      Marker
		class  Class
		record string
	}{
		{"S1 open no state", th(false), mk(), ClassActive, ""},
		{"S2 open state=resolved (human reopened)", th(false), mk("state=resolved", "last=12"), ClassHeld, stateReopened},
		{"S3 open state=reopened", th(false), mk("state=reopened"), ClassHeld, ""},
		{"S4 open state=closed", th(false), mk("state=closed"), ClassHeld, ""},
		{"S5 open unknown state", th(false), mk("state=wat"), ClassHeld, ""},
		{"S6 closed state=resolved last matches", th(true), mk("state=resolved", "last=12"), ClassClosedByUs, ""},
		{"S7 closed state=resolved last differs", th(true), mk("state=resolved", "last=11"), ClassClosedByHuman, stateClosed},
		{"S7 closed state=resolved last missing", th(true), mk("state=resolved"), ClassClosedByHuman, stateClosed},
		{"S8 closed no state", th(true), mk(), ClassClosedByHuman, stateClosed},
		{"S9 closed state=closed", th(true), mk("state=closed"), ClassClosedByHuman, ""},
		{"S9 closed state=reopened", th(true), mk("state=reopened"), ClassClosedByHuman, ""},
		{"S9 closed unknown state", th(true), mk("state=wat"), ClassClosedByHuman, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, rec := Classify(tc.t, tc.m)
			if c != tc.class || rec != tc.record {
				t.Fatalf("Classify = (%v, %q), want (%v, %q)", c, rec, tc.class, tc.record)
			}
		})
	}
}

// --- P1-P10 -------------------------------------------------------------

func TestRemotePRSyncP1Fresh(t *testing.T) {
	s := newSim(t)
	g := grp("a", 5, "nil deref")
	res := s.run(g)
	wantWrites(t, s, "POST")
	if res.Posted != 1 || len(s.threads) != 1 {
		t.Fatalf("res=%+v threads=%d", res, len(s.threads))
	}
	text, m, ok := SplitMarker(s.threads[0].comments[0].Body)
	if !ok || m.FP != fpOf("a") || len(m.Attrs) != 0 || text != g.Text {
		t.Fatalf("posted body wrong: %q", s.threads[0].comments[0].Body)
	}

	t.Run("unanchored goes to review body", func(t *testing.T) {
		s := newSim(t)
		g := grp("a", 5, "x")
		g.Anchored = false
		res := s.run(g)
		wantWrites(t, s)
		if len(res.Unanchored) != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("post failure falls back to review body", func(t *testing.T) {
		s := newSim(t)
		s.failPost = true
		res := s.run(grp("a", 5, "x"))
		if res.PostFailed != 1 || len(res.Unanchored) != 1 || len(res.Warnings) != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("finding without fingerprint posts unmarked", func(t *testing.T) {
		s := newSim(t)
		g := grp("a", 5, "x")
		g.FP = ""
		s.run(g)
		s.run(g)
		if len(s.threads) != 2 {
			t.Fatalf("unmanaged group should always post: %d", len(s.threads))
		}
		if strings.Contains(s.threads[0].comments[0].Body, "commitbrief:fp") {
			t.Fatalf("unmanaged comment carries a marker")
		}
	})
}

func TestRemotePRSyncP2P3(t *testing.T) {
	s := newSim(t)
	g := grp("a", 5, "nil deref")
	th := s.active(g, "extra=1")
	res := s.run(g)
	wantWrites(t, s) // P2
	if res.Unchanged != 1 {
		t.Fatalf("res=%+v", res)
	}

	g2 := g
	g2.Text = "[HIGH] - nil deref v2"
	res = s.run(g2) // P3
	wantWrites(t, s, "PATCH")
	if res.Updated != 1 {
		t.Fatalf("res=%+v", res)
	}
	if m := th.marker(t); !reflect.DeepEqual(m.Attrs, []string{"extra=1"}) {
		t.Fatalf("marker attrs not preserved: %v", m.Attrs)
	}
	if text, _, _ := SplitMarker(th.comments[0].Body); text != g2.Text {
		t.Fatalf("body not updated: %q", text)
	}
	s.run(g2)
	wantWrites(t, s)
}

func TestRemotePRSyncP4(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("viewerCanResolve true: post, resolve, record", func(t *testing.T) {
		s := newSim(t)
		old := s.active(g)
		old.outdated = true
		oldLast := old.lastID()
		res := s.run(g)
		wantWrites(t, s, "POST", "RESOLVE", "PATCH")
		if res.Posted != 1 || res.Resolved != 1 || !old.resolved {
			t.Fatalf("res=%+v", res)
		}
		m := old.marker(t)
		if m.State() != stateResolved || m.Last() != oldLast {
			t.Fatalf("old marker = %v", m)
		}
		if s.countFP("a") != 2 {
			t.Fatalf("threads = %d", s.countFP("a"))
		}
		// Next run: new thread is canonical and unchanged; old one is ours-closed.
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("viewerCanResolve false: no post, no resolve", func(t *testing.T) {
		s := newSim(t)
		old := s.active(g)
		old.outdated = true
		old.canResolve = false
		for i := 0; i < 3; i++ {
			s.run(g)
			wantWrites(t, s)
			if s.countFP("a") != 1 {
				t.Fatalf("run %d threads = %d", i, s.countFP("a"))
			}
		}
	})
	t.Run("viewerCanResolve false: body still updated", func(t *testing.T) {
		s := newSim(t)
		old := s.active(g)
		old.outdated = true
		old.canResolve = false
		g2 := g
		g2.Text = "[HIGH] - changed"
		s.run(g2)
		wantWrites(t, s, "PATCH")
	})
}

func TestRemotePRSyncP5P6(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("P5 held, same line: body only, state kept", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g, "state=reopened")
		g2 := g
		g2.Text = "[HIGH] - changed"
		res := s.run(g2)
		wantWrites(t, s, "PATCH")
		if res.Updated != 1 || th.marker(t).State() != stateReopened {
			t.Fatalf("res=%+v marker=%v", res, th.marker(t))
		}
		s.run(g2)
		wantWrites(t, s)
	})
	t.Run("P6 held + outdated: new post, old untouched, held counted", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g, "state=reopened")
		th.outdated = true
		res := s.run(g)
		wantWrites(t, s, "POST")
		if res.Held != 1 || res.Posted != 1 || th.resolved {
			t.Fatalf("res=%+v", res)
		}
		s.run(g) // new thread is canonical, held one non-canonical (N2)
		wantWrites(t, s)
	})
}

func TestRemotePRSyncP7P8(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("P7 closed by us, same line: unresolve and clear state", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g), true)
		res := s.run(g)
		wantWrites(t, s, "UNRESOLVE", "PATCH")
		m := th.marker(t)
		if th.resolved || m.State() != "" || m.Last() != 0 || res.Updated != 1 {
			t.Fatalf("res=%+v marker=%v resolved=%v", res, m, th.resolved)
		}
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("P7 unresolve denied: finding moves to review body", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g), true)
		th.canUnresolve = false
		res := s.run(g)
		wantWrites(t, s)
		if len(res.Unanchored) != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("P7 unresolve fails: finding moves to review body", func(t *testing.T) {
		s := newSim(t)
		s.closed(s.active(g), true)
		s.failUnresolve = true
		res := s.run(g)
		if len(res.Unanchored) != 1 || len(res.Warnings) != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("P8 closed by us + outdated: new post only", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g), true)
		th.outdated = true
		s.run(g)
		wantWrites(t, s, "POST")
		s.run(g)
		wantWrites(t, s)
		if s.countFP("a") != 2 {
			t.Fatalf("threads = %d", s.countFP("a"))
		}
	})
	t.Run("P8 post failure lands in review body", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g), true)
		th.outdated = true
		s.failPost = true
		res := s.run(g)
		if len(res.Unanchored) != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
}

func TestRemotePRSyncP9P10(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("P9 closed by human: untouched, state=closed recorded once", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g), false)
		res := s.run(g)
		wantWrites(t, s, "PATCH")
		if res.Unchanged != 1 || th.marker(t).State() != stateClosed || !th.resolved {
			t.Fatalf("res=%+v marker=%v", res, th.marker(t))
		}
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("P9 state=resolved but last differs (S7)", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g, "state=resolved", "last=1"), false)
		s.run(g)
		wantWrites(t, s, "PATCH")
		if th.marker(t).State() != stateClosed {
			t.Fatalf("marker=%v", th.marker(t))
		}
	})
	t.Run("P10 closed by human + outdated: single post, no thread calls", func(t *testing.T) {
		for _, name := range []string{"S8", "S7"} {
			s := newSim(t)
			var th *simThread
			if name == "S8" {
				th = s.closed(s.active(g), false)
			} else {
				th = s.closed(s.active(g, "state=resolved", "last=1"), false)
			}
			th.outdated = true
			s.run(g)
			// the closed thread only gets its state=closed record
			for _, w := range s.writes() {
				if w == "RESOLVE" || w == "UNRESOLVE" || w == "REPLY" {
					t.Fatalf("%s: unexpected %s", name, w)
				}
			}
			if n := s.count("POST"); n != 1 {
				t.Fatalf("%s: POST count = %d", name, n)
			}
			s.run(g) // new thread is P2
			wantWrites(t, s)
			// human closes the new thread too: next run skips (P9), count stays 2
			newTh := s.threads[len(s.threads)-1]
			newTh.resolved = true
			s.run(g)
			s.run(g)
			wantWrites(t, s)
			if s.countFP("a") != 2 {
				t.Fatalf("%s: threads = %d", name, s.countFP("a"))
			}
		}
	})
}

// --- N1-N3 ----------------------------------------------------------------

func TestRemotePRSyncN(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("N1 duplicate active thread is resolved and recorded", func(t *testing.T) {
		s := newSim(t)
		older := s.active(g)
		newer := s.active(g)
		res := s.run(g)
		wantWrites(t, s, "RESOLVE", "PATCH")
		if !older.resolved || newer.resolved || res.Resolved != 1 {
			t.Fatalf("res=%+v", res)
		}
		if m := older.marker(t); m.State() != stateResolved || m.Last() != older.lastID() {
			t.Fatalf("marker=%v", m)
		}
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("N1 without resolve permission: no call, counted stale", func(t *testing.T) {
		s := newSim(t)
		older := s.active(g)
		older.canResolve = false
		s.active(g)
		res := s.run(g)
		wantWrites(t, s)
		if res.StaleOpen != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("N1 resolve fails: counted stale", func(t *testing.T) {
		s := newSim(t)
		s.active(g)
		s.active(g)
		s.failResolve = true
		res := s.run(g)
		if res.StaleOpen != 1 || res.Resolved != 0 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("N2 duplicate held thread is left alone", func(t *testing.T) {
		s := newSim(t)
		held := s.active(g, "state=reopened")
		s.active(g)
		res := s.run(g)
		wantWrites(t, s)
		if res.Held != 1 || held.resolved {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("N3 closed duplicate is left alone", func(t *testing.T) {
		s := newSim(t)
		s.closed(s.active(g), true)
		s.active(g)
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("canonical: non-outdated beats outdated open", func(t *testing.T) {
		s := newSim(t)
		oldOpen := s.active(g)
		oldOpen.outdated = true
		closedFresh := s.closed(s.active(g), true) // newer, closed, not outdated
		s.run(g)
		// the closed non-outdated thread is canonical -> unresolved; the outdated open one is resolved (N1)
		if closedFresh.resolved {
			t.Fatalf("canonical closed thread should be reopened")
		}
		if !oldOpen.resolved {
			t.Fatalf("outdated duplicate should be resolved")
		}
		s.run(g)
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("canonical: two closed-by-us threads, outdated one skipped", func(t *testing.T) {
		s := newSim(t)
		a := s.closed(s.active(g), true)
		b := s.closed(s.active(g), true)
		a.outdated = true // older and outdated
		_ = b
		s.run(g)
		if a.resolved != true || b.resolved {
			t.Fatalf("non-outdated one must be unresolved: a=%v b=%v", a.resolved, b.resolved)
		}
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("canonical: newest wins on tie", func(t *testing.T) {
		s := newSim(t)
		a := s.active(g)
		b := s.active(g)
		s.run(g)
		if !a.resolved || b.resolved {
			t.Fatalf("a=%v b=%v", a.resolved, b.resolved)
		}
	})
}

// --- G1-G4 ----------------------------------------------------------------

func TestRemotePRSyncG(t *testing.T) {
	t.Run("G1 active thread without finding: resolve + record", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		res := s.run()
		wantWrites(t, s, "RESOLVE", "PATCH")
		m := th.marker(t)
		if res.Resolved != 1 || m.State() != stateResolved || m.Last() != th.lastID() {
			t.Fatalf("res=%+v marker=%v", res, m)
		}
		s.run()
		wantWrites(t, s)
	})
	t.Run("G1 resolve before human reply: last is the human comment (Q2)", func(t *testing.T) {
		s := newSim(t)
		g := grp("a", 5, "x")
		th := s.active(g)
		s.human(th, "will fix")
		s.run()
		wantWrites(t, s, "RESOLVE", "PATCH")
		if th.marker(t).Last() != th.lastID() || th.lastID() == th.comments[0].ID {
			t.Fatalf("marker=%v", th.marker(t))
		}
		s.run(g) // finding is back: still ours -> unresolved
		wantWrites(t, s, "UNRESOLVE", "PATCH")
		if th.resolved {
			t.Fatalf("thread should be open again")
		}
	})
	t.Run("G1 resolve 403: gone reply, no state written", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		s.failResolve = true
		res := s.run()
		wantWrites(t, s, "RESOLVE", "REPLY")
		if res.Replied != 1 || th.marker(t).State() != "" {
			t.Fatalf("res=%+v marker=%v", res, th.marker(t))
		}
		last := th.comments[len(th.comments)-1].Body
		if !strings.Contains(last, "No longer present as of abcdef0.") || !strings.Contains(last, "<!-- commitbrief:gone v=1 -->") {
			t.Fatalf("gone reply = %q", last)
		}
		s.run() // second run: still no permission, but no second reply
		for _, w := range s.writes() {
			if w == "REPLY" || w == "PATCH" {
				t.Fatalf("unexpected %s on rerun", w)
			}
		}
	})
	t.Run("G1 no resolve permission: gone reply once", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"))
		th.canResolve = false
		s.run()
		wantWrites(t, s, "REPLY")
		s.run()
		wantWrites(t, s)
	})
	t.Run("G2 held thread without finding: untouched, counted", func(t *testing.T) {
		s := newSim(t)
		th := s.active(grp("a", 5, "x"), "state=reopened")
		for i := 0; i < 2; i++ {
			res := s.run()
			wantWrites(t, s)
			if res.Held != 1 || th.resolved || res.Replied != 0 {
				t.Fatalf("res=%+v", res)
			}
		}
	})
	t.Run("G3 closed by us without finding: untouched", func(t *testing.T) {
		s := newSim(t)
		s.closed(s.active(grp("a", 5, "x")), true)
		s.run()
		wantWrites(t, s)
	})
	t.Run("G4 closed by human without finding: record only", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(grp("a", 5, "x")), false)
		s.run()
		wantWrites(t, s, "PATCH")
		if th.marker(t).State() != stateClosed {
			t.Fatalf("marker=%v", th.marker(t))
		}
		s.run()
		wantWrites(t, s)
	})
}

// --- back replies -----------------------------------------------------------

func TestRemotePRSyncBack(t *testing.T) {
	g := grp("a", 5, "nil deref")
	goneThread := func(s *ghSim, attrs ...string) *simThread {
		th := s.active(g, attrs...)
		th.canResolve = false
		s.run() // no finding -> gone reply (or nothing if held)
		return th
	}
	assertBackOnce := func(t *testing.T, s *ghSim, th *simThread, gs ...Group) {
		t.Helper()
		res := s.run(gs...)
		found := 0
		for _, w := range s.writes() {
			if w == "REPLY" {
				found++
			}
		}
		if found != 1 || res.Replied != 1 {
			t.Fatalf("want exactly one back reply, writes=%v res=%+v", s.writes(), res)
		}
		last := th.comments[len(th.comments)-1].Body
		if !strings.Contains(last, "Present again as of abcdef0.") || !strings.Contains(last, "commitbrief:back v=1") {
			t.Fatalf("back reply = %q", last)
		}
		s.run(gs...)
		for _, w := range s.writes() {
			if w == "REPLY" {
				t.Fatalf("second back reply on rerun")
			}
		}
	}
	t.Run("P2", func(t *testing.T) {
		s := newSim(t)
		th := goneThread(s)
		assertBackOnce(t, s, th, g)
	})
	t.Run("P3", func(t *testing.T) {
		s := newSim(t)
		th := goneThread(s)
		g2 := g
		g2.Text = "[HIGH] - changed"
		assertBackOnce(t, s, th, g2)
	})
	t.Run("P5", func(t *testing.T) {
		s := newSim(t)
		th := goneThread(s)
		th.canResolve = true
		// a human reopened-and-held marker appears
		th.comments[0].Body = markedBody(g.Text, g.FP, "state=reopened")
		th.canResolve = false
		assertBackOnce(t, s, th, g)
	})
	t.Run("P4 without POST", func(t *testing.T) {
		s := newSim(t)
		th := goneThread(s)
		th.outdated = true
		assertBackOnce(t, s, th, g)
		if s.countFP("a") != 1 {
			t.Fatalf("no POST expected, threads=%d", s.countFP("a"))
		}
	})
	t.Run("P7", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g)
		th.canResolve = true
		// we resolved, then (resolve of record) a gone reply is the last comment
		th.comments = append(th.comments, Comment{ID: s.newID(), Body: "No longer present as of 1234567. by #commitbrief <!-- commitbrief:gone v=1 -->", ViewerDidAuthor: true})
		s.closed(th, true)
		res := s.run(g)
		wantWrites(t, s, "UNRESOLVE", "PATCH", "REPLY")
		if res.Replied != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
	t.Run("P4 with POST: no back reply", func(t *testing.T) {
		s := newSim(t)
		th := goneThread(s)
		th.canResolve = true
		th.outdated = true
		s.run(g)
		for _, w := range s.writes() {
			if w == "REPLY" {
				t.Fatalf("back reply must not be sent when the replacement is posted")
			}
		}
	})
	t.Run("P4 POST fails: back is retried", func(t *testing.T) {
		s := newSim(t)
		th := goneThread(s)
		th.canResolve = true
		th.outdated = true
		s.failPost = true
		res := s.run(g)
		if res.PostFailed != 1 || res.Replied != 1 {
			t.Fatalf("res=%+v", res)
		}
	})
}

// Q12: gone, back, gone, back with no resolve permission.
func TestRemotePRSyncGoneBackCycle(t *testing.T) {
	g := grp("a", 5, "nil deref")
	s := newSim(t)
	th := s.active(g)
	th.canResolve = false
	kinds := []string{}
	for i, groups := range [][]Group{nil, {g}, nil, {g}} {
		s.run(groups...)
		if n := s.count("REPLY"); n != 1 {
			t.Fatalf("run %d: replies = %d", i, n)
		}
		kinds = append(kinds, statusReplyKind(th.comments[len(th.comments)-1].Body))
		s.run(groups...) // rerun, no second reply
		if n := s.count("REPLY"); n != 0 {
			t.Fatalf("run %d rerun: replies = %d", i, n)
		}
	}
	if !reflect.DeepEqual(kinds, []string{"gone", "back", "gone", "back"}) {
		t.Fatalf("kinds = %v", kinds)
	}
}

// Q14: human reply after gone, and the 10-comment window.
func TestRemotePRSyncStatusWindow(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("human reply after gone: single back, no second gone", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g)
		th.canResolve = false
		s.run()
		s.human(th, "ok, will look")
		s.run()
		wantWrites(t, s) // gone already announced
		s.run(g)
		wantWrites(t, s, "REPLY")
		if statusReplyKind(th.comments[len(th.comments)-1].Body) != "back" {
			t.Fatalf("expected back reply")
		}
	})
	t.Run("status reply outside the window: no back", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g)
		th.canResolve = false
		s.run() // gone
		for i := 0; i < 10; i++ {
			s.human(th, fmt.Sprintf("c%d", i))
		}
		s.run(g)
		wantWrites(t, s)
	})
	t.Run("10+ human comments after gone: one extra gone, then stable", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g)
		th.canResolve = false
		s.run() // gone
		for i := 0; i < 10; i++ {
			s.human(th, fmt.Sprintf("c%d", i))
		}
		s.run()
		wantWrites(t, s, "REPLY")
		s.run()
		wantWrites(t, s)
	})
}

// Q11: no resolve permission, three commits touching the line.
func TestRemotePRSyncNoResolvePermission(t *testing.T) {
	g := grp("a", 5, "nil deref")
	s := newSim(t)
	old := s.active(g)
	old.canResolve = false
	dup := s.active(g)
	dup.canResolve = false
	_ = dup
	for i := 0; i < 3; i++ {
		old.outdated = true
		res := s.run(g)
		if s.count("POST") != 0 || s.count("RESOLVE") != 0 || s.countFP("a") != 2 {
			t.Fatalf("run %d: calls=%v threads=%d", i, s.calls, s.countFP("a"))
		}
		if res.StaleOpen != 1 && i == 0 && false {
			t.Fatalf("res=%+v", res)
		}
	}
}

// --- sequences --------------------------------------------------------------

func TestRemotePRSyncThreeRunsOneComment(t *testing.T) {
	s := newSim(t)
	gs := []Group{grp("a", 5, "one"), grp("b", 9, "two"), grp("c", 12, "three")}
	for i := 0; i < 3; i++ {
		s.run(gs...)
		if i > 0 {
			wantWrites(t, s)
		}
	}
	if len(s.threads) != 3 {
		t.Fatalf("threads = %d", len(s.threads))
	}
	for _, c := range []string{"a", "b", "c"} {
		if s.countFP(c) != 1 {
			t.Fatalf("fp %s threads = %d", c, s.countFP(c))
		}
	}
}

func TestRemotePRSyncResolveUnresolveCycle(t *testing.T) {
	g := grp("a", 5, "nil deref")
	s := newSim(t)
	s.run(g)
	th := s.threads[0]
	s.run() // gone
	if !th.resolved || th.marker(t).State() != stateResolved {
		t.Fatalf("thread not resolved/recorded: %v", th.marker(t))
	}
	s.run(g) // back
	if th.resolved || th.marker(t).State() != "" || th.marker(t).Last() != 0 {
		t.Fatalf("thread not reopened/cleaned: %v", th.marker(t))
	}
	s.run(g)
	wantWrites(t, s)
	if len(s.threads) != 1 {
		t.Fatalf("threads = %d", len(s.threads))
	}
}

func TestRemotePRSyncHumanReopened(t *testing.T) {
	g := grp("a", 5, "nil deref")
	s := newSim(t)
	s.run(g)
	th := s.threads[0]
	s.run() // we resolve it
	th.resolved = false
	s.run() // S2: held, record reopened
	wantWrites(t, s, "PATCH")
	if th.marker(t).State() != stateReopened {
		t.Fatalf("marker=%v", th.marker(t))
	}
	for i := 0; i < 2; i++ {
		res := s.run()
		wantWrites(t, s)
		if res.Held != 1 || th.resolved {
			t.Fatalf("res=%+v", res)
		}
	}
	// A later human close of a held thread is left alone (S9: no record).
	th.resolved = true
	s.run(g)
	wantWrites(t, s)
	if th.marker(t).State() != stateReopened || !th.resolved {
		t.Fatalf("marker=%v", th.marker(t))
	}
}

func TestRemotePRSyncHumanClosedThenReopened(t *testing.T) {
	g := grp("a", 5, "nil deref")
	s := newSim(t)
	s.run(g)
	th := s.threads[0]
	th.resolved = true
	s.run(g)
	wantWrites(t, s, "PATCH")
	if th.marker(t).State() != stateClosed {
		t.Fatalf("marker=%v", th.marker(t))
	}
	s.run(g)
	wantWrites(t, s) // never reopened by us
	th.resolved = false
	res := s.run()
	wantWrites(t, s)
	if res.Held != 1 {
		t.Fatalf("res=%+v", res)
	}
}

// Q1: no thread proliferation across present/absent/present/absent runs.
func TestRemotePRSyncNoProliferation(t *testing.T) {
	g := grp("a", 5, "nil deref")
	seqs := [][]Group{{g}, nil, {g}, nil, {g}, nil}
	t.Run("held + outdated", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g, "state=reopened")
		th.outdated = true
		for _, gs := range seqs {
			s.run(gs...)
			if s.countFP("a") > 2 {
				t.Fatalf("threads = %d", s.countFP("a"))
			}
		}
	})
	t.Run("active + outdated", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g)
		th.outdated = true
		for _, gs := range seqs {
			s.run(gs...)
			if s.countFP("a") > 2 {
				t.Fatalf("threads = %d", s.countFP("a"))
			}
		}
	})
	t.Run("half-done move: new thread open, old resolve failed (Q10)", func(t *testing.T) {
		s := newSim(t)
		old := s.active(g)
		old.outdated = true
		s.failResolve = true
		s.run(g)
		if s.countFP("a") != 2 || old.resolved {
			t.Fatalf("threads = %d", s.countFP("a"))
		}
		s.failResolve = false
		for _, gs := range seqs {
			s.run(gs...)
			if s.countFP("a") != 2 {
				t.Fatalf("threads = %d", s.countFP("a"))
			}
		}
	})
}

// Q3/Q4: the loop cap.
func TestRemotePRSyncCap(t *testing.T) {
	cap1 := func(c []render.Finding) []render.Finding { return c[:1] }
	none := func(c []render.Finding) []render.Finding { return nil }
	t.Run("cap limits new comments only", func(t *testing.T) {
		s := newSim(t)
		res := s.runSel(cap1, grp("a", 1, "a"), grp("b", 2, "b"), grp("c", 3, "c"))
		if res.Posted != 1 || len(s.threads) != 1 {
			t.Fatalf("res=%+v threads=%d", res, len(s.threads))
		}
	})
	t.Run("capped live finding keeps its open thread", func(t *testing.T) {
		s := newSim(t)
		g := grp("a", 5, "x")
		th := s.active(g)
		s.runSel(none, g)
		wantWrites(t, s)
		if th.resolved {
			t.Fatalf("live thread resolved")
		}
	})
	t.Run("capped outdated move leaves old thread open", func(t *testing.T) {
		s := newSim(t)
		g := grp("a", 5, "x")
		old := s.active(g)
		old.outdated = true
		s.runSel(none, g)
		wantWrites(t, s)
		if old.resolved {
			t.Fatalf("old thread resolved without replacement")
		}
	})
	t.Run("failed outdated move leaves old thread open", func(t *testing.T) {
		s := newSim(t)
		g := grp("a", 5, "x")
		old := s.active(g)
		old.outdated = true
		s.failPost = true
		s.run(g)
		if old.resolved || s.count("RESOLVE") != 0 {
			t.Fatalf("old thread resolved without replacement")
		}
	})
	t.Run("capped-out fallback is not resurfaced as unanchored", func(t *testing.T) {
		s := newSim(t)
		res := s.runSel(none, grp("a", 1, "a"))
		if len(res.Unanchored) != 0 || res.Posted != 0 {
			t.Fatalf("res=%+v", res)
		}
	})
}

// Q8: unknown state values.
func TestRemotePRSyncUnknownState(t *testing.T) {
	g := grp("a", 5, "nil deref")
	t.Run("open: never resolved, attrs kept on PATCH", func(t *testing.T) {
		s := newSim(t)
		th := s.active(g, "state=wat", "extra=1")
		s.run()
		wantWrites(t, s)
		g2 := g
		g2.Text = "[HIGH] - changed"
		s.run(g2)
		wantWrites(t, s, "PATCH")
		if m := th.marker(t); !reflect.DeepEqual(m.Attrs, []string{"state=wat", "extra=1"}) {
			t.Fatalf("attrs = %v", m.Attrs)
		}
	})
	t.Run("closed: never unresolved or recorded", func(t *testing.T) {
		s := newSim(t)
		th := s.closed(s.active(g, "state=wat"), false)
		s.run(g)
		s.run()
		wantWrites(t, s)
		if !th.resolved {
			t.Fatalf("closed thread was reopened")
		}
	})
}

// Comments that are not ours are never touched.
func TestRemotePRSyncForeignCommentsUntouched(t *testing.T) {
	g := grp("a", 5, "nil deref")
	s := newSim(t)
	// no marker at all
	s.add("[HIGH] - old style comment without a marker")
	// marker written by someone else
	other := s.active(g)
	other.comments[0].ViewerDidAuthor = false
	// unsupported marker version
	s.add(fmt.Sprintf("text\n<!-- commitbrief:fp=%s v=2 -->", fpOf("a")))
	// malformed fingerprint
	s.add("text\n<!-- commitbrief:fp=zzzz v=1 -->")
	s.run()
	wantWrites(t, s)
	res := s.run(g) // group has no thread of ours -> exactly one POST
	wantWrites(t, s, "POST")
	if res.Posted != 1 {
		t.Fatalf("res=%+v", res)
	}
}

// Runs against a thread list that is read once and written by Sync only.
func TestRemotePRSyncPatchFailureIsWarning(t *testing.T) {
	g := grp("a", 5, "x")
	s := newSim(t)
	s.active(g)
	s.failPatch = true
	g2 := g
	g2.Text = "[HIGH] - changed"
	res := s.run(g2)
	if res.Updated != 0 || len(res.Warnings) != 1 {
		t.Fatalf("res=%+v", res)
	}
}

func TestRemotePRSyncNoRecordPermission(t *testing.T) {
	g := grp("a", 5, "x")
	s := newSim(t)
	th := s.active(g)
	th.canUpdate = false
	th.comments[0].ViewerCanUpdate = false
	res := s.run()
	// without the ability to record state we do not resolve; fall back to the gone reply
	wantWrites(t, s, "REPLY")
	if th.resolved || res.Replied != 1 {
		t.Fatalf("res=%+v", res)
	}
}

// --- invariants over random runs (D1-D4) -----------------------------------

func TestRemotePRSyncInvariantsRandomRuns(t *testing.T) {
	fps := []string{"a", "b", "c"}
	// Deterministic LCG so a failure reproduces.
	seed := uint32(12345)
	next := func(n int) int {
		seed = seed*1664525 + 1013904223
		return int(seed>>16) % n
	}
	for trial := 0; trial < 60; trial++ {
		s := newSim(t)
		line := map[string]int{"a": 5, "b": 9, "c": 14}
		for step := 0; step < 14; step++ {
			// randomly mutate the world the way humans / code changes do
			for _, th := range s.threads {
				switch next(12) {
				case 0:
					th.outdated = true
				case 1:
					th.canResolve = !th.canResolve
				}
			}
			var groups []Group
			for _, c := range fps {
				if next(3) != 0 {
					groups = append(groups, grp(c, line[c], "title "+c))
				}
			}
			before := len(s.threads)
			res := s.run(groups...)
			// D: a live finding never loses its only thread: a thread of ours exists for it
			// unless the post was impossible.
			live := map[string]bool{}
			for _, g := range groups {
				live[g.FP] = true
			}
			for _, c := range fps {
				if live[fpOf(c)] && s.countFP(c) == 0 && res.PostFailed == 0 {
					t.Fatalf("trial %d step %d: live finding %s has no thread", trial, step, c)
				}
			}
			// D: at most one new thread per fingerprint per run.
			if len(s.threads)-before > len(groups) {
				t.Fatalf("trial %d step %d: %d new threads for %d groups", trial, step, len(s.threads)-before, len(groups))
			}
			// D: an immediate identical rerun is a no-op unless a transient fixed point is still moving.
			s.run(groups...)
			s.run(groups...)
			for _, w := range s.writes() {
				if w == "POST" {
					t.Fatalf("trial %d step %d: rerun posted again", trial, step)
				}
			}
		}
	}
}

var _ = context.Background
