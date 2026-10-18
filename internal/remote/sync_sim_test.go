// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// simThread is one review thread of the simulated PR.
type simThread struct {
	id           string
	path         string
	resolved     bool
	outdated     bool
	canResolve   bool
	canUnresolve bool
	canReply     bool
	canUpdate    bool
	comments     []Comment
}

// ghSim is a stateful fake `gh`: it understands the exact calls the sync
// layer makes (thread listing, resolve/unresolve, PATCH, reply, POST) and
// mutates its own thread list the way GitHub would.
type ghSim struct {
	t       *testing.T
	threads []*simThread
	nextID  int64
	calls   []string

	failResolve   bool
	failUnresolve bool
	failPost      bool
	failPatch     bool
	failList      bool
}

func newSim(t *testing.T) *ghSim { return &ghSim{t: t, nextID: 1000} }

func (s *ghSim) newID() int64 { s.nextID++; return s.nextID }

// add creates a thread authored by us with the given root body.
func (s *ghSim) add(body string) *simThread {
	id := s.newID()
	th := &simThread{
		id: "T" + strconv.FormatInt(id, 10), path: "a.go", canResolve: true, canUnresolve: true,
		canReply: true, canUpdate: true,
		comments: []Comment{{ID: id, Body: body, ViewerDidAuthor: true, ViewerCanUpdate: true}},
	}
	s.threads = append(s.threads, th)
	return th
}

func (s *ghSim) byNode(id string) *simThread {
	for _, th := range s.threads {
		if th.id == id {
			return th
		}
	}
	return nil
}

func (s *ghSim) byRoot(id int64) *simThread {
	for _, th := range s.threads {
		if th.comments[0].ID == id {
			return th
		}
	}
	return nil
}

func (s *ghSim) count(prefix string) int {
	n := 0
	for _, c := range s.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func (s *ghSim) reset() { s.calls = nil }

func arg(args []string, key string) string {
	for i, a := range args {
		if a == key && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, key+"=") {
			return strings.TrimPrefix(a, key+"=")
		}
	}
	return ""
}

func (s *ghSim) listJSON() []byte {
	nodes := []map[string]any{}
	for _, th := range s.threads {
		root := th.comments[0]
		recent := th.comments
		if len(recent) > 10 {
			recent = recent[len(recent)-10:]
		}
		rc := []map[string]any{}
		for _, c := range recent {
			rc = append(rc, map[string]any{"databaseId": c.ID, "body": c.Body, "viewerDidAuthor": c.ViewerDidAuthor})
		}
		nodes = append(nodes, map[string]any{
			"id": th.id, "path": th.path, "isResolved": th.resolved, "isOutdated": th.outdated,
			"viewerCanResolve": th.canResolve, "viewerCanUnresolve": th.canUnresolve, "viewerCanReply": th.canReply,
			"comments": map[string]any{"nodes": []map[string]any{{
				"databaseId": root.ID, "body": root.Body, "viewerDidAuthor": root.ViewerDidAuthor, "viewerCanUpdate": th.canUpdate,
			}}},
			"recent": map[string]any{"nodes": rc},
		})
	}
	page := map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"reviewThreads": map[string]any{"pageInfo": map[string]any{"hasNextPage": false}, "nodes": nodes},
	}}}}
	b, _ := json.Marshal(page)
	return b
}

