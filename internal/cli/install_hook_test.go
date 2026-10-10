// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// hookGit runs git in dir with a hermetic identity and returns trimmed stdout.
func hookGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// TestPrePushHookReviewsPushedCommitsNotWorkingTree runs the generated
// pre-push script against a throwaway repo with a stub in place of the
// commitbrief binary. The stub materialises `git diff <args>` into a file so
// the test can assert which diff the hook would have sent for review.
func TestPrePushHookReviewsPushedCommitsNotWorkingTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pre-push hook is a POSIX sh script")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}

	repo := t.TempDir()
	hookGit(t, repo, "init", "-q")

	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("root.txt", "ROOT_MARKER\n")
	hookGit(t, repo, "add", ".")
	hookGit(t, repo, "commit", "-q", "-m", "root")
	rootSHA := hookGit(t, repo, "rev-parse", "HEAD")

	write("tip.txt", "TIP_MARKER\n")
	hookGit(t, repo, "add", ".")
	hookGit(t, repo, "commit", "-q", "-m", "tip")
	tipSHA := hookGit(t, repo, "rev-parse", "HEAD")

	// Uncommitted work that must never reach the reviewed diff.
	write("root.txt", "ROOT_MARKER\nDIRTY_MARKER\n")

	outFile := filepath.Join(t.TempDir(), "captured.diff")
	stub := filepath.Join(t.TempDir(), "cb-stub.sh")
	stubBody := "#!/bin/sh\n" +
		"# argv: diff <range> --fail-on=... \n" +
		"[ \"$1\" = diff ] || exit 2\n" +
		"echo \"RANGE=$2\" >> \"" + outFile + "\"\n" +
		"git diff --no-color \"$2\" >> \"" + outFile + "\"\n"
	if err := os.WriteFile(stub, []byte(stubBody), 0o755); err != nil {
		t.Fatal(err)
	}

	script := filepath.Join(t.TempDir(), "pre-push")
	if err := os.WriteFile(script, []byte(hookScript("pre-push", stub)), 0o755); err != nil {
		t.Fatal(err)
	}

	z40 := strings.Repeat("0", 40)
	runHook := func(stdin string) string {
		t.Helper()
		_ = os.Remove(outFile)
		cmd := exec.Command("sh", script, "origin", "https://example.invalid/r.git")
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader(stdin)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("hook failed: %v\n%s", err, out)
		}
		b, err := os.ReadFile(outFile)
		if err != nil {
			t.Fatalf("stub never ran: %v", err)
		}
		return string(b)
	}

	t.Run("new branch reviews the tip commit only", func(t *testing.T) {
		got := runHook("refs/heads/x " + tipSHA + " refs/heads/x " + z40 + "\n")
		if !strings.Contains(got, "TIP_MARKER") {
			t.Errorf("tip commit change missing from reviewed diff:\n%s", got)
		}
		if strings.Contains(got, "ROOT_MARKER") {
			t.Errorf("parent commit leaked into reviewed diff:\n%s", got)
		}
		if strings.Contains(got, "DIRTY_MARKER") {
			t.Errorf("uncommitted working-tree change leaked into reviewed diff:\n%s", got)
		}
		if !strings.Contains(got, "RANGE="+tipSHA+"^.."+tipSHA) {
			t.Errorf("unexpected range:\n%s", got)
		}
	})

	t.Run("new branch with a root commit reviews that commit", func(t *testing.T) {
		got := runHook("refs/heads/x " + rootSHA + " refs/heads/x " + z40 + "\n")
		if !strings.Contains(got, "ROOT_MARKER") {
			t.Errorf("root commit content missing from reviewed diff:\n%s", got)
		}
		if strings.Contains(got, "TIP_MARKER") || strings.Contains(got, "DIRTY_MARKER") {
			t.Errorf("diff must contain the root commit only:\n%s", got)
		}
		if !strings.Contains(got, "RANGE=4b825dc642cb6eb9a060e54bf8d69288fbee4904.."+rootSHA) {
			t.Errorf("root commit must diff against the empty tree:\n%s", got)
		}
	})

	t.Run("existing branch reviews remote..local", func(t *testing.T) {
		got := runHook("refs/heads/x " + tipSHA + " refs/heads/x " + rootSHA + "\n")
		if !strings.Contains(got, "TIP_MARKER") {
			t.Errorf("pushed commit missing from reviewed diff:\n%s", got)
		}
		if strings.Contains(got, "ROOT_MARKER") || strings.Contains(got, "DIRTY_MARKER") {
			t.Errorf("already-pushed or uncommitted content leaked:\n%s", got)
		}
		if !strings.Contains(got, "RANGE="+rootSHA+".."+tipSHA) {
			t.Errorf("unexpected range:\n%s", got)
		}
	})

	t.Run("new annotated tag reviews the tagged commit, not the whole tree", func(t *testing.T) {
		hookGit(t, repo, "tag", "-a", "v-new", "-m", "release", tipSHA)
		tagSHA := hookGit(t, repo, "rev-parse", "v-new")
		if tagSHA == tipSHA {
			t.Fatal("expected a distinct tag object")
		}
		got := runHook("refs/tags/v-new " + tagSHA + " refs/tags/v-new " + z40 + "\n")
		if !strings.Contains(got, "RANGE="+tipSHA+"^.."+tipSHA) {
			t.Errorf("tag must be peeled to its commit; got:\n%s", got)
		}
		if !strings.Contains(got, "TIP_MARKER") || strings.Contains(got, "ROOT_MARKER") {
			t.Errorf("only the tagged commit's change may be reviewed:\n%s", got)
		}
	})

	t.Run("new annotated tag on a root commit diffs against the empty tree", func(t *testing.T) {
		hookGit(t, repo, "tag", "-a", "v-root", "-m", "first", rootSHA)
		tagSHA := hookGit(t, repo, "rev-parse", "v-root")
		got := runHook("refs/tags/v-root " + tagSHA + " refs/tags/v-root " + z40 + "\n")
		if !strings.Contains(got, "RANGE=4b825dc642cb6eb9a060e54bf8d69288fbee4904.."+rootSHA) {
			t.Errorf("root-commit tag must be peeled and diffed against the empty tree:\n%s", got)
		}
	})

	t.Run("existing annotated tag reviews old..new commit", func(t *testing.T) {
		hookGit(t, repo, "tag", "-a", "v-old", "-m", "old", rootSHA)
		hookGit(t, repo, "tag", "-a", "v-mv", "-m", "new", tipSHA)
		oldTag := hookGit(t, repo, "rev-parse", "v-old")
		newTag := hookGit(t, repo, "rev-parse", "v-mv")
		got := runHook("refs/tags/v-mv " + newTag + " refs/tags/v-mv " + oldTag + "\n")
		if !strings.Contains(got, "RANGE="+oldTag+".."+tipSHA) {
			t.Errorf("local tag must be peeled to its commit:\n%s", got)
		}
		if !strings.Contains(got, "TIP_MARKER") || strings.Contains(got, "DIRTY_MARKER") {
			t.Errorf("unexpected reviewed diff:\n%s", got)
		}
	})

	t.Run("tag that does not point at a commit is skipped", func(t *testing.T) {
		treeSHA := hookGit(t, repo, "rev-parse", tipSHA+"^{tree}")
		hookGit(t, repo, "tag", "-a", "v-tree", "-m", "tree", treeSHA)
		tagSHA := hookGit(t, repo, "rev-parse", "v-tree")
		_ = os.Remove(outFile)
		cmd := exec.Command("sh", script, "origin", "https://example.invalid/r.git")
		cmd.Dir = repo
		cmd.Stdin = strings.NewReader("refs/tags/v-tree " + tagSHA + " refs/tags/v-tree " + z40 + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook must not block the push: %v\n%s", err, out)
		}
		if _, statErr := os.Stat(outFile); statErr == nil {
			t.Errorf("non-commit tag must not be reviewed")
		}
		if !strings.Contains(string(out), "does not point at a commit") {
			t.Errorf("expected a skip warning; got:\n%s", out)
		}
	})

	t.Run("shallow clone boundary is skipped with a warning", func(t *testing.T) {
		// A depth-1 clone keeps the tip's "parent" header but not the parent
		// object. The hook must not fall back to the empty tree (which would
		// send the whole repository); it warns on stderr and skips the ref.
		shallow := filepath.Join(t.TempDir(), "shallow")
		hookGit(t, repo, "clone", "-q", "--depth", "1", "file://"+repo, shallow)
		if hookGit(t, shallow, "rev-parse", "--is-shallow-repository") != "true" {
			t.Skip("git did not produce a shallow clone")
		}
		_ = os.Remove(outFile)
		cmd := exec.Command("sh", script, "origin", "https://example.invalid/r.git")
		cmd.Dir = shallow
		cmd.Stdin = strings.NewReader("refs/heads/x " + tipSHA + " refs/heads/x " + z40 + "\n")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("hook must not block the push: %v\n%s", err, out)
		}
		if _, statErr := os.Stat(outFile); statErr == nil {
			b, _ := os.ReadFile(outFile)
			t.Errorf("review must be skipped on a shallow boundary, but the stub ran:\n%s", b)
		}
		if !strings.Contains(string(out), "shallow clone") {
			t.Errorf("expected a shallow-clone warning on stderr; got:\n%s", out)
		}
	})
}
