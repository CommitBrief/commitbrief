// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitExec(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func newCommitTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not on PATH")
	}
	dir := t.TempDir()
	gitExec(t, dir, "init", "-q", "-b", "main")
	gitExec(t, dir, "config", "user.email", "c@test")
	gitExec(t, dir, "config", "user.name", "c")
	gitExec(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitExec(t, dir, "add", "f.txt")
	return dir
}

func TestCommitWritesMultiLineMessage(t *testing.T) {
	dir := newCommitTestRepo(t)
	msg := "feat: add f\n\nA body paragraph that\nspans multiple lines."

	summary, err := Commit(context.Background(), dir, msg)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if !strings.Contains(summary, "feat: add f") {
		t.Errorf("summary missing subject: %q", summary)
	}
	got := strings.TrimRight(gitExec(t, dir, "log", "-1", "--pretty=%B"), "\n")
	if got != msg {
		t.Errorf("committed body = %q, want %q", got, msg)
	}
}

func TestCommitRejectsEmptyMessage(t *testing.T) {
	dir := newCommitTestRepo(t)
	if _, err := Commit(context.Background(), dir, "   \n  "); err == nil {
		t.Fatal("empty message must error")
	}
}

func TestCommitSurfacesGitError(t *testing.T) {
	dir := newCommitTestRepo(t)
	// First commit succeeds.
	if _, err := Commit(context.Background(), dir, "init f"); err != nil {
		t.Fatalf("first commit: %v", err)
	}
	// Nothing staged now → git commit fails; the error should be surfaced.
	if _, err := Commit(context.Background(), dir, "nothing staged"); err == nil {
		t.Fatal("commit with empty index must error")
	}
}
