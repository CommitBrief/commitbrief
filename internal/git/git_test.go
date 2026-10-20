// SPDX-License-Identifier: GPL-3.0-or-later

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

func TestGoGitWorkingTreeOpsReturnUnsupported(t *testing.T) {
	f := newSimpleRepo(t)
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]func() (Diff, error){
		"StagedDiff":   g.StagedDiff,
		"UnstagedDiff": g.UnstagedDiff,
		"Diff":         func() (Diff, error) { return g.Diff([]string{"HEAD"}) },
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

func TestDispatchPrefersPrimaryFallsToFallback(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
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

func TestRenameDefaultBranchToMain(t *testing.T) {
	f := newSimpleRepo(t)
	f.renameDefaultBranchToMain(t)
	g, err := NewGoGitRepo(f.dir)
	if err != nil {
		t.Fatal(err)
	}
	// Resolving "main" should now succeed and point at the tip commit.
	ref, err := g.repo.Reference("refs/heads/main", true)
	if err != nil {
		t.Fatalf("resolve main after rename: %v", err)
	}
	if ref.Hash() != f.mainCommit {
		t.Errorf("main = %s, want %s", ref.Hash(), f.mainCommit)
	}
}

// ---------- Dispatcher CLI-fallback coverage ----------
//
// The dispatcher prefers go-git; methods that the go-git backend returns
// ErrUnsupported for (worktree operations and the Diff passthrough)
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

// ---------- CLIRepo direct (additional methods) ----------

// Ensure DispatchRepo satisfies Repo
var _ Repo = (*DispatchRepo)(nil)
var _ Repo = (*CLIRepo)(nil)
var _ Repo = (*GoGitRepo)(nil)
