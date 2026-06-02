// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os/exec"
	"strings"
	"testing"
)

// gitOut runs git in dir and returns its combined output, failing the test
// on error. Read-only helper for asserting repo state after a commit.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func headSubject(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "log", "-1", "--pretty=%s"))
}

func commitCount(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitOut(t, dir, "rev-list", "--count", "HEAD"))
}

// --yes commits the mock's suggestion non-interactively and reports it.
func TestCommitYesCommitsStaged(t *testing.T) {
	e := newCLIEnv(t)
	before := commitCount(t, e.repoRoot)

	if err := e.run("commit", "--yes"); err != nil {
		t.Fatalf("commit --yes: %v", err)
	}

	if after := commitCount(t, e.repoRoot); after == before {
		t.Fatalf("expected a new commit; count stayed at %s", after)
	}
	if subj := headSubject(t, e.repoRoot); subj != "feat(store): add user lookup by name" {
		t.Errorf("HEAD subject = %q, want the mock message", subj)
	}
	if out := e.out.String(); !strings.Contains(out, "Committed:") {
		t.Errorf("missing committed confirmation; got:\n%s", out)
	}
}

// No staged changes → a meaningful error, no commit.
func TestCommitNoStagedErrors(t *testing.T) {
	e := newCLIEnv(t)
	gitOut(t, e.repoRoot, "reset", "-q") // unstage the fixture change
	before := commitCount(t, e.repoRoot)

	err := e.run("commit", "--yes")
	if err == nil {
		t.Fatal("commit with nothing staged must error")
	}
	if !strings.Contains(err.Error(), "No staged changes") {
		t.Errorf("unexpected error: %v", err)
	}
	if after := commitCount(t, e.repoRoot); after != before {
		t.Errorf("no commit should have been created (count %s → %s)", before, after)
	}
}

// Non-interactive without --yes can't confirm → error before any commit.
func TestCommitNonInteractiveRequiresYes(t *testing.T) {
	e := newCLIEnv(t)
	before := commitCount(t, e.repoRoot)

	if err := e.run("commit"); err == nil {
		t.Fatal("commit without --yes on a non-TTY must error")
	}
	if after := commitCount(t, e.repoRoot); after != before {
		t.Errorf("no commit should have been created (count %s → %s)", before, after)
	}
}

func TestCommitRejectsJSON(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("commit", "--json", "--yes"); err == nil {
		t.Fatal("commit with --json must error")
	}
}

func TestCommitRejectsFileFilter(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("commit", "--file", "app.go", "--yes"); err == nil {
		t.Fatal("commit with --file must error")
	}
}

func TestCommitInvalidType(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("commit", "--type", "bogus", "--yes")
	if err == nil {
		t.Fatal("commit with an unknown --type must error")
	}
	if !strings.Contains(err.Error(), "invalid commit type") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCommitGenerateTooMany(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("commit", "--generate", "99", "--yes"); err == nil {
		t.Fatal("--generate above the cap must error")
	}
}

// --generate N with --yes generates several messages but, being
// non-interactive, commits the first one.
func TestCommitGenerateYesCommitsFirst(t *testing.T) {
	e := newCLIEnv(t)
	before := commitCount(t, e.repoRoot)

	if err := e.run("commit", "--generate", "3", "--yes"); err != nil {
		t.Fatalf("commit --generate 3 --yes: %v", err)
	}
	if after := commitCount(t, e.repoRoot); after == before {
		t.Fatal("expected a new commit from --generate --yes")
	}
	if subj := headSubject(t, e.repoRoot); subj != "feat(store): add user lookup by name" {
		t.Errorf("HEAD subject = %q, want the first mock suggestion", subj)
	}
}

// --type conventional+body keeps the multi-line body in the committed message.
func TestCommitTypePassesThrough(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("commit", "--type", "conventional+body", "--yes"); err != nil {
		t.Fatalf("commit --type conventional+body --yes: %v", err)
	}
	body := gitOut(t, e.repoRoot, "log", "-1", "--pretty=%B")
	if !strings.Contains(body, "Synthetic commit message from the mock provider.") {
		t.Errorf("body not committed; got:\n%s", body)
	}
}
