package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRepoCurrentDir(t *testing.T) {
	f := newSimpleRepo(t)
	got, err := FindRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != f.dir {
		t.Errorf("FindRepo = %q, want %q", got, f.dir)
	}
}

func TestFindRepoParentTraversal(t *testing.T) {
	f := newSimpleRepo(t)
	nested := filepath.Join(f.dir, "a", "b", "c")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepo(nested)
	if err != nil {
		t.Fatal(err)
	}
	if got != f.dir {
		t.Errorf("FindRepo = %q, want %q (should walk up)", got, f.dir)
	}
}

func TestFindRepoNotARepo(t *testing.T) {
	tmp := t.TempDir()
	_, err := FindRepo(tmp)
	if !errors.Is(err, ErrNotARepo) {
		t.Errorf("err = %v, want ErrNotARepo", err)
	}
}

func TestFindRepoGitFileNotDir(t *testing.T) {
	// Submodule/worktree case: `.git` is a regular file containing a gitdir pointer.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != dir {
		t.Errorf("FindRepo = %q, want %q (`.git` as file should still mark a repo)", got, dir)
	}
}

func TestGoGitCommitDiff(t *testing.T) {
	f := newSimpleRepo(t)
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := g.CommitDiff(f.mainCommit.String())
	if err != nil {
		t.Fatalf("CommitDiff: %v", err)
	}
	if d.Origin != OriginCommit {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginCommit)
	}
	if !strings.Contains(d.Content, "hello again") {
		t.Errorf("diff content missing expected line; got:\n%s", d.Content)
	}
}

func TestGoGitCommitDiffInitialReturnsUnsupported(t *testing.T) {
	f := newSimpleRepo(t)
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = g.CommitDiff(f.firstCommit.String())
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("initial commit via go-git: err = %v, want ErrUnsupported (CLI fallback expected)", err)
	}
}

func TestDispatchInitialCommitFallsToCLI(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.CommitDiff(f.firstCommit.String())
	if err != nil {
		t.Fatalf("dispatch CommitDiff initial: %v", err)
	}
	if !strings.Contains(d.Content, "hello world") {
		t.Errorf("CLI fallback initial commit missing content; got:\n%s", d.Content)
	}
}

func TestGoGitWorkingTreeOpsReturnUnsupported(t *testing.T) {
	f := newSimpleRepo(t)
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func() (Diff, error){
		"StagedDiff":   g.StagedDiff,
		"UnstagedDiff": g.UnstagedDiff,
		"FileDiff":     func() (Diff, error) { return g.FileDiff("hello.txt") },
	}
	for name, fn := range cases {
		_, err := fn()
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s err = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestGoGitNotARepo(t *testing.T) {
	tmp := t.TempDir()
	_, err := NewGoGitRepo(tmp)
	if !errors.Is(err, ErrNotARepo) {
		t.Errorf("err = %v, want ErrNotARepo", err)
	}
}

func TestCLIStagedDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	mustWrite(t, f.dir, "staged.txt", "staged content\n")
	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	// Stage the new file via CLI
	if _, err := r.run("add", "staged.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	d, err := r.StagedDiff()
	if err != nil {
		t.Fatalf("StagedDiff: %v", err)
	}
	if d.Origin != OriginStaged {
		t.Errorf("Origin = %q", d.Origin)
	}
	if !strings.Contains(d.Content, "staged content") {
		t.Errorf("StagedDiff content missing expected line; got:\n%s", d.Content)
	}
}

func TestCLIUnstagedDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	mustWrite(t, f.dir, "hello.txt", "hello world\nhello again\nhello unstaged\n")
	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.UnstagedDiff()
	if err != nil {
		t.Fatalf("UnstagedDiff: %v", err)
	}
	if !strings.Contains(d.Content, "hello unstaged") {
		t.Errorf("UnstagedDiff missing expected line; got:\n%s", d.Content)
	}
}

func TestCLICommitDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.CommitDiff(f.mainCommit.String())
	if err != nil {
		t.Fatalf("CommitDiff: %v", err)
	}
	if !strings.Contains(d.Content, "hello again") {
		t.Errorf("CLI CommitDiff missing expected line; got:\n%s", d.Content)
	}
}

