// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func findSubcommand(root *cobra.Command, name string) *cobra.Command {
	for _, c := range root.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func TestUpgradeCommandRegistered(t *testing.T) {
	root := newRootCmd()
	cmd := findSubcommand(root, "upgrade")
	if cmd == nil {
		t.Fatal("upgrade command is not registered on root")
		return
	}
	if cmd.Flags().Lookup("check") == nil {
		t.Fatal("--check flag is missing")
	}
	if cmd.Args == nil {
		t.Fatal("upgrade should reject positional arguments")
	}
}

func TestUpgradeCommandRejectsArgs(t *testing.T) {
	cmd := newUpgradeCmd()
	if err := cmd.Args(cmd, []string{"v1.2.3"}); err == nil {
		t.Fatal("Args() error = nil, want a rejection of positional arguments")
	}
}

func TestUpgradeReportJSON(t *testing.T) {
	rep := upgradeReport{
		Current:         "v1.14.0",
		Latest:          "v1.15.0",
		Method:          "homebrew",
		UpdateAvailable: true,
		Action:          "brew upgrade commitbrief",
	}
	var sb strings.Builder
	if err := writeUpgradeJSON(&sb, rep); err != nil {
		t.Fatalf("writeUpgradeJSON() error = %v", err)
	}
	out := sb.String()
	for _, want := range []string{`"current": "v1.14.0"`, `"latest": "v1.15.0"`, `"method": "homebrew"`, `"update_available": true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON output missing %s:\n%s", want, out)
		}
	}
}

// TestUpgradeReportJSONDevBuild pins the shape emitted for a build whose
// version cannot be parsed. --json must still produce a report there:
// empty stdout with exit 0 gives a parsing script no way to tell "no
// update available" from "the command did nothing".
func TestUpgradeReportJSONDevBuild(t *testing.T) {
	rep := upgradeReport{
		Current:         "dev",
		Method:          "manual",
		UpdateAvailable: false,
	}
	var sb strings.Builder
	if err := writeUpgradeJSON(&sb, rep); err != nil {
		t.Fatalf("writeUpgradeJSON() error = %v", err)
	}
	out := sb.String()
	if strings.TrimSpace(out) == "" {
		t.Fatal("dev-build report produced empty output")
	}
	for _, want := range []string{`"current": "dev"`, `"method": "manual"`, `"update_available": false`} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON output missing %s:\n%s", want, out)
		}
	}
}
