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
	got := stdout.String()
	if !strings.Contains(got, "hello") {
		t.Errorf("stdout should carry the content; got %q", got)
	}
	// Output is bracketed top and bottom with the finding separator rule.
	if !strings.HasPrefix(got, plainTextRule) {
		t.Errorf("plain-text output should open with the %q rule; got %q", plainTextRule, got)
	}
	if !strings.HasSuffix(strings.TrimRight(got, "\n"), plainTextRule) {
		t.Errorf("plain-text output should close with the %q rule; got %q", plainTextRule, got)
	}
}

func TestWrapPlainTextBracketsAndDedupesEdges(t *testing.T) {
	// Bookend rules are added on both edges; the between-findings rule
	// the model emits is preserved untouched.
	body := "finding one\n\n" + plainTextRule + "\n\nfinding two"
	got := wrapPlainText(body)

	if !strings.HasPrefix(got, plainTextRule+"\n\n") {
		t.Errorf("missing top bracket rule; got %q", got)
	}
	if !strings.HasSuffix(got, plainTextRule+"\n\n") {
		t.Errorf("missing bottom bracket rule; got %q", got)
	}
	// Three rules total: top bracket + one between findings + bottom bracket.
	if n := strings.Count(got, plainTextRule); n != 3 {
		t.Errorf("expected 3 rules (top + between + bottom), got %d in %q", n, got)
	}

	// A stray edge rule the model emitted must not double up.
	withEdges := plainTextRule + "\n\nonly finding\n\n" + plainTextRule
	if n := strings.Count(wrapPlainText(withEdges), plainTextRule); n != 2 {
		t.Errorf("stray edge rules should be deduped to 2 (top + bottom), got %d", n)
	}
}
