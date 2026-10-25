// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The selection layer is pure git-CLI interop, so these fixtures are built
// with the real `git` binary rather than go-git: only the CLI lets us pin
// author identity, committer identity and author date per commit, and build a
// genuine merge commit — all of which are exactly what we need to filter on.

type filterRepo struct {
	dir string
}

func (r *filterRepo) git(t *testing.T, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// commitAs writes a file and commits it as the given identity on the given
// author date (YYYY-MM-DD). Committer identity defaults to the author unless
// committerName is non-empty.
func (r *filterRepo) commitAs(t *testing.T, name, email, date, file, msg, committerName string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(r.dir, file)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, file), []byte(msg+"\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	stamp := date + "T12:00:00+00:00"
	cName, cEmail := name, email
	if committerName != "" {
		cName, cEmail = committerName, strings.ToLower(committerName)+"@example.com"
	}
	env := []string{
		"GIT_AUTHOR_NAME=" + name,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_AUTHOR_DATE=" + stamp,
		"GIT_COMMITTER_NAME=" + cName,
		"GIT_COMMITTER_EMAIL=" + cEmail,
		"GIT_COMMITTER_DATE=" + stamp,
	}
	r.git(t, nil, "add", file)
	r.git(t, env, "commit", "-m", msg)
}

// newFilterRepo builds a history purpose-made for the filter tests:
//
//	main:    C1(alice 2026-01-10) — C2(bob 2026-02-10) — C3(alice 2026-03-10, committed by carol)
//	payments: branches off C1, carries C4(bob 2026-02-20) — merged back as M
//
// so every filter kind has both a hit and a miss to prove against.
func newFilterRepo(t *testing.T) *filterRepo {
	t.Helper()
	requireGitCLI(t)
	r := &filterRepo{dir: t.TempDir()}
	r.git(t, nil, "init", "-q", "-b", "main")
	r.git(t, nil, "config", "user.name", "Test")
	r.git(t, nil, "config", "user.email", "test@example.com")
	r.git(t, nil, "config", "commit.gpgsign", "false")

	r.commitAs(t, "Alice", "alice@example.com", "2026-01-10", "a.txt", "feat: add invoice calc", "")
	r.commitAs(t, "Bob", "bob@example.com", "2026-02-10", "b.txt", "fix: token refresh", "")

	// Feature branch off the first commit, so its commit is unique to it.
	r.git(t, nil, "checkout", "-q", "-b", "payments/stripe", "HEAD~1")
	r.commitAs(t, "Bob", "bob@example.com", "2026-02-20", "c.txt", "chore: bump sdk", "")
	r.git(t, nil, "checkout", "-q", "main")

	r.commitAs(t, "Alice", "alice@example.com", "2026-03-10", "d.txt", "docs: readme", "Carol")
	return r
}

func (r *filterRepo) merge(t *testing.T, ref string) {
	t.Helper()
	env := []string{
		"GIT_AUTHOR_NAME=Dave", "GIT_AUTHOR_EMAIL=dave@example.com",
		"GIT_AUTHOR_DATE=2026-04-01T12:00:00+00:00",
		"GIT_COMMITTER_NAME=Dave", "GIT_COMMITTER_EMAIL=dave@example.com",
		"GIT_COMMITTER_DATE=2026-04-01T12:00:00+00:00",
	}
	r.git(t, env, "merge", "--no-ff", "-m", "Merge branch "+ref, ref)
}

func day(s string) time.Time {
	ts, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return ts
}

// endOfDay mirrors what the CLI does to --end-date so the named day is
// inclusive.
func endOfDay(s string) time.Time {
	return day(s).Add(24*time.Hour - time.Second)
}

func subjects(sel Selection) []string {
	out := make([]string, 0, len(sel.Commits))
	for _, c := range sel.Commits {
		out = append(out, c.Subject)
	}
	return out
}

func mustSelect(t *testing.T, dir string, f CommitFilter) Selection {
	t.Helper()
	sel, err := SelectCommits(context.Background(), dir, f)
	if err != nil {
		t.Fatalf("SelectCommits: %v", err)
	}
	return sel
}

func TestCommitFilterActive(t *testing.T) {
	cases := []struct {
		name string
		f    CommitFilter
		want bool
	}{
		{"zero", CommitFilter{}, false},
		{"author", CommitFilter{Authors: []string{"alice"}}, true},
		{"committer", CommitFilter{Committers: []string{"carol"}}, true},
		{"since", CommitFilter{Since: day("2026-01-01")}, true},
		{"until", CommitFilter{Until: day("2026-01-01")}, true},
		{"text", CommitFilter{Text: "invoice"}, true},
		// Modifiers never activate on their own — otherwise `--merges` would
		// silently turn a staged review into a history walk.
		{"merges only", CommitFilter{Merges: true}, false},
		{"max only", CommitFilter{MaxCommits: 5}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.Active(); got != tc.want {
				t.Fatalf("Active() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSelectCommitsByAuthor(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"alice"}})
	want := []string{"docs: readme", "feat: add invoice calc"}
	if got := subjects(sel); !equalStrings(got, want) {
		t.Fatalf("subjects = %v, want %v", got, want)
	}
}

func TestSelectCommitsByAuthorEmail(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"bob@example.com"}})
	if got := subjects(sel); !equalStrings(got, []string{"fix: token refresh"}) {
		t.Fatalf("subjects = %v, want [fix: token refresh]", got)
	}
}

func TestSelectCommitsMultipleAuthorsAreOred(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"alice", "bob"}})
	if len(sel.Commits) != 3 {
		t.Fatalf("expected all 3 main-branch commits, got %v", subjects(sel))
	}
}

