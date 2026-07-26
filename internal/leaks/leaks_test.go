// SPDX-License-Identifier: GPL-3.0-or-later

package leaks

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/ignore"
)

// A syntactically valid AWS key that matches the built-in pattern. It is a
// well-known documentation placeholder, not a real credential.
const fakeAWSKey = "AKIAIOSFODNN7EXAMPLE"

type repo struct{ dir string }

func newRepo(t *testing.T) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	r := &repo{dir: t.TempDir()}
	r.git(t, "init", "-q", "-b", "main")
	r.git(t, "config", "user.name", "Test")
	r.git(t, "config", "user.email", "test@example.com")
	r.git(t, "config", "commit.gpgsign", "false")
	return r
}

func (r *repo) git(t *testing.T, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func (r *repo) write(t *testing.T, rel, content string) {
	t.Helper()
	path := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (r *repo) commit(t *testing.T, msg string, paths ...string) {
	t.Helper()
	args := append([]string{"add"}, paths...)
	r.git(t, args...)
	r.git(t, "commit", "-q", "-m", msg)
}

func mustScanTree(t *testing.T, r *repo, opts Options) Result {
	t.Helper()
	res, err := ScanWorktree(context.Background(), r.dir, opts)
	if err != nil {
		t.Fatalf("ScanWorktree: %v", err)
	}
	return res
}

func files(res Result) []string {
	out := make([]string, 0, len(res.Findings))
	for _, f := range res.Findings {
		out = append(out, fmt.Sprintf("%s:%d", f.File, f.Line))
	}
	return out
}

// ---------- worktree ----------

func TestScanWorktreeFindsTrackedSecret(t *testing.T) {
	r := newRepo(t)
	r.write(t, "config.yml", "key: "+fakeAWSKey+"\n")
	r.write(t, "clean.go", "package app\n")
	r.commit(t, "initial", "config.yml", "clean.go")

	res := mustScanTree(t, r, Options{})
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %v", files(res))
	}
	f := res.Findings[0]
	if f.File != "config.yml" || f.Line != 1 {
		t.Errorf("finding = %s:%d, want config.yml:1", f.File, f.Line)
	}
	if len(f.Patterns) == 0 || f.Patterns[0] != "AWS Access Key" {
		t.Errorf("patterns = %v, want [AWS Access Key]", f.Patterns)
	}
	if f.FromHistory() {
		t.Error("a worktree hit must not claim commit attribution")
	}
	if res.FilesScanned != 2 {
		t.Errorf("FilesScanned = %d, want 2", res.FilesScanned)
	}
}

// The invariant that matters most: the scanner reads whole files, so it must
// never echo what it found. See engineering/standards/security.md.
func TestScanWorktreeNeverRecordsTheSecret(t *testing.T) {
	r := newRepo(t)
	r.write(t, "config.yml", "key: "+fakeAWSKey+"\n")
	r.commit(t, "initial", "config.yml")

	res := mustScanTree(t, r, Options{})
	if len(res.Findings) == 0 {
		t.Fatal("expected a finding to check")
	}
	dump := fmt.Sprintf("%#v", res)
	if strings.Contains(dump, fakeAWSKey) {
		t.Fatalf("the matched secret leaked into the Result:\n%s", dump)
	}
}

func TestScanWorktreeIgnoresUntrackedFiles(t *testing.T) {
	// An untracked, usually-gitignored .env is where a secret is SUPPOSED to
	// live. Flagging it would be noise, and it cannot leak through git.
	r := newRepo(t)
	r.write(t, "clean.go", "package app\n")
	r.commit(t, "initial", "clean.go")
	r.write(t, ".env", "AWS_KEY="+fakeAWSKey+"\n")

	res := mustScanTree(t, r, Options{})
	if len(res.Findings) != 0 {
		t.Fatalf("untracked files must not be scanned, got %v", files(res))
	}
}

func TestScanWorktreeHonorsIgnoreLayers(t *testing.T) {
	r := newRepo(t)
	r.write(t, "vendor/lib.go", "key := \""+fakeAWSKey+"\"\n")
	r.write(t, "app.go", "package app\n")
	r.commit(t, "initial", "vendor/lib.go", "app.go")

	res := mustScanTree(t, r, Options{Matcher: ignore.Builtin()})
	if len(res.Findings) != 0 {
		t.Fatalf("vendor/** is a built-in ignore; got %v", files(res))
	}

	// Without the matcher the same file is reported — proving the layer, not
	// some other filter, is what excluded it.
	if got := mustScanTree(t, r, Options{}); len(got.Findings) != 1 {
		t.Fatalf("without the ignore layer the hit should surface, got %v", files(got))
	}
}

func TestScanWorktreeHonorsPathFilters(t *testing.T) {
	r := newRepo(t)
	r.write(t, "src/a.yml", "key: "+fakeAWSKey+"\n")
	r.write(t, "docs/b.yml", "key: "+fakeAWSKey+"\n")
	r.commit(t, "initial", "src/a.yml", "docs/b.yml")

	res := mustScanTree(t, r, Options{Dirs: []string{"src"}})
	if len(res.Findings) != 1 || res.Findings[0].File != "src/a.yml" {
		t.Fatalf("--dir should narrow to src/, got %v", files(res))
	}

	res = mustScanTree(t, r, Options{ExcludeDirs: []string{"docs"}})
	if len(res.Findings) != 1 || res.Findings[0].File != "src/a.yml" {
		t.Fatalf("--exclude-dir should drop docs/, got %v", files(res))
	}
}

func TestScanWorktreeSkipsBinaryAndOversizedFiles(t *testing.T) {
	r := newRepo(t)
	// A NUL byte in the first 8 KiB marks the file binary; the key that
	// follows must not be reported.
	r.write(t, "blob.bin", "\x00\x01\x02 "+fakeAWSKey+"\n")
	r.write(t, "big.txt", strings.Repeat("x", 64)+"\n"+fakeAWSKey+"\n")
	r.commit(t, "initial", "blob.bin", "big.txt")

	res := mustScanTree(t, r, Options{MaxFileBytes: 32})
	if len(res.Findings) != 0 {
		t.Fatalf("binary and oversized files must be skipped, got %v", files(res))
	}
	if res.Skipped != 2 {
		t.Errorf("Skipped = %d, want 2 (skips are reported, never silent)", res.Skipped)
	}
}

func TestScanWorktreeUserPatterns(t *testing.T) {
	r := newRepo(t)
	r.write(t, "svc.conf", "token = INT-0123456789\n")
	r.commit(t, "initial", "svc.conf")

	if res := mustScanTree(t, r, Options{}); len(res.Findings) != 0 {
		t.Fatalf("the house format should not match a built-in, got %v", files(res))
	}

	res := mustScanTree(t, r, Options{
		Patterns: []guard.UserSecretPattern{{Name: "Internal Token", Regex: `INT-[0-9]{10}`}},
	})
	if len(res.Findings) != 1 || res.Findings[0].Patterns[0] != "Internal Token" {
		t.Fatalf("user pattern should match, got %v", res.Findings)
	}
}

func TestScanWorktreeInvalidUserPatternFailsBeforeReadingAnything(t *testing.T) {
	r := newRepo(t)
	r.write(t, "a.txt", "hello\n")
	r.commit(t, "initial", "a.txt")

	_, err := ScanWorktree(context.Background(), r.dir, Options{
		Patterns: []guard.UserSecretPattern{{Name: "Broken", Regex: "([unclosed"}},
	})
	if err == nil {
		t.Fatal("an invalid user regex must fail the scan, not be skipped")
	}
	if !strings.Contains(err.Error(), "Broken") {
		t.Errorf("error should name the offending pattern; got %v", err)
	}
}

func TestScanWorktreeCleanRepo(t *testing.T) {
	r := newRepo(t)
	r.write(t, "a.go", "package app\n")
	r.commit(t, "initial", "a.go")

	res := mustScanTree(t, r, Options{})
	if len(res.Findings) != 0 {
		t.Fatalf("clean repo should yield no findings, got %v", files(res))
	}
}

// ---------- history ----------

func selectAll(t *testing.T, r *repo) git.Selection {
	t.Helper()
	sel, err := git.SelectCommits(context.Background(), r.dir, git.CommitFilter{
		Text: "", Rev: []string{"HEAD"}, MaxCommits: 50,
	})
	if err != nil {
		t.Fatalf("SelectCommits: %v", err)
	}
	// CommitFilter.Active() is false with no predicate, but SelectCommits
	// still walks — which is what this helper wants.
	return sel
}

// The whole reason to scan history: a secret that was committed and later
// removed is still reachable in the repo.
func TestScanHistoryFindsRemovedSecret(t *testing.T) {
	r := newRepo(t)
	r.write(t, "config.yml", "key: "+fakeAWSKey+"\n")
	r.commit(t, "add config", "config.yml")
	r.write(t, "config.yml", "key: REDACTED\n")
	r.commit(t, "scrub the key", "config.yml")

	// It is gone from the tree...
	if res := mustScanTree(t, r, Options{}); len(res.Findings) != 0 {
		t.Fatalf("the working tree is clean; got %v", files(res))
	}

	// ...but not from the history.
	res, err := ScanHistory(context.Background(), r.dir, selectAll(t, r), Options{})
	if err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected the historical hit, got %v", files(res))
	}
	f := res.Findings[0]
	if f.File != "config.yml" || f.Line != 1 {
		t.Errorf("finding = %s:%d, want config.yml:1", f.File, f.Line)
	}
	if !f.FromHistory() || f.Short == "" || f.Author != "Test" || f.Date.IsZero() {
		t.Errorf("history hit needs full attribution, got %#v", f)
	}
}

