// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSetupAliasExplicitName drives `commitbrief setup --alias=cb` end to end
// and asserts the alias lands in the detected shell's startup file. stdin is
// non-TTY under `go test`, so the explicit-name path runs headless.
func TestSetupAliasExplicitName(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", "")

	if err := e.run("setup", "--alias=cb"); err != nil {
		t.Fatalf("setup --alias=cb: %v", err)
	}
	rc := filepath.Join(e.homeDir, ".zshrc")
	data, err := os.ReadFile(rc)
	if err != nil {
		t.Fatalf("read %s: %v", rc, err)
	}
	if !strings.Contains(string(data), "alias cb='commitbrief'") {
		t.Errorf(".zshrc missing alias:\n%s", data)
	}
}

// TestSetupAliasInvalidName rejects an unusable alias name in the headless
// path rather than writing garbage into the startup file.
func TestSetupAliasInvalidName(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("ZDOTDIR", "")

	if err := e.run("setup", "--alias=bad name"); err == nil {
		t.Error("expected an error for an invalid alias name")
	}
}

// TestSetupAliasNonTTYNoValue errors when a bare --alias is given without a
// terminal to prompt on and without --yes.
func TestSetupAliasNonTTYNoValue(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("SHELL", "/bin/zsh")

	if err := e.run("setup", "--alias"); err == nil {
		t.Error("bare --alias on a non-TTY without --yes should error")
	}
}

// TestSetupAliasNonTTYWithYes falls back to the default name when --yes is
// passed on a non-TTY.
func TestSetupAliasNonTTYWithYes(t *testing.T) {
	e := newCLIEnv(t)
	t.Setenv("SHELL", "/bin/bash")

	if err := e.run("setup", "--alias", "--yes"); err != nil {
		t.Fatalf("setup --alias --yes: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(e.homeDir, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alias cbr='commitbrief'") {
		t.Errorf(".bashrc missing default alias:\n%s", data)
	}
}
