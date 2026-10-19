// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// Thread states recorded in the identity marker (`state=`). Only these
// strings are ever written by CommitBrief; any other value found in a
// marker is preserved untouched and treated as "hold" (fail-safe).
const (
	stateResolved = "resolved"
	stateReopened = "reopened"
	stateClosed   = "closed"
)

// Class is how a thread CommitBrief owns is currently classified (S1-S9).
type Class int

const (
	// ClassActive is an open thread CommitBrief manages (S1).
	ClassActive Class = iota
	// ClassHeld is an open thread a human reopened or marked to keep (S2-S5).
	ClassHeld
	// ClassClosedByUs is a thread CommitBrief resolved and nobody touched since (S6).
	ClassClosedByUs
	// ClassClosedByHuman is a closed thread a human decided on (S7-S9).
	ClassClosedByHuman
)

// Classify applies the S1-S9 table to one thread. record is the state the
// run should write into the root marker ("reopened" for S2, "closed" for
// S7/S8) or "" when nothing is to be recorded.
func Classify(t Thread, m Marker) (c Class, record string) {
	state := m.State()
	if !t.Resolved {
		switch state {
		case "":
			return ClassActive, ""
		case stateResolved:
			return ClassHeld, stateReopened
		default:
			return ClassHeld, ""
		}
	}
	switch state {
	case stateResolved:
		if last := m.Last(); last != 0 && last == t.LastID() {
			return ClassClosedByUs, ""
		}
		return ClassClosedByHuman, stateClosed
	case "":
		return ClassClosedByHuman, stateClosed
	default:
		return ClassClosedByHuman, ""
	}
}

// Group is every finding that shares one fingerprint, folded into a single
// comment. Rep is the finding the comment is anchored on (lowest anchorable
// line); Text is the rendered comment without the identity marker. A group
// with an empty FP is unmanaged: it never matches a thread and is always
// posted fresh, like before comment sync existed.
type Group struct {
	FP       string
	Rep      render.Finding
	Members  []render.Finding
	Severity render.Severity
	Side     string
	Anchored bool
	Text     string
}

func (g Group) ref() string { return g.Rep.PathRef() }

func (g Group) body() string {
	if g.FP == "" {
		return g.Text
	}
	return withMarker(g.Text, NewMarker(g.FP))
}

// Warning is a non-fatal problem met while syncing. Err is nil when the
// operation was skipped for lack of permission.
type Warning struct {
	Op  string
	Ref string
	Err error
}

// Result is the outcome of one Sync run.
type Result struct {
	Posted     int
	PostFailed int
	Updated    int
	Unchanged  int
	Resolved   int
	Replied    int
	Held       int
	// StaleOpen counts duplicate (non-canonical) threads that could not be
	// resolved and were left open.
	StaleOpen int
	// StaleReason says why the duplicates stayed open: "permission" when the
	// viewer may not resolve (or record), otherwise the first API error.
	StaleReason string
	// Unanchored are findings that could not be given an inline comment and
	// must be listed in the review body instead.
	Unanchored []render.Finding
	Warnings   []Warning
	// OpenOurs counts CommitBrief threads that were already on the PR and are
	// still open once the sync is done: untouched, held and out-of-scope
	// ones, and threads left open by a failed resolve. Comments this run
	// posted are not counted: the verdict already weighs the findings they
	// carry. An incremental verdict that approves must not ignore OpenOurs
	// (ADR-0046 §A.7b).
	OpenOurs int
}

