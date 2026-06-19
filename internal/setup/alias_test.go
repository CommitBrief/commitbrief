// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/alias"
)

func TestRunAliasNonInteractiveDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")

	out, err := RunAlias(context.Background(), AliasOptions{
		Shell:       "zsh",
		Interactive: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != alias.DefaultName {
		t.Errorf("Name = %q, want default %q", out.Name, alias.DefaultName)
	}
	if out.Shell != "zsh" {
		t.Errorf("Shell = %q, want zsh", out.Shell)
	}
	if !out.Changed {
		t.Error("first install should report Changed")
	}
	if !strings.Contains(out.ReloadCmd, ".zshrc") {
		t.Errorf("ReloadCmd = %q, want a source command", out.ReloadCmd)
	}
	data, err := os.ReadFile(filepath.Join(home, ".zshrc"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "alias cbr='commitbrief'") {
		t.Errorf(".zshrc missing alias:\n%s", data)
	}
}

func TestRunAliasNonInteractiveExplicitName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", "")

	out, err := RunAlias(context.Background(), AliasOptions{
		Shell:       "bash",
		Name:        "cb",
		Interactive: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "cb" {
		t.Errorf("Name = %q, want cb", out.Name)
	}
	data, _ := os.ReadFile(filepath.Join(home, ".bashrc"))
	if !strings.Contains(string(data), "alias cb='commitbrief'") {
		t.Errorf(".bashrc missing alias:\n%s", data)
	}
}

func TestRunAliasInvalidNameNonInteractive(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := RunAlias(context.Background(), AliasOptions{
		Shell:       "zsh",
		Name:        "bad name",
		Interactive: false,
	})
	if err == nil {
		t.Error("invalid name in non-interactive mode should error")
	}
}

func TestRunAliasUnsupportedShell(t *testing.T) {
	_, err := RunAlias(context.Background(), AliasOptions{
		Shell:       "no-such-shell",
		Interactive: false,
	})
	if err == nil {
		t.Error("unsupported shell should error")
	}
}