func TestDispatchPrefersPrimaryFallsToFallback(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// CommitDiff: should go via go-git (primary)
	d, err := repo.CommitDiff(f.mainCommit.String())
	if err != nil {
		t.Fatalf("CommitDiff: %v", err)
	}
	if d.Origin != OriginCommit {
		t.Error("CommitDiff origin wrong")
	}

	// StagedDiff: go-git returns ErrUnsupported → falls to CLI
	mustWrite(t, f.dir, "new.txt", "new file\n")
	cli, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.run("add", "new.txt"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	staged, err := repo.StagedDiff()
	if err != nil {
		t.Fatalf("StagedDiff via dispatch: %v", err)
	}
	if !strings.Contains(staged.Content, "new file") {
		t.Errorf("StagedDiff via fallback missing content; got:\n%s", staged.Content)
	}
}

func TestDispatchOpenNotARepo(t *testing.T) {
	tmp := t.TempDir()
	_, err := Open(tmp, DispatchOptions{})
	if !errors.Is(err, ErrNotARepo) {
		t.Errorf("err = %v, want ErrNotARepo", err)
	}
}

func TestDispatchRoot(t *testing.T) {
	f := newSimpleRepo(t)
	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if repo.Root() != f.dir {
		t.Errorf("Root() = %q, want %q", repo.Root(), f.dir)
	}
}

func TestDiffOrigin(t *testing.T) {
	d := Diff{Content: "patch", Origin: OriginCommit}
	if d.Empty() {
		t.Error("Empty() = true for non-empty content")
	}
	empty := Diff{}
	if !empty.Empty() {
		t.Error("Empty() = false for zero Diff")
	}
}

func TestGoGitRangeDiff(t *testing.T) {
	f := newSimpleRepo(t)
	featureHash := f.createFeatureBranch(t, "feature")
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	// main: A→B (mainCommit), feature: A→C (featureHash); merge base = A.
	// RangeDiff(main, feature) should show feature's additions over the
	// merge base — i.e., extra.txt added.
	d, err := g.RangeDiff(f.mainCommit.String(), featureHash.String())
	if err != nil {
		t.Fatalf("RangeDiff: %v", err)
	}
	if d.Origin != OriginRange {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginRange)
	}
	if !strings.Contains(d.Content, "extra.txt") {
		t.Errorf("RangeDiff missing 'extra.txt'; got:\n%s", d.Content)
	}
	if !strings.Contains(d.Content, "extra content") {
		t.Errorf("RangeDiff missing added content; got:\n%s", d.Content)
	}
}

func TestGoGitBranchDiff(t *testing.T) {
	f := newSimpleRepo(t)
	// HEAD is currently on mainCommit (the last commit returned by newSimpleRepo).
	// Use the first commit as the target — BranchDiff should show changes
	// from first → HEAD (i.e., "hello again" added).
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := g.BranchDiff(f.firstCommit.String())
	if err != nil {
		t.Fatalf("BranchDiff: %v", err)
	}
	if d.Origin != OriginBranch {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginBranch)
	}
	if !strings.Contains(d.Content, "hello again") {
		t.Errorf("BranchDiff missing expected line; got:\n%s", d.Content)
	}
}

func TestRenameDefaultBranchToMain(t *testing.T) {
	f := newSimpleRepo(t)
	f.renameDefaultBranchToMain(t)
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	// Resolving "main" should now succeed.
	d, err := g.BranchDiff(f.firstCommit.String())
	if err != nil {
		t.Fatalf("BranchDiff after rename: %v", err)
	}
	if d.Empty() {
		t.Error("expected non-empty diff after branch rename")
	}
}

func TestCheckoutMainHelper(t *testing.T) {
	f := newSimpleRepo(t)
	_ = f.createFeatureBranch(t, "feature")
	// Worktree is now on feature with extra.txt present.
	if _, err := os.Stat(filepath.Join(f.dir, "extra.txt")); err != nil {
		t.Fatalf("expected extra.txt on feature branch: %v", err)
	}
	f.checkoutMain(t)
	// After checkout to main, extra.txt should be gone.
	if _, err := os.Stat(filepath.Join(f.dir, "extra.txt")); !os.IsNotExist(err) {
		t.Errorf("extra.txt should be absent on main; stat err = %v", err)
	}
}

// ---------- Dispatcher CLI-fallback coverage ----------
//
// The dispatcher prefers go-git; methods that the go-git backend returns
// ErrUnsupported for (worktree operations, initial-commit CommitDiff)
// must transparently fall through to CLIRepo. These tests verify each
// fallback path end-to-end.