func TestSelectCommitsAuthorAndDateAreAnded(t *testing.T) {
	r := newFilterRepo(t)
	// Alice has commits in January and March; the window keeps only January.
	sel := mustSelect(t, r.dir, CommitFilter{
		Authors: []string{"alice"},
		Since:   day("2026-01-01"),
		Until:   endOfDay("2026-01-31"),
	})
	if got := subjects(sel); !equalStrings(got, []string{"feat: add invoice calc"}) {
		t.Fatalf("subjects = %v, want [feat: add invoice calc]", got)
	}
}

// The named --end-date day must be included. git's bare `--until=<date>` stops
// at that day's midnight, which would silently drop it.
func TestSelectCommitsEndDateIsInclusive(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Until: endOfDay("2026-02-10")})
	if got := subjects(sel); !equalStrings(got, []string{"fix: token refresh", "feat: add invoice calc"}) {
		t.Fatalf("subjects = %v, want the Jan + Feb 10 commits", got)
	}
}

func TestSelectCommitsStartDateIsInclusive(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Since: day("2026-03-10")})
	if got := subjects(sel); !equalStrings(got, []string{"docs: readme"}) {
		t.Fatalf("subjects = %v, want [docs: readme]", got)
	}
}

func TestSelectCommitsByCommitter(t *testing.T) {
	r := newFilterRepo(t)
	// Carol committed only the docs commit (authored by Alice).
	sel := mustSelect(t, r.dir, CommitFilter{Committers: []string{"carol"}})
	if got := subjects(sel); !equalStrings(got, []string{"docs: readme"}) {
		t.Fatalf("subjects = %v, want [docs: readme]", got)
	}
}

func TestSelectCommitsByMessageText(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Text: "invoice"})
	if got := subjects(sel); !equalStrings(got, []string{"feat: add invoice calc"}) {
		t.Fatalf("subjects = %v, want [feat: add invoice calc]", got)
	}
}

func TestSelectCommitsTextIsCaseInsensitive(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Text: "INVOICE"})
	if len(sel.Commits) != 1 {
		t.Fatalf("case-insensitive match failed, got %v", subjects(sel))
	}
}

// --text also matches BRANCH NAMES: `payments/stripe` is not on HEAD, and its
// commit's message says nothing about payments, yet it must be selected.
func TestSelectCommitsByBranchName(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Text: "payments"})
	if got := subjects(sel); !equalStrings(got, []string{"chore: bump sdk"}) {
		t.Fatalf("subjects = %v, want [chore: bump sdk] from the payments/stripe branch", got)
	}
}

