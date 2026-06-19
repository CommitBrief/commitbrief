// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package alias

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stubLookPath replaces the package lookPath seam for the duration of a test
// so PATH-based conflict detection is deterministic regardless of the host.
func stubLookPath(t *testing.T, found string) {
	t.Helper()
	prev := lookPath
	lookPath = func(string) (string, error) {
		if found == "" {
			return "", exec.ErrNotFound
		}
		return found, nil
	}
	t.Cleanup(func() { lookPath = prev })
}

func TestFileInstallerInstallIdempotentAndReplace(t *testing.T) {
	stubLookPath(t, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")

	inst, ok := ByName("zsh")
	if !ok {
		t.Fatal("zsh installer missing on unix")
	}
	rc := filepath.Join(home, ".zshrc")

	changed, reload, err := inst.Install("cbr")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Error("first install should change the file")
	}
	if !strings.Contains(reload, ".zshrc") {
		t.Errorf("reload hint = %q, want a source command", reload)
	}
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alias cbr='commitbrief'") {
		t.Errorf(".zshrc missing alias:\n%s", data)
	}

	// Idempotent: same alias again → no change.
	changed2, _, err := inst.Install("cbr")
	if err != nil {
		t.Fatal(err)
	}
	if changed2 {
		t.Error("re-install of identical alias should be a no-op")
	}

	// Rename: install a different alias; the old one must be removed.
	if _, _, err := inst.Install("cb"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(rc)
	if strings.Contains(string(data), "alias cbr=") {
		t.Errorf("old alias survived rename:\n%s", data)
	}
	if !strings.Contains(string(data), "alias cb='commitbrief'") {
		t.Errorf("new alias missing:\n%s", data)
	}
}

func TestFileInstallerConflictPath(t *testing.T) {
	stubLookPath(t, "/usr/local/bin/cbr")
	home := t.TempDir()
	t.Setenv("HOME", home)

	inst, _ := ByName("bash")
	reason, err := inst.Conflict("cbr")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "/usr/local/bin/cbr" {
		t.Errorf("Conflict reason = %q, want the PATH hit", reason)
	}
}

func TestFileInstallerConflictExistingAlias(t *testing.T) {
	stubLookPath(t, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")

	rc := filepath.Join(home, ".zshrc")
	if err := os.WriteFile(rc, []byte("alias cbr='something-else'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inst, _ := ByName("zsh")
	reason, err := inst.Conflict("cbr")
	if err != nil {
		t.Fatal(err)
	}
	if reason == "" {
		t.Error("expected a conflict for a pre-existing alias")
	}

	// Our own managed block must NOT be flagged as a conflict.
	if _, _, err := inst.Install("free"); err != nil {
		t.Fatal(err)
	}
	reason, err = inst.Conflict("free")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "" {
		t.Errorf("our own managed alias was flagged as a conflict: %q", reason)
	}
}

func TestDetectFromShell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/usr/bin/fish")
	inst, ok := Detect()
	if !ok || inst.Name() != "fish" {
		t.Errorf("Detect with SHELL=fish → %v, ok=%v", inst, ok)
	}

	t.Setenv("SHELL", "/some/exotic/shell")
	if _, ok := Detect(); ok {
		t.Error("Detect on an unsupported shell should return false")
	}
}

// TestWriteFileAtomicPreservesSymlink guards the fix for the dotfile-manager
// case: a symlinked rc (Stow/chezmoi/yadm) must stay a symlink, with the
// content landing on the resolved real file — not be replaced by a standalone
// file.
func TestWriteFileAtomicPreservesSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.zshrc")
	if err := os.WriteFile(real, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.zshrc")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if err := writeFileAtomic(link, []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Error("symlink was replaced by a regular file")
	}
	if data, _ := os.ReadFile(real); string(data) != "new\n" {
		t.Errorf("resolved file content = %q, want %q", data, "new\n")
	}
}

// TestWriteFileAtomicPreservesMode guards the fix that keeps a user's stricter
// mode (e.g. chmod 600) instead of resetting it to 0644 on every install.
func TestWriteFileAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "rc")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(p, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600 preserved", fi.Mode().Perm())
	}
}

// TestFishAbbrConflictAnchored guards the fix for the false-positive abbr
// match: only an abbreviation whose KEY is the name conflicts, not one that
// merely mentions it in the expansion body.
func TestFishAbbrConflictAnchored(t *testing.T) {
	stubLookPath(t, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	cfg := filepath.Join(home, ".config", "fish", "config.fish")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o755); err != nil {
		t.Fatal(err)
	}

	inst, ok := ByName("fish")
	if !ok {
		t.Fatal("fish installer missing")
	}

	// abbr whose BODY mentions cbr must NOT be a conflict.
	if err := os.WriteFile(cfg, []byte("abbr gco 'git checkout cbr'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if reason, err := inst.Conflict("cbr"); err != nil {
		t.Fatal(err)
	} else if reason != "" {
		t.Errorf("body mention of cbr should not conflict; got %q", reason)
	}

	// abbr whose KEY is cbr IS a conflict (plain and flagged forms).
	for _, line := range []string{
		"abbr cbr 'commitbrief'\n",
		"abbr -a cbr 'commitbrief'\n",
		"abbr --add cbr 'x'\n",
	} {
		if err := os.WriteFile(cfg, []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
		reason, err := inst.Conflict("cbr")
		if err != nil {
			t.Fatal(err)
		}
		if reason == "" {
			t.Errorf("abbr key conflict not detected for %q", line)
		}
	}
}
