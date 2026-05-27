// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestEmitPlainTextHonorsOutputFlag(t *testing.T) {
	// UC-07 regression guard. Pre-v0.9.2 the CLI-provider path wrote
	// directly to cmd.OutOrStdout() regardless of --output, so users
	// piping `commitbrief --cli claude --output review.md` watched
	// the response land on stdout while review.md stayed empty.
	// emitPlainText now routes through openOutput so both branches
	// (structured renderers and plain-text emit) share the file-
	// destination plumbing.
	resetGlobalFlags(t)
	dir := t.TempDir()
	outPath := filepath.Join(dir, "review.md")
	global.output = outPath

	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := emitPlainText(cmd, "## Reviewed\n\nNo critical findings.\n"); err != nil {
		t.Fatalf("emitPlainText: %v", err)
	}

	// The file MUST contain the content; stdout MUST be empty.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("output file not written: %v", err)
	}
	if !strings.Contains(string(data), "Reviewed") {
		t.Errorf("output file missing content; got:\n%s", data)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout should be empty when --output is set; got:\n%s", stdout.String())
	}
}

func TestEmitPlainTextDefaultsToStdoutWhenNoOutputFlag(t *testing.T) {
	// Counter-guard: when --output is unset, the content still goes
	// to the cobra command's stdout. Catches a regression where the
	// openOutput refactor accidentally swallowed stdout output.
	resetGlobalFlags(t)
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&bytes.Buffer{})

	if err := emitPlainText(cmd, "hello"); err != nil {
		t.Fatal(err)
	}
	if got := stdout.String(); !strings.HasPrefix(got, "hello") {
		t.Errorf("stdout should carry the content; got %q", got)
	}
}