func TestDispatchUnstagedFallsToCLI(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	mustWrite(t, f.dir, "hello.txt", "hello world\nhello again\nhello unstaged tail\n")

	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.UnstagedDiff()
	if err != nil {
		t.Fatalf("UnstagedDiff via dispatch: %v", err)
	}
	if d.Origin != OriginUnstaged {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginUnstaged)
	}
	if !strings.Contains(d.Content, "hello unstaged tail") {
		t.Errorf("dispatch UnstagedDiff missing edit; got:\n%s", d.Content)
	}
}

func TestDispatchFileDiffFallsToCLI(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	mustWrite(t, f.dir, "hello.txt", "hello world\nhello again\nfile-diff-edit\n")

	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.FileDiff("hello.txt")
	if err != nil {
		t.Fatalf("FileDiff via dispatch: %v", err)
	}
	if d.Origin != OriginFile {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginFile)
	}
	if !strings.Contains(d.Content, "file-diff-edit") {
		t.Errorf("dispatch FileDiff missing edit; got:\n%s", d.Content)
	}
}

func TestDispatchUsesGoGitForCommitDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)

	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.CommitDiff(f.mainCommit.String())
	if err != nil {
		t.Fatalf("dispatch CommitDiff: %v", err)
	}
	if !strings.Contains(d.Content, "hello again") {
		t.Errorf("dispatch CommitDiff missing expected line; got:\n%s", d.Content)
	}
}

func TestDispatchRangeDiffViaGoGit(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	feature := f.createFeatureBranch(t, "feature")

	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.RangeDiff(f.mainCommit.String(), feature.String())
	if err != nil {
		t.Fatalf("dispatch RangeDiff: %v", err)
	}
	if !strings.Contains(d.Content, "extra.txt") {
		t.Errorf("RangeDiff missing extra.txt; got:\n%s", d.Content)
	}
}

func TestDispatchBranchDiffViaGoGit(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)

	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.BranchDiff(f.firstCommit.String())
	if err != nil {
		t.Fatalf("dispatch BranchDiff: %v", err)
	}
	if !strings.Contains(d.Content, "hello again") {
		t.Errorf("BranchDiff missing expected line; got:\n%s", d.Content)
	}
}

// ---------- CLIRepo direct (additional methods) ----------

func TestCLIFileDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	mustWrite(t, f.dir, "hello.txt", "hello world\nhello again\nedit-via-cli-file\n")

	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.FileDiff("hello.txt")
	if err != nil {
		t.Fatalf("CLI FileDiff: %v", err)
	}
	if !strings.Contains(d.Content, "edit-via-cli-file") {
		t.Errorf("FileDiff missing edit; got:\n%s", d.Content)
	}
}

func TestCLIRangeDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	feature := f.createFeatureBranch(t, "feature")

	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.RangeDiff(f.mainCommit.String(), feature.String())
	if err != nil {
		t.Fatalf("CLI RangeDiff: %v", err)
	}
	if d.Origin != OriginRange {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginRange)
	}
	if !strings.Contains(d.Content, "extra.txt") {
		t.Errorf("CLI RangeDiff missing extra.txt; got:\n%s", d.Content)
	}
}

func TestCLIBranchDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)

	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	d, err := r.BranchDiff(f.firstCommit.String())
	if err != nil {
		t.Fatalf("CLI BranchDiff: %v", err)
	}
	if d.Origin != OriginBranch {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginBranch)
	}
}

func TestCLIArgValidation(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	r, err := NewCLIRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		call func() (Diff, error)
	}{
		{"FileDiff empty", func() (Diff, error) { return r.FileDiff("") }},
		{"CommitDiff empty", func() (Diff, error) { return r.CommitDiff("") }},
		{"RangeDiff missing", func() (Diff, error) { return r.RangeDiff("", "main") }},
		{"BranchDiff empty", func() (Diff, error) { return r.BranchDiff("") }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.call(); err == nil {
				t.Error("expected validation error for empty argument")
			}
		})
	}
}

// Ensure DispatchRepo satisfies Repo
var _ Repo = (*DispatchRepo)(nil)
var _ Repo = (*CLIRepo)(nil)
var _ Repo = (*GoGitRepo)(nil)