// SyncParams is the input of Sync. Select applies the loop cap (ADR-0016
// §6) to the findings that need a brand-new inline comment.
type SyncParams struct {
	Runner   Runner
	Slug     string
	PRNumber int
	CommitID string
	Threads  []Thread
	Groups   []Group
	Select   func([]render.Finding) []render.Finding
	// InScope reports whether a thread's file was part of this run's review.
	// A nil InScope means the whole PR was reviewed. Threads outside the
	// scope are never touched: a narrowed run (--file/--dir/--exclude-*) says
	// nothing about files it did not look at (ADR-0046 §Update 2026-10-18).
	InScope func(path string) bool
	// Incremental marks a --since-last-review run: only the delta since the
	// last review was re-reviewed, so a finding missing from Groups proves
	// nothing about code outside that delta. In that mode a thread whose
	// finding is gone is resolved only when it is active, outdated and its
	// file is in InDiff (ADR-0046 §A.7b); every other thread is left alone.
	Incremental bool
	// InDiff reports whether a file is part of the incremental diff. It is
	// consulted only when Incremental is set; nil then means no file is.
	InDiff func(path string) bool
}

type threadOp int

const (
	opNone threadOp = iota
	opResolve
	opUnresolve
)

type tinfo struct {
	t     Thread
	m     Marker
	text  string
	class Class
	rec   string
	group *Group

	newText  *string
	op       threadOp
	goneFall bool // resolve impossible -> reply "gone" (G1) instead of counting stale (N1)
	back     bool // eligible for a "back" reply when it ends the run open
	postIdx  int  // P4: index into posts, -1 when none
	open     bool // still open on GitHub after the operations so far
}

func (ti *tinfo) ref() string {
	if ti.group != nil {
		return ti.group.ref()
	}
	return "comment " + strconv.FormatInt(ti.t.Root.ID, 10)
}

type postItem struct {
	g        *Group
	old      *tinfo // P4: outdated active thread to resolve once the new comment exists
	fallback bool   // failed/unanchorable POST falls back to the review body
}

type syncer struct {
	ctx context.Context
	p   SyncParams
	res Result

	infos []*tinfo
	posts []postItem
	// postOK[i] is true once posts[i] was created.
	postOK map[int]bool
	// outOfScopeOpen counts our open threads the run did not review.
	outOfScopeOpen int
}

// Sync reconciles the PR's CommitBrief review threads with the current
// findings (ADR-0046). The decision logic is pure over the thread snapshot;
// all GitHub writes go through the small helpers in threads.go/comment.go.
func Sync(ctx context.Context, p SyncParams) Result {
	s := &syncer{ctx: ctx, p: p, postOK: map[int]bool{}}
	s.plan()
	s.execute()
	s.res.OpenOurs = s.outOfScopeOpen
	for _, ti := range s.infos {
		if ti.open {
			s.res.OpenOurs++
		}
	}
	return s.res
}

func (s *syncer) warn(op, ref string, err error) {
	s.res.Warnings = append(s.res.Warnings, Warning{Op: op, Ref: ref, Err: err})
}

// canonicalBefore orders two threads of the same fingerprint: not outdated,
// then open, then the newest root comment.
func canonicalBefore(a, b *tinfo) bool {
	if a.t.Outdated != b.t.Outdated {
		return !a.t.Outdated
	}
	aOpen, bOpen := !a.t.Resolved, !b.t.Resolved
	if aOpen != bOpen {
		return aOpen
	}
	return a.t.Root.ID > b.t.Root.ID
}

func (s *syncer) plan() {
	groups := map[string]*Group{}
	for i := range s.p.Groups {
		if g := &s.p.Groups[i]; g.FP != "" {
			groups[g.FP] = g
		}
	}
	canon := map[string]*tinfo{}
	for _, t := range s.p.Threads {
		text, m, ok := SplitMarker(t.Root.Body)
		if !ok || !t.Root.ViewerDidAuthor {
			continue // not ours: never touched
		}
		if s.p.InScope != nil && !s.p.InScope(t.Path) {
			if !t.Resolved {
				s.outOfScopeOpen++
			}
			continue // not reviewed this run: never touched
		}
		ti := &tinfo{t: t, m: m, text: text, postIdx: -1, group: groups[m.FP], open: !t.Resolved}
		ti.class, ti.rec = Classify(t, m)
		if ti.rec != "" && !t.Root.ViewerCanUpdate {
			s.warn("record", ti.ref(), nil)
			ti.rec = ""
		}
		s.infos = append(s.infos, ti)
		if c := canon[m.FP]; c == nil || canonicalBefore(ti, c) {
			canon[m.FP] = ti
		}
	}

	hasThread := map[string]bool{}
	for _, ti := range s.infos {
		hasThread[ti.m.FP] = true
		g := ti.group
		switch {
		case g == nil:
			s.planNoFinding(ti)
		case canon[ti.m.FP] != ti:
			s.planNonCanonical(ti)
		default:
			s.planCanonical(ti, g)
		}
	}
	// Groups with no thread of ours: P1 (fresh comment).
	for i := range s.p.Groups {
		g := &s.p.Groups[i]
		if g.FP == "" || !hasThread[g.FP] {
			s.posts = append(s.posts, postItem{g: g, fallback: true})
		}
	}
}