func TestSelectCommitsBranchNameAndMessageUnion(t *testing.T) {
	r := newFilterRepo(t)
	// "token" hits a message; add a branch whose name also contains it.
	r.git(t, nil, "branch", "token-work", "HEAD")
	sel := mustSelect(t, r.dir, CommitFilter{Text: "token"})
	if len(sel.Commits) == 0 {
		t.Fatal("expected at least the message match")
	}
	found := false
	for _, s := range subjects(sel) {
		if s == "fix: token refresh" {
			found = true
		}
	}
	if !found {
		t.Fatalf("message match missing from %v", subjects(sel))
	}
}

func TestSelectCommitsBranchNameFiltersStillApply(t *testing.T) {
	r := newFilterRepo(t)
	// The payments/stripe commit is Bob's; asking for Alice's must exclude it
	// even though the branch name matches.
	sel := mustSelect(t, r.dir, CommitFilter{Text: "payments", Authors: []string{"alice"}})
	if len(sel.Commits) != 0 {
		t.Fatalf("identity filter must still apply to ref matches, got %v", subjects(sel))
	}
}

func TestSelectCommitsExcludesMergesByDefault(t *testing.T) {
	r := newFilterRepo(t)
	r.merge(t, "payments/stripe")
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"dave"}})
	if len(sel.Commits) != 0 {
		t.Fatalf("merge commit should be excluded by default, got %v", subjects(sel))
	}
}

func TestSelectCommitsIncludesMergesWhenAsked(t *testing.T) {
	r := newFilterRepo(t)
	r.merge(t, "payments/stripe")
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"dave"}, Merges: true})
	if len(sel.Commits) != 1 {
		t.Fatalf("expected the merge commit, got %v", subjects(sel))
	}
}

func TestSelectCommitsTruncatesAtMaxCommits(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Since: day("2026-01-01"), MaxCommits: 2})
	if !sel.Truncated {
		t.Fatal("expected Truncated")
	}
	if len(sel.Commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(sel.Commits))
	}
	// Newest first, so the cap keeps the most recent work.
	if sel.Commits[0].Subject != "docs: readme" {
		t.Fatalf("expected newest-first ordering, got %v", subjects(sel))
	}
}

// withWalkLimit lowers the phase-A ceiling for one test.
func withWalkLimit(t *testing.T, n int) {
	t.Helper()
	old := walkLimit
	walkLimit = n
	t.Cleanup(func() { walkLimit = old })
}

// TestSelectCommitsWalkLimitBoundary pins the off-by-one: a history of exactly
// walkLimit commits was fully inspected and must not be reported as cut, while
// one commit more must be, and then only walkLimit commits are processed.
// The filter repo has three non-merge commits on main.
func TestSelectCommitsWalkLimitBoundary(t *testing.T) {
	r := newFilterRepo(t)
	f := CommitFilter{Since: day("2026-01-01")}

	cases := []struct {
		name          string
		limit         int
		wantTruncated bool
		wantWalked    int
	}{
		{"limit above history", 4, false, 3},
		{"limit equals history", 3, false, 3},
		{"limit one below history", 2, true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withWalkLimit(t, tc.limit)
			sel := mustSelect(t, r.dir, f)
			if sel.WalkTruncated != tc.wantTruncated {
				t.Fatalf("WalkTruncated = %v, want %v", sel.WalkTruncated, tc.wantTruncated)
			}
			if sel.Walked != tc.wantWalked || len(sel.Commits) != tc.wantWalked {
				t.Fatalf("Walked = %d, commits = %d, want %d", sel.Walked, len(sel.Commits), tc.wantWalked)
			}
		})
	}
}

func TestSelectCommitsNoMatchIsNotAnError(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"nobody"}})
	if len(sel.Commits) != 0 {
		t.Fatalf("expected no matches, got %v", subjects(sel))
	}
	if sel.Truncated {
		t.Fatal("empty selection must not report truncation")
	}
}

func TestSelectCommitsHonorsExplicitRange(t *testing.T) {
	r := newFilterRepo(t)
	// Only the newest commit is in HEAD~1..HEAD, so Alice's January commit is
	// out of range even though she matches.
	sel := mustSelect(t, r.dir, CommitFilter{Authors: []string{"alice"}, Rev: []string{"HEAD~1..HEAD"}})
	if got := subjects(sel); !equalStrings(got, []string{"docs: readme"}) {
		t.Fatalf("subjects = %v, want [docs: readme]", got)
	}
}