func TestScanHistoryNeverRecordsTheSecret(t *testing.T) {
	r := newRepo(t)
	r.write(t, "config.yml", "key: "+fakeAWSKey+"\n")
	r.commit(t, "add config", "config.yml")

	res, err := ScanHistory(context.Background(), r.dir, selectAll(t, r), Options{})
	if err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if len(res.Findings) == 0 {
		t.Fatal("expected a finding to check")
	}
	if dump := fmt.Sprintf("%#v", res); strings.Contains(dump, fakeAWSKey) {
		t.Fatalf("the matched secret leaked into the Result:\n%s", dump)
	}
}

func TestScanHistoryReportsRealFileLineNumbers(t *testing.T) {
	// guard's SecretMatch.Line counts lines within the diff string, which is
	// meaningless once the diff is gone. The reported number must point at the
	// file as that commit left it.
	r := newRepo(t)
	r.write(t, "app.conf", "alpha\nbeta\ngamma\ndelta\n")
	r.commit(t, "initial", "app.conf")
	r.write(t, "app.conf", "alpha\nbeta\ngamma\ndelta\nkey = "+fakeAWSKey+"\n")
	r.commit(t, "append key", "app.conf")

	res, err := ScanHistory(context.Background(), r.dir, selectAll(t, r), Options{})
	if err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if len(res.Findings) != 1 {
		t.Fatalf("expected 1 finding, got %v", files(res))
	}
	if res.Findings[0].Line != 5 {
		t.Errorf("Line = %d, want 5 (the real line in the post-image)", res.Findings[0].Line)
	}
}

