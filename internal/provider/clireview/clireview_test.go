// SPDX-License-Identifier: GPL-3.0-or-later

package clireview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// scriptPath stages a temporary shell script that mimics a one-shot
// CLI invocation: it accepts whatever args we hand it and writes a
// canned response to stdout. The caller picks the binary name (e.g.
// "fake-cli"), so we can also assert PATH lookup behavior. Skip on
// Windows where shebangs aren't honored — these tests are POSIX-only,
// like the existing git CLI tests in internal/git.
func scriptPath(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script mock not supported on Windows; clireview is exercised by integration tests on POSIX")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Prepend the script's dir to PATH so exec.LookPath finds it.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return path
}

func TestBackendEmitsPlainTextMarker(t *testing.T) {
	// Compile-time + runtime assertion: Backend implements
	// provider.PlainTextEmitter so review.go can detect CLI providers.
	var _ provider.PlainTextEmitter = (*Backend)(nil)
	b := New(Spec{Name: "test-cli", Binary: "irrelevant"})
	if _, ok := any(b).(provider.PlainTextEmitter); !ok {
		t.Error("Backend should satisfy provider.PlainTextEmitter")
	}
}

func TestBackendNameAndPricing(t *testing.T) {
	b := New(Spec{Name: "test-cli", Binary: "x"})
	if b.Name() != "test-cli" {
		t.Errorf("Name = %q, want %q", b.Name(), "test-cli")
	}
	p := b.Pricing("any")
	if p.InputPer1M != 0 || p.OutputPer1M != 0 || p.CachedInputPer1M != 0 {
		t.Errorf("Pricing should be zero for CLI providers; got %+v", p)
	}
}