func TestSelectCommitsPopulatesMetadata(t *testing.T) {
	r := newFilterRepo(t)
	sel := mustSelect(t, r.dir, CommitFilter{Text: "invoice"})
	if len(sel.Commits) != 1 {
		t.Fatalf("expected 1 commit, got %d", len(sel.Commits))
	}
	c := sel.Commits[0]
	if len(c.Hash) != 40 {
		t.Errorf("Hash = %q, want a 40-hex sha", c.Hash)
	}
	if c.Short == "" {
		t.Error("Short is empty")
	}
	if c.Author != "Alice" || c.AuthorEmail != "alice@example.com" {
		t.Errorf("author = %q <%q>, want Alice <alice@example.com>", c.Author, c.AuthorEmail)
	}
	if c.Committer != "Alice" {
		t.Errorf("committer = %q, want Alice", c.Committer)
	}
	if c.Date.UTC().Format("2006-01-02") != "2026-01-10" {
		t.Errorf("date = %v, want 2026-01-10", c.Date)
	}
	if !equalStrings(c.Files, []string{"a.txt"}) {
		t.Errorf("files = %v, want [a.txt]", c.Files)
	}
}

func TestFilteredDiffConcatenatesPerCommitPatches(t *testing.T) {
	r := newFilterRepo(t)
	d, sel, err := FilteredDiff(context.Background(), r.dir, CommitFilter{Authors: []string{"alice"}})
	if err != nil {
		t.Fatalf("FilteredDiff: %v", err)
	}
	if d.Origin != OriginFiltered {
		t.Errorf("origin = %q, want %q", d.Origin, OriginFiltered)
	}
	if d.Args["commits"] != "2" {
		t.Errorf("args[commits] = %q, want 2", d.Args["commits"])
	}
	if len(sel.Commits) != 2 {
		t.Fatalf("expected 2 commits, got %d", len(sel.Commits))
	}
	// Both commits' files must appear, and nothing from Bob's.
	if !strings.Contains(d.Content, "d.txt") || !strings.Contains(d.Content, "a.txt") {
		t.Errorf("expected both Alice patches, got:\n%s", d.Content)
	}
	if strings.Contains(d.Content, "b.txt") {
		t.Errorf("Bob's commit leaked into the diff:\n%s", d.Content)
	}
	// No `git show` preamble may survive — the content starts at a patch.
	if !strings.HasPrefix(d.Content, "diff --git ") {
		t.Errorf("content should start with a patch header, got:\n%.120s", d.Content)
	}
	if strings.Contains(d.Content, "Author:") || strings.Contains(d.Content, "commit "+sel.Commits[0].Hash) {
		t.Errorf("commit headers leaked into the patch text:\n%s", d.Content)
	}
}

func TestFilteredDiffEmptySelectionYieldsEmptyDiff(t *testing.T) {
	r := newFilterRepo(t)
	d, sel, err := FilteredDiff(context.Background(), r.dir, CommitFilter{Authors: []string{"nobody"}})
	if err != nil {
		t.Fatalf("FilteredDiff: %v", err)
	}
	if !d.Empty() {
		t.Errorf("expected an empty diff, got %q", d.Content)
	}
	if len(sel.Commits) != 0 {
		t.Errorf("expected no commits, got %d", len(sel.Commits))
	}
}

func TestPatchBodyStripsPreamble(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  string
	}{
		{"already a patch", "diff --git a/x b/x\n@@\n", "diff --git a/x b/x\n@@\n"},
		{"leading blank", "\ndiff --git a/x b/x\n", "diff --git a/x b/x\n"},
		{"no patch at all", "\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := patchBody(tc.block); got != tc.want {
				t.Fatalf("patchBody(%q) = %q, want %q", tc.block, got, tc.want)
			}
		})
	}
}