// canResolve: resolving needs the thread permission and the ability to
// record `state=resolved` afterwards; without the record a later run would
// mistake our own resolve for a human decision.
func canResolve(t Thread) bool { return t.CanResolve && t.Root.ViewerCanUpdate }

func (s *syncer) planNoFinding(ti *tinfo) {
	switch ti.class {
	case ClassActive:
		if s.p.Incremental && !s.goneInDelta(ti.t) {
			return // not re-reviewed: stays as it is
		}
		ti.op, ti.goneFall = opResolve, true // G1
	case ClassHeld:
		s.res.Held++ // G2
	}
}

// goneInDelta reports whether an incremental run may treat a missing finding
// as fixed: the thread's code changed (outdated) and its file is in the delta.
func (s *syncer) goneInDelta(t Thread) bool {
	return t.Outdated && s.p.InDiff != nil && s.p.InDiff(t.Path)
}

func (s *syncer) planNonCanonical(ti *tinfo) {
	switch ti.class {
	case ClassActive:
		ti.op = opResolve // N1
	case ClassHeld:
		s.res.Held++ // N2
	}
}

func (s *syncer) bodyChange(ti *tinfo, g *Group) {
	if ti.text == g.Text {
		s.res.Unchanged++
		return
	}
	if !ti.t.Root.ViewerCanUpdate {
		s.warn("update", g.ref(), nil)
		s.res.Unchanged++
		return
	}
	txt := g.Text
	ti.newText = &txt
}

func (s *syncer) addPost(ti *tinfo, g *Group, old *tinfo, fallback bool) {
	s.posts = append(s.posts, postItem{g: g, old: old, fallback: fallback})
	if ti != nil {
		ti.postIdx = len(s.posts) - 1
	}
}

func (s *syncer) planCanonical(ti *tinfo, g *Group) {
	out := ti.t.Outdated
	switch ti.class {
	case ClassActive:
		if !out { // P2 / P3
			s.bodyChange(ti, g)
			ti.back = true
			return
		}
		if canResolve(ti.t) { // P4
			s.addPost(ti, g, ti, false)
			ti.back = true
			return
		}
		s.bodyChange(ti, g)
		ti.back = true
	case ClassHeld:
		if !out { // P5
			s.bodyChange(ti, g)
			ti.back = true
			return
		}
		s.res.Held++ // P6
		s.addPost(nil, g, nil, false)
	case ClassClosedByUs:
		if !out { // P7
			ti.op = opUnresolve
			if ti.text != g.Text {
				txt := g.Text
				ti.newText = &txt
			}
			ti.back = true
			return
		}
		s.addPost(nil, g, nil, true) // P8
	case ClassClosedByHuman:
		if !out { // P9
			s.res.Unchanged++
			return
		}
		s.addPost(nil, g, nil, true) // P10
	}
}

func (s *syncer) execute() {
	// Thread-only operations first, in snapshot order.
	for _, ti := range s.infos {
		s.executeThread(ti)
	}
	s.executePosts()
	// Back replies for outdated active threads whose replacement did not appear.
	for _, ti := range s.infos {
		if ti.postIdx >= 0 && ti.back && !s.postOK[ti.postIdx] {
			s.tryBack(ti)
		}
	}
}

