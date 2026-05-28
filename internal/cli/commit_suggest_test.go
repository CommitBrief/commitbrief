// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"
	"testing"
)

// The mock provider returns mock.DefaultCommitMessage for a FreeForm
// request, so --suggest-commit prints it after the review.
func TestSuggestCommitPrintsMessageAfterReview(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--suggest-commit"); err != nil {
		t.Fatalf("review --suggest-commit: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "Suggested commit message:") {
		t.Errorf("missing suggestion header; got:\n%s", out)
	}
	if !strings.Contains(out, "feat(store): add user lookup by name") {
		t.Errorf("missing mock commit message; got:\n%s", out)
	}
	// The review itself still renders alongside the suggestion.
	if !strings.Contains(out, "mock review output") {
		t.Errorf("review output should still be present; got:\n%s", out)
	}
}

// Default (no scope flag) is the staged review, so --suggest-commit works
// without an explicit --staged.
func TestSuggestCommitWorksWithDefaultScope(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--suggest-commit"); err != nil {
		t.Fatalf("--suggest-commit with default (staged) scope should work: %v", err)
	}
	if !strings.Contains(e.out.String(), "Suggested commit message:") {
		t.Error("expected a suggestion with the default staged scope")
	}
}

func TestSuggestCommitRejectsJSON(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--staged", "--suggest-commit", "--json"); err == nil {
		t.Fatal("--suggest-commit with --json must error (output conflict)")
	}
}

func TestSuggestCommitRejectsUnstaged(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("--unstaged", "--suggest-commit"); err == nil {
		t.Fatal("--suggest-commit with --unstaged must error (staged-only)")
	}
}