func TestParseSelectCommitsSkipsMalformed(t *testing.T) {
	good := logRecordSep +
		strings.Join([]string{
			"1111111111111111111111111111111111111111", "1111111",
			"Alice", "alice@example.com", "Alice", "alice@example.com",
			"2026-01-10T12:00:00+00:00",
			"2222222222222222222222222222222222222222 3333333333333333333333333333333333333333",
			"feat: x", "body",
		}, logFieldSep) + logFieldSep + "\nM\tx.go\n"
	out := logRecordSep + "deadbeef" + good // first record has no field separators

	got := parseSelectCommits(out)
	if len(got) != 1 {
		t.Fatalf("expected 1 well-formed record, got %d: %#v", len(got), got)
	}
	if got[0].Author != "Alice" || got[0].Date.IsZero() || !equalStrings(got[0].Files, []string{"x.go"}) {
		t.Fatalf("record not parsed as expected: %#v", got[0])
	}
	// %P is space-separated, so a merge commit yields both parents.
	if len(got[0].Parents) != 2 {
		t.Fatalf("expected 2 parents, got %#v", got[0].Parents)
	}
	if got[0].Subject != "feat: x" || got[0].Body != "body" {
		t.Fatalf("parents field must not shift subject/body: %#v", got[0])
	}
}

func TestChunk(t *testing.T) {
	got := chunk([]string{"a", "b", "c", "d", "e"}, 2)
	if len(got) != 3 || len(got[0]) != 2 || len(got[2]) != 1 {
		t.Fatalf("chunk mismatch: %#v", got)
	}
	if chunk(nil, 2) != nil {
		t.Fatal("chunk(nil) should be nil")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// newTopicRepo builds main with one commit plus a branch "big-topic" carrying
// n commits that exist on no other ref, so phase A (HEAD = main) sees one
// commit and phase A′ (--text big-topic) sees n.
func newTopicRepo(t *testing.T, n int) *filterRepo {
	t.Helper()
	requireGitCLI(t)
	r := &filterRepo{dir: t.TempDir()}
	r.git(t, nil, "init", "-q", "-b", "main")
	r.git(t, nil, "config", "user.name", "Test")
	r.git(t, nil, "config", "user.email", "test@example.com")
	r.git(t, nil, "config", "commit.gpgsign", "false")
	r.commitAs(t, "Alice", "alice@example.com", "2026-01-10", "base.txt", "chore: base", "")
	r.git(t, nil, "checkout", "-q", "-b", "big-topic")
	for i := 1; i <= n; i++ {
		r.commitAs(t, "Bob", "bob@example.com", fmt.Sprintf("2026-02-%02d", i),
			fmt.Sprintf("t%d.txt", i), fmt.Sprintf("work item %d", i), "")
	}
	r.git(t, nil, "checkout", "-q", "main")
	return r
}

// Phase A′ is bounded by walkLimit like phase A, not by MaxCommits: a branch
// holding more matching commits than MaxCommits must surface as Truncated (the
// final cut), never be cut silently inside the ref walk.
func TestSelectCommitsBranchNameHonorsMaxCommitsAsTruncation(t *testing.T) {
	r := newTopicRepo(t, 3)
	sel := mustSelect(t, r.dir, CommitFilter{Text: "big-topic", MaxCommits: 1})
	if !sel.Truncated {
		t.Fatalf("expected Truncated when the branch holds 3 commits and MaxCommits is 1, got %v", subjects(sel))
	}
	if len(sel.Commits) != 1 {
		t.Fatalf("expected 1 commit after the cut, got %d", len(sel.Commits))
	}
	if sel.WalkTruncated {
		t.Fatal("WalkTruncated must stay false: the walk ceiling was not reached")
	}
}

// Phase A′ sets WalkTruncated with the same off-by-one rule as phase A:
// exactly walkLimit branch commits were fully inspected, one more was not.
func TestSelectCommitsBranchNameWalkLimitBoundary(t *testing.T) {
	r := newTopicRepo(t, 3)
	f := CommitFilter{Text: "big-topic", MaxCommits: 100}

	cases := []struct {
		name          string
		limit         int
		wantTruncated bool
		wantCommits   int
	}{
		{"limit above branch", 4, false, 3},
		{"limit equals branch", 3, false, 3},
		{"limit one below branch", 2, true, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withWalkLimit(t, tc.limit)
			sel := mustSelect(t, r.dir, f)
			if sel.WalkTruncated != tc.wantTruncated {
				t.Fatalf("WalkTruncated = %v, want %v", sel.WalkTruncated, tc.wantTruncated)
			}
			if len(sel.Commits) != tc.wantCommits {
				t.Fatalf("commits = %d (%v), want %d", len(sel.Commits), subjects(sel), tc.wantCommits)
			}
		})
	}
}