// mark describes a change to the root marker's state/last attributes.
type mark struct {
	state string
	last  int64
}

// flush writes the thread's root comment once: new body text (if any) plus
// marker changes. The marker's unknown attributes are always preserved.
func (s *syncer) flush(ti *tinfo, mk *mark) error {
	m := ti.m
	switch {
	case mk != nil:
		m = m.WithState(mk.state, mk.last)
	case ti.rec != "":
		m = m.WithState(ti.rec, 0)
	}
	changed := mk != nil || ti.rec != ""
	if ti.newText == nil && !changed {
		return nil
	}
	var body string
	if ti.newText != nil {
		body = withMarker(*ti.newText, m)
	} else {
		body = rewriteMarker(ti.t.Root.Body, m)
	}
	return UpdateComment(s.ctx, s.p.Runner, s.p.Slug, ti.t.Root.ID, body)
}

func (s *syncer) flushPlain(ti *tinfo) {
	if err := s.flush(ti, nil); err != nil {
		s.warn("update", ti.ref(), err)
		return
	}
	if ti.newText != nil {
		s.res.Updated++
	}
}

// errNoPermission marks a resolve that was skipped because the viewer may not
// resolve the thread or record the new state afterwards.
var errNoPermission = errors.New("no permission")

// resolveAndRecord resolves the thread and records `state=resolved`. A nil
// error means the thread is now resolved; errNoPermission means nothing was
// attempted; any other error is the failed resolve call. It never warns, so
// each caller decides how a failure is reported.
func (s *syncer) resolveAndRecord(ti *tinfo) error {
	if !canResolve(ti.t) {
		return errNoPermission
	}
	if err := ResolveThread(s.ctx, s.p.Runner, ti.t.NodeID); err != nil {
		return err
	}
	ti.open = false
	s.res.Resolved++
	// Resolve first, record second: a failed resolve leaves the marker alone.
	saved := ti.newText
	ti.newText = nil
	if err := s.flush(ti, &mark{state: stateResolved, last: ti.t.LastID()}); err != nil {
		s.warn("record", ti.ref(), err)
	}
	ti.newText = saved
	return nil
}

// warnResolve reports a failed resolve call; a missing permission is silent
// here because the callers surface it their own way (gone reply, summary).
func (s *syncer) warnResolve(ti *tinfo, err error) {
	if err != nil && !errors.Is(err, errNoPermission) {
		s.warn("resolve", ti.ref(), err)
	}
}

func (s *syncer) executeThread(ti *tinfo) {
	switch ti.op {
	case opResolve:
		err := s.resolveAndRecord(ti)
		if err == nil {
			return
		}
		if ti.goneFall {
			s.warnResolve(ti, err)
			s.tryGone(ti)
			return
		}
		// N1: one summary line carries the reason, not one warning per thread.
		s.res.StaleOpen++
		if errors.Is(err, errNoPermission) {
			if s.res.StaleReason == "" {
				s.res.StaleReason = "permission"
			}
		} else if s.res.StaleReason == "" || s.res.StaleReason == "permission" {
			s.res.StaleReason = err.Error()
		}
	case opUnresolve:
		g := ti.group
		if !ti.t.CanUnresolve || !ti.t.Root.ViewerCanUpdate {
			s.warn("unresolve", g.ref(), nil)
			s.res.Unanchored = append(s.res.Unanchored, g.Members...)
			return
		}
		if err := UnresolveThread(s.ctx, s.p.Runner, ti.t.NodeID); err != nil {
			s.warn("unresolve", g.ref(), err)
			s.res.Unanchored = append(s.res.Unanchored, g.Members...)
			return
		}
		ti.open = true
		s.res.Updated++
		if err := s.flush(ti, &mark{}); err != nil {
			s.warn("record", ti.ref(), err)
		}
		if ti.back {
			s.tryBack(ti)
		}
	default:
		s.flushPlain(ti)
		if ti.back && ti.postIdx < 0 {
			s.tryBack(ti)
		}
	}
}

