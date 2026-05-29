// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExpandDefault(t *testing.T) {
	cases := []struct {
		name      string
		rawArgs   []string
		def       string
		wantArgs  []string
		wantApply bool
	}{
		{"bare + default expands", nil, "--unstaged --cli gemini",
			[]string{"--unstaged", "--cli", "gemini"}, true},
		{"bare + single token", []string{}, "--staged",
			[]string{"--staged"}, true},
		{"bare + empty default → unchanged", nil, "", nil, false},
		{"bare + whitespace default → unchanged", nil, "   \t ", nil, false},
		{"explicit flag bypasses default", []string{"--json"}, "--unstaged",
			[]string{"--json"}, false},
		{"explicit subcommand bypasses default", []string{"dry-run"}, "--unstaged",
			[]string{"dry-run"}, false},
		{"extra whitespace in default is collapsed", nil, "  --unstaged    --verbose ",
			[]string{"--unstaged", "--verbose"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, apply := expandDefault(tc.rawArgs, tc.def)
			if apply != tc.wantApply {
				t.Errorf("apply = %v, want %v", apply, tc.wantApply)
			}
			if apply && !reflect.DeepEqual(got, tc.wantArgs) {
				t.Errorf("args = %v, want %v", got, tc.wantArgs)
			}
			if !apply && !reflect.DeepEqual(got, tc.rawArgs) {
				t.Errorf("non-apply must return rawArgs unchanged; got %v, want %v", got, tc.rawArgs)
			}
		})
	}
}

// TestLoadDefaultCommandFromConfig wires loadDefaultCommand against a real
// config file via $COMMITBRIEF_CONFIG, confirming the command.default
// field is read end-to-end (load + field access).
func TestLoadDefaultCommandFromConfig(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(cfgPath, []byte("command:\n  default: \"--unstaged --cli gemini\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("COMMITBRIEF_CONFIG", cfgPath)

	if got := loadDefaultCommand(); got != "--unstaged --cli gemini" {
		t.Errorf("loadDefaultCommand() = %q, want %q", got, "--unstaged --cli gemini")
	}
}

// TestLoadDefaultCommandEmptyWhenUnset: a config without command.default
// (here, a missing file) yields "" so the bare invocation keeps the
// built-in --staged behavior.
func TestLoadDefaultCommandEmptyWhenUnset(t *testing.T) {
	t.Setenv("COMMITBRIEF_CONFIG", filepath.Join(t.TempDir(), "does-not-exist.yml"))
	if got := loadDefaultCommand(); got != "" {
		t.Errorf("loadDefaultCommand() with no config = %q, want empty", got)
	}
}