func TestBackendReviewStreamsStdoutToContent(t *testing.T) {
	// Happy path: the fake binary echoes a canned review back. We
	// confirm Content captures stdout exactly (trimmed of trailing
	// whitespace) and Usage stays zeroed.
	const expected = "💥 [CRITICAL] · app.go:1\n\nProblem\n\nDetails"
	scriptPath(t, "fake-cli", "printf '%s' \""+expected+"\"")

	b := New(Spec{
		Name:   "fake-cli",
		Binary: "fake-cli",
		PromptArgs: func(prompt string) []string {
			return []string{"-p", prompt}
		},
		Timeout: 5 * time.Second,
	})
	resp, err := b.Review(context.Background(), provider.Request{
		SystemPrompt: "rules",
		UserPrompt:   "review this",
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != expected {
		t.Errorf("Content = %q, want %q", resp.Content, expected)
	}
	if resp.Usage.InputTokens != 0 || resp.Usage.OutputTokens != 0 {
		t.Errorf("Usage should be zero for CLI providers; got %+v", resp.Usage)
	}
}

func TestBackendReviewSurfacesNonZeroExit(t *testing.T) {
	// Non-zero exit from the host CLI should bubble up as an error
	// containing whatever the host wrote to stderr (e.g. "401
	// unauthorized" or "rate limit exceeded").
	scriptPath(t, "fake-cli", "echo 'auth: not logged in' >&2; exit 1")

	b := New(Spec{
		Name:       "fake-cli",
		Binary:     "fake-cli",
		PromptArgs: func(p string) []string { return []string{"-p", p} },
		Timeout:    5 * time.Second,
	})
	_, err := b.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err == nil {
		t.Fatal("Review should error on non-zero exit; got nil")
	}
	if !strings.Contains(err.Error(), "auth: not logged in") {
		t.Errorf("error should surface stderr; got: %v", err)
	}
}

func TestBackendReviewEmptyOutputIsError(t *testing.T) {
	// CLI exits zero but writes nothing — probably the host CLI
	// silently dropped the request (rate limit pause, etc.). Treat
	// as an error rather than show an empty review.
	scriptPath(t, "fake-cli", "exit 0")

	b := New(Spec{
		Name:       "fake-cli",
		Binary:     "fake-cli",
		PromptArgs: func(p string) []string { return []string{"-p", p} },
		Timeout:    5 * time.Second,
	})
	_, err := b.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err == nil {
		t.Fatal("empty output should produce an error; got nil")
	}
	if !strings.Contains(err.Error(), "empty output") {
		t.Errorf("error should mention empty output; got: %v", err)
	}
}

func TestBackendReviewRespectsContextCancel(t *testing.T) {
	// A slow CLI + cancelled context should return quickly. Without
	// the cancel propagation we'd block on the subprocess.
	scriptPath(t, "fake-cli", "sleep 5; echo done")

	b := New(Spec{
		Name:       "fake-cli",
		Binary:     "fake-cli",
		PromptArgs: func(p string) []string { return []string{p} },
		Timeout:    10 * time.Second,
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, err := b.Review(ctx, provider.Request{UserPrompt: "x"})
	if err == nil {
		t.Fatal("cancelled context should surface as an error")
	}
}

func TestBackendReviewTimeoutMessageMentionsLimit(t *testing.T) {
	// Spec.Timeout fires its own context inside Review. Surface a
	// clear "timed out after N" message rather than a generic
	// context.DeadlineExceeded so the user knows where to look.
	scriptPath(t, "fake-cli", "sleep 2")

	b := New(Spec{
		Name:       "fake-cli",
		Binary:     "fake-cli",
		PromptArgs: func(p string) []string { return []string{p} },
		Timeout:    100 * time.Millisecond,
	})
	_, err := b.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout message; got: %v", err)
	}
}

func TestBackendTestConnectionMissingBinary(t *testing.T) {
	// Empty PATH → exec.LookPath fails → TestConnection returns a
	// translatable "binary not found" error so doctor / providers
	// test can surface it cleanly.
	t.Setenv("PATH", "")
	b := New(Spec{Name: "ghost-cli", Binary: "definitely-not-here-12345"})
	err := b.TestConnection(context.Background())
	if err == nil {
		t.Fatal("expected error for missing binary; got nil")
	}
	if !strings.Contains(err.Error(), "not found on PATH") {
		t.Errorf("error should mention PATH lookup; got: %v", err)
	}
}

func TestBackendTestConnectionSuccess(t *testing.T) {
	// Working binary + working version command → TestConnection
	// returns nil.
	scriptPath(t, "fake-cli", "echo 1.2.3")
	b := New(Spec{
		Name:        "fake-cli",
		Binary:      "fake-cli",
		VersionArgs: []string{"--version"},
	})
	if err := b.TestConnection(context.Background()); err != nil {
		t.Errorf("TestConnection: %v", err)
	}
}

func TestBackendDefaultModelIncludesVersion(t *testing.T) {
	// The cache key includes the model identifier; for CLI providers
	// we encode the version so cache entries invalidate cleanly
	// across host-CLI upgrades.
	scriptPath(t, "fake-cli", "echo 'fake-cli version 9.9.9'")
	b := New(Spec{
		Name:        "fake-cli",
		Binary:      "fake-cli",
		VersionArgs: []string{"--version"},
	})
	got := b.DefaultModel()
	if !strings.Contains(got, "9.9.9") {
		t.Errorf("DefaultModel = %q, want it to include the binary version", got)
	}
}

func TestBackendNoPromptArgsErrors(t *testing.T) {
	// A misconfigured Spec (no PromptArgs) is a programmer error.
	// Fail loudly at Review-time rather than passing empty argv.
	b := New(Spec{Name: "x-cli", Binary: "x"})
	_, err := b.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err == nil {
		t.Fatal("missing PromptArgs should error")
	}
	if !strings.Contains(err.Error(), "PromptArgs is required") {
		t.Errorf("error should mention PromptArgs; got: %v", err)
	}
}

func TestBackendReviewCombinesSystemAndUserPrompts(t *testing.T) {
	// The CLI gets ONE prompt — we concatenate system+user with a
	// paragraph break so the model sees both halves. Pin the joiner
	// behaviour so a refactor doesn't silently drop one side.
	scriptPath(t, "fake-cli",
		// Echo the first positional arg back, so the test can read it.
		"printf '%s' \"$2\"")

	b := New(Spec{
		Name:       "fake-cli",
		Binary:     "fake-cli",
		PromptArgs: func(p string) []string { return []string{"-p", p} },
		Timeout:    5 * time.Second,
	})
	resp, err := b.Review(context.Background(), provider.Request{
		SystemPrompt: "RULES",
		UserPrompt:   "DIFF",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "RULES\n\nDIFF"
	if resp.Content != want {
		t.Errorf("combined prompt = %q, want %q", resp.Content, want)
	}
}

func TestBackendReviewUseStdinPipesPromptViaStdin(t *testing.T) {
	// UC-24 regression guard. With UseStdin=true the prompt must come
	// in on the subprocess's stdin and NOT appear in argv. The fake
	// CLI prints `<argv> :: <stdin>` so we can confirm both halves.
	scriptPath(t, "stdin-cli",
		"printf 'argv=%s :: stdin=' \"$*\"; cat -")

	b := New(Spec{
		Name:   "stdin-cli",
		Binary: "stdin-cli",
		// When stdin mode is active the prompt arg is always empty —
		// adapter returns only the flag combo to read from stdin.
		PromptArgs: func(p string) []string {
			if p != "" {
				t.Errorf("PromptArgs received non-empty prompt %q under UseStdin=true", p)
			}
			return []string{"-p", "-"}
		},
		UseStdin: true,
		Timeout:  5 * time.Second,
	})
	resp, err := b.Review(context.Background(), provider.Request{
		SystemPrompt: "SYS",
		UserPrompt:   "USR",
	})
	if err != nil {
		t.Fatal(err)
	}
	// stdin half must carry the combined prompt; argv half must NOT.
	if !strings.Contains(resp.Content, "stdin=SYS\n\nUSR") {
		t.Errorf("stdin should carry combined prompt; got %q", resp.Content)
	}
	if strings.Contains(resp.Content, "argv=-p - SYS") || strings.Contains(resp.Content, "argv=-p - USR") {
		t.Errorf("argv must not include the prompt; got %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "argv=-p -") {
		t.Errorf("argv should contain the stdin-flag combo; got %q", resp.Content)
	}
}

func TestBackendDefaultModelMemoisesVersionCall(t *testing.T) {
	// UC-23 regression guard. The version subprocess must run *once*
	// per Backend even when DefaultModel is queried repeatedly — every
	// cache-key build asks for it, so the cost of re-shelling out is
	// not academic. We assert by counting probe-file lines that the
	// fake binary appends per invocation.
	dir := t.TempDir()
	probe := filepath.Join(dir, "calls.log")
	scriptPath(t, "memo-cli",
		"printf 'memo-cli 1.2.3\\n'; printf '.' >> "+probe)

	b := New(Spec{
		Name:        "memo-cli",
		Binary:      "memo-cli",
		PromptArgs:  func(p string) []string { return []string{"-p", p} },
		VersionArgs: []string{"--version"},
	})
	for i := 0; i < 5; i++ {
		got := b.DefaultModel()
		if !strings.Contains(got, "1.2.3") {
			t.Errorf("call %d: DefaultModel = %q, want it to include the version", i, got)
		}
	}
	data, err := os.ReadFile(probe)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(data); got != 1 {
		t.Errorf("version probe ran %d times; sync.Once memo should keep it at 1", got)
	}
}

// guard: keep import for fmt usage in formatted assertions
var _ = fmt.Sprint
