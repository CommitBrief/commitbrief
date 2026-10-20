// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type fixture struct {
	dir         string
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

// newSimpleRepo creates a repo with two commits:
//
//	A (firstCommit; adds hello.txt) — B (mainCommit; updates hello.txt)
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
		firstCommit: first,
		mainCommit:  main,
	}
}

func requireGitCLI(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping CLI integration test")
	}
}