func TestScanHistoryHonorsPathFilters(t *testing.T) {
	r := newRepo(t)
	r.write(t, "src/a.yml", "key: "+fakeAWSKey+"\n")
	r.write(t, "docs/b.yml", "key: "+fakeAWSKey+"\n")
	r.commit(t, "initial", "src/a.yml", "docs/b.yml")

	res, err := ScanHistory(context.Background(), r.dir, selectAll(t, r), Options{Dirs: []string{"src"}})
	if err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "src/a.yml" {
		t.Fatalf("--dir should narrow the history scan too, got %v", files(res))
	}
}

func TestScanHistoryEmptySelection(t *testing.T) {
	r := newRepo(t)
	r.write(t, "a.go", "package app\n")
	r.commit(t, "initial", "a.go")

	res, err := ScanHistory(context.Background(), r.dir, git.Selection{}, Options{})
	if err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if len(res.Findings) != 0 || res.CommitsScanned != 0 {
		t.Fatalf("an empty selection scans nothing, got %#v", res)
	}
}

func TestScanHistoryPropagatesTruncation(t *testing.T) {
	r := newRepo(t)
	r.write(t, "a.go", "package app\n")
	r.commit(t, "initial", "a.go")

	res, err := ScanHistory(context.Background(), r.dir,
		git.Selection{Truncated: true}, Options{})
	if err != nil {
		t.Fatalf("ScanHistory: %v", err)
	}
	if !res.Truncated {
		t.Error("a truncated selection must stay truncated in the result")
	}
}

// ---------- shared ----------

func TestResultMergeAndSort(t *testing.T) {
	tree := Result{
		Findings:     []Finding{{File: "b.txt", Line: 2}},
		FilesScanned: 3,
		Skipped:      1,
	}
	hist := Result{
		Findings:       []Finding{{File: "a.txt", Line: 1, Commit: "abc"}},
		FilesScanned:   2,
		CommitsScanned: 4,
		Truncated:      true,
	}

	merged := tree.Merge(hist)
	if merged.FilesScanned != 5 || merged.CommitsScanned != 4 || merged.Skipped != 1 {
		t.Errorf("counters not summed: %#v", merged)
	}
	if !merged.Truncated {
		t.Error("truncation must survive a merge")
	}

	merged.Sort()
	// Worktree hits sort before history hits regardless of path, so the report
	// leads with what is on disk right now.
	if merged.Findings[0].FromHistory() {
		t.Errorf("worktree findings should sort first, got %#v", merged.Findings)
	}
}

func TestIsHighNotCritical(t *testing.T) {
	user := []guard.UserSecretPattern{{Name: "Internal Token", Regex: "x"}}

	if !IsHighNotCritical("JWT", nil) {
		t.Error("JWT is the one built-in with a real false-positive rate; want high")
	}
	if IsHighNotCritical("AWS Access Key", nil) {
		t.Error("AWS Access Key should stay critical")
	}
	if !IsHighNotCritical("Internal Token", user) {
		t.Error("user patterns cannot be asserted critical; want high")
	}
	if IsHighNotCritical("Internal Token", nil) {
		t.Error("an unknown pattern with no user set should stay critical")
	}
}

func TestIsBinary(t *testing.T) {
	if !isBinary([]byte("abc\x00def")) {
		t.Error("a NUL byte marks the content binary")
	}
	if isBinary([]byte("plain text\nwith unicode ✓\n")) {
		t.Error("UTF-8 text must not be classified binary")
	}
	if isBinary(nil) {
		t.Error("empty content is not binary")
	}
	// The sniff only inspects the head, so a NUL far past the window is missed
	// by design — that is the documented, git-standard trade-off.
	tail := append([]byte(strings.Repeat("a", binarySniffBytes+10)), 0)
	if isBinary(tail) {
		t.Error("the sniff should only inspect the first 8 KiB")
	}
}