func (s *ghSim) Run(_ context.Context, args ...string) ([]byte, error) {
	if len(args) < 2 || args[0] != "api" {
		s.t.Fatalf("unexpected gh call: %v", args)
	}
	if args[1] == "graphql" {
		q := arg(args, "-f")
		for i, a := range args {
			if strings.HasPrefix(a, "query=") {
				q = strings.TrimPrefix(a, "query=")
			}
			_ = i
		}
		switch {
		case strings.Contains(q, "unresolveReviewThread"):
			id := argKV(args, "id")
			s.calls = append(s.calls, "UNRESOLVE "+id)
			if s.failUnresolve {
				return nil, errors.New("unresolve denied")
			}
			s.byNode(id).resolved = false
			return []byte(`{"data":{}}`), nil
		case strings.Contains(q, "resolveReviewThread"):
			id := argKV(args, "id")
			s.calls = append(s.calls, "RESOLVE "+id)
			if s.failResolve {
				return nil, errors.New("resolve denied")
			}
			s.byNode(id).resolved = true
			return []byte(`{"data":{}}`), nil
		default:
			s.calls = append(s.calls, "LIST")
			if s.failList {
				return nil, errors.New("graphql down")
			}
			return s.listJSON(), nil
		}
	}
	method := arg(args, "--method")
	var endpoint string
	for _, a := range args {
		if strings.HasPrefix(a, "/repos/") {
			endpoint = a
		}
	}
	body := argKV(args, "body")
	switch {
	case method == "PATCH":
		id, _ := strconv.ParseInt(endpoint[strings.LastIndex(endpoint, "/")+1:], 10, 64)
		s.calls = append(s.calls, fmt.Sprintf("PATCH %d", id))
		if s.failPatch {
			return nil, errors.New("patch denied")
		}
		s.byRoot(id).comments[0].Body = body
	case method == "POST" && strings.HasSuffix(endpoint, "/replies"):
		parts := strings.Split(endpoint, "/")
		id, _ := strconv.ParseInt(parts[len(parts)-2], 10, 64)
		s.calls = append(s.calls, fmt.Sprintf("REPLY %d", id))
		th := s.byRoot(id)
		th.comments = append(th.comments, Comment{ID: s.newID(), Body: body, ViewerDidAuthor: true})
	case method == "POST":
		s.calls = append(s.calls, "POST")
		if s.failPost {
			return nil, errors.New("post denied")
		}
		s.add(body)
	default:
		s.t.Fatalf("unexpected REST call: %v", args)
	}
	return nil, nil
}

// argKV returns the value of a `-f key=value` / `-F key=value` pair.
func argKV(args []string, key string) string {
	for _, a := range args {
		if strings.HasPrefix(a, key+"=") {
			return strings.TrimPrefix(a, key+"=")
		}
	}
	return ""
}

// --- helpers shared by the sync tests ---

func fpOf(c string) string { return strings.Repeat(c, 64) }

// grp builds an anchorable single-member group.
func grp(c string, line int, title string) Group {
	f := render.Finding{Severity: render.SeverityHigh, File: "a.go", Line: line, Title: title, Fingerprint: fpOf(c)}
	return Group{FP: fpOf(c), Rep: f, Members: []render.Finding{f}, Severity: f.Severity, Side: "RIGHT", Anchored: true, Text: "[HIGH] - " + title}
}

// body renders a comment body for a group with extra marker attributes.
func markedBody(text, fp string, attrs ...string) string {
	m := NewMarker(fp)
	m.Attrs = attrs
	return withMarker(text, m)
}

// openThread adds an active thread whose body matches group g.
func (s *ghSim) active(g Group, attrs ...string) *simThread {
	return s.add(markedBody(g.Text, g.FP, attrs...))
}

func (s *ghSim) run(groups ...Group) Result {
	return s.runSel(nil, groups...)
}

func (s *ghSim) runSel(sel func([]render.Finding) []render.Finding, groups ...Group) Result {
	snap, err := FetchThreads(context.Background(), s, "o/r", 7)
	if err != nil {
		s.t.Fatalf("FetchThreads: %v", err)
	}
	s.reset()
	return Sync(context.Background(), SyncParams{
		Runner: s, Slug: "o/r", PRNumber: 7, CommitID: "abcdef0123456789",
		Threads: snap, Groups: groups, Select: sel,
	})
}

func (th *simThread) marker(t *testing.T) Marker {
	t.Helper()
	_, m, ok := SplitMarker(th.comments[0].Body)
	if !ok {
		t.Fatalf("thread %s lost its marker: %q", th.id, th.comments[0].Body)
	}
	return m
}

func (th *simThread) lastID() int64 { return th.comments[len(th.comments)-1].ID }
