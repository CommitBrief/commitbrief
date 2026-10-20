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

func TestDispatchStagedDiff(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
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
	if staged.Origin != OriginStaged {
		t.Errorf("Origin = %q, want %q", staged.Origin, OriginStaged)
	}
	if !strings.Contains(staged.Content, "new file") {
		t.Errorf("StagedDiff missing content; got:\n%s", staged.Content)
	}
}

func TestDispatchDiffPassthrough(t *testing.T) {
	requireGitCLI(t)
	f := newSimpleRepo(t)
	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := repo.Diff([]string{f.firstCommit.String(), f.mainCommit.String()})
	if err != nil {
		t.Fatalf("Diff via dispatch: %v", err)
	}
	if d.Origin != OriginDiff {
		t.Errorf("Origin = %q, want %q", d.Origin, OriginDiff)
	}
	if !strings.Contains(d.Content, "hello again") {
		t.Errorf("Diff missing expected line; got:\n%s", d.Content)
	}
}

// Without a `git` binary the repo still opens (non-diff commands keep working)
// but every diff method reports ErrNoGitCLI.
func TestDispatchNoGitCLI(t *testing.T) {
	f := newSimpleRepo(t) // fixture is built with go-git, so no git binary needed
	t.Setenv("PATH", t.TempDir())

	repo, err := Open(f.dir, DispatchOptions{})
	if err != nil {
		t.Fatalf("Open without git on PATH: %v", err)
	}
	if repo.Root() != f.dir {
		t.Errorf("Root() = %q, want %q", repo.Root(), f.dir)
	}
	cases := map[string]func() (Diff, error){
		"StagedDiff":   repo.StagedDiff,
		"UnstagedDiff": repo.UnstagedDiff,
		"Diff":         func() (Diff, error) { return repo.Diff([]string{"HEAD"}) },
	}
	for name, fn := range cases {
		if _, err := fn(); !errors.Is(err, ErrNoGitCLI) {
			t.Errorf("%s err = %v, want ErrNoGitCLI", name, err)
		}
	}
}

func TestNewCLIRepoNoGitCLI(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := NewCLIRepo(t.TempDir()); !errors.Is(err, ErrNoGitCLI) {
		t.Errorf("err = %v, want ErrNoGitCLI", err)
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

func TestDispatchUnstagedDiff(t *testing.T) {
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