func shortOID(oid string) string {
	if len(oid) > 7 {
		return oid[:7]
	}
	return oid
}

// statusOf returns the kind of the latest status reply CommitBrief left in
// the thread ("gone", "back" or ""), looking at the recent window only.
func statusOf(t Thread) string {
	for i := len(t.Recent) - 1; i >= 0; i-- {
		c := t.Recent[i]
		if c.ID == t.Root.ID || !c.ViewerDidAuthor {
			continue
		}
		if k := statusReplyKind(c.Body); k != "" {
			return k
		}
	}
	return ""
}

func (s *syncer) reply(ti *tinfo, text string) {
	if !ti.t.CanReply {
		s.warn("reply", ti.ref(), nil)
		return
	}
	if err := ReplyToComment(s.ctx, s.p.Runner, s.p.Slug, s.p.PRNumber, ti.t.Root.ID, text); err != nil {
		s.warn("reply", ti.ref(), err)
		return
	}
	s.res.Replied++
}

func (s *syncer) tryGone(ti *tinfo) {
	if statusOf(ti.t) == "gone" {
		return
	}
	s.reply(ti, fmt.Sprintf("No longer present as of %s. %s <!-- commitbrief:gone v=1 -->", shortOID(s.p.CommitID), signature))
}

func (s *syncer) tryBack(ti *tinfo) {
	if statusOf(ti.t) != "gone" {
		return
	}
	s.reply(ti, fmt.Sprintf("Present again as of %s. %s <!-- commitbrief:back v=1 -->", shortOID(s.p.CommitID), signature))
}

func (s *syncer) executePosts() {
	if len(s.posts) == 0 {
		return
	}
	cands := make([]render.Finding, len(s.posts))
	for i, it := range s.posts {
		f := it.g.Rep
		f.Severity = it.g.Severity
		f.Fingerprint = strconv.Itoa(i)
		cands[i] = f
	}
	selected := cands
	if s.p.Select != nil {
		selected = s.p.Select(cands)
	}
	for _, c := range selected {
		i, err := strconv.Atoi(c.Fingerprint)
		if err != nil || i < 0 || i >= len(s.posts) {
			continue
		}
		it := s.posts[i]
		g := it.g
		if !g.Anchored {
			if it.fallback {
				s.res.Unanchored = append(s.res.Unanchored, g.Members...)
			}
			continue
		}
		err = PostComment(s.ctx, s.p.Runner, CommentRequest{
			RepoSlug: s.p.Slug,
			PRNumber: s.p.PRNumber,
			CommitID: s.p.CommitID,
			Path:     g.Rep.File,
			Line:     g.Rep.Line,
			Side:     g.Side,
			Body:     g.body(),
		})
		if err != nil {
			s.res.PostFailed++
			s.warn("post", g.ref(), err)
			if it.fallback {
				s.res.Unanchored = append(s.res.Unanchored, g.Members...)
			}
			continue
		}
		s.res.Posted++
		s.postOK[i] = true
		if it.old != nil {
			s.warnResolve(it.old, s.resolveAndRecord(it.old))
		}
	}
	// P4 threads whose replacement comment never appeared (cap, unanchorable
	// or failed POST) are still ours and still current: keep their body fresh
	// and count them, so the summary adds up to the findings.
	for i, it := range s.posts {
		if it.old != nil && !s.postOK[i] {
			s.keepOrUpdate(it.old, it.g)
		}
	}
}

// keepOrUpdate refreshes a still-current thread's body in place, or counts it
// unchanged when it already matches or cannot be edited.
func (s *syncer) keepOrUpdate(ti *tinfo, g *Group) {
	if ti.text == g.Text || !ti.t.Root.ViewerCanUpdate {
		s.res.Unchanged++
		return
	}
	txt := g.Text
	ti.newText = &txt
	s.flushPlain(ti)
}
