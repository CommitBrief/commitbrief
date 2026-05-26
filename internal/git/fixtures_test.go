package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type fixture struct {
	dir         string
	repo        *gogit.Repository
	firstCommit plumbing.Hash
	mainCommit  plumbing.Hash
}

func mustWrite(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func mustCommit(t *testing.T, w *gogit.Worktree, paths []string, msg string) plumbing.Hash {
	t.Helper()
	for _, p := range paths {
		if _, err := w.Add(p); err != nil {
			t.Fatalf("add %s: %v", p, err)
		}
	}
	hash, err := w.Commit(msg, &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@example.com",
			When:  time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC),
		},
	})
	if err != nil {
		t.Fatalf("commit: %v", err)
	}
	return hash
}

// newSimpleRepo creates a repo with two commits on main and a feature branch
// diverged from the first commit:
//
//	main:    A — B (mainCommit; updates hello.txt)
//	feature: A — C (featureCommit; adds extra.txt)
//	             \— mergeBase = A (firstCommit)
func newSimpleRepo(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	w, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	mustWrite(t, dir, "hello.txt", "hello world\n")
	first := mustCommit(t, w, []string{"hello.txt"}, "initial")
	mustWrite(t, dir, "hello.txt", "hello world\nhello again\n")
	main := mustCommit(t, w, []string{"hello.txt"}, "update hello")
	return &fixture{
		dir:         dir,
		repo:        repo,
		firstCommit: first,
		mainCommit:  main,
	}
}

func (f *fixture) createFeatureBranch(t *testing.T, name string) plumbing.Hash {
	t.Helper()
	w, err := f.repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := w.Checkout(&gogit.CheckoutOptions{
		Hash:   f.firstCommit,
		Create: true,
		Branch: plumbing.NewBranchReferenceName(name),
	}); err != nil {
		t.Fatalf("checkout feature: %v", err)
	}
	mustWrite(t, f.dir, "extra.txt", "extra content\n")
	return mustCommit(t, w, []string{"extra.txt"}, "add extra")
}

func (f *fixture) checkoutMain(t *testing.T) {
	t.Helper()
	w, err := f.repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := w.Checkout(&gogit.CheckoutOptions{
		Hash: f.mainCommit,
	}); err != nil {
		t.Fatalf("checkout main: %v", err)
	}
}

// configureMainBranch ensures HEAD is on `main` instead of go-git's default
// `master`, matching the conventions assumed by modern test repos and CI.
func (f *fixture) renameDefaultBranchToMain(t *testing.T) {
	t.Helper()
	if err := f.repo.Storer.SetReference(plumbing.NewHashReference(
		plumbing.NewBranchReferenceName("main"), f.mainCommit,
	)); err != nil {
		t.Fatalf("set main ref: %v", err)
	}
	if err := f.repo.Storer.SetReference(plumbing.NewSymbolicReference(
		plumbing.HEAD, plumbing.NewBranchReferenceName("main"),
	)); err != nil {
		t.Fatalf("set HEAD: %v", err)
	}
	cfg, err := f.repo.Config()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	cfg.Branches["main"] = &config.Branch{Name: "main"}
	if err := f.repo.SetConfig(cfg); err != nil {
		t.Fatalf("setconfig: %v", err)
	}
}

func requireGitCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping CLI integration test")
	}
}
