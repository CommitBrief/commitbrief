// SPDX-License-Identifier: GPL-3.0-or-later

package geminicli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/clireview"
)

func TestRegistersAsPlainTextProvider(t *testing.T) {
	p, err := provider.New(Name, config.ProviderConfig{})
	if err != nil {
		t.Fatalf("provider.New(%q): %v", Name, err)
	}
	if p.Name() != Name {
		t.Errorf("Name() = %q, want %q", p.Name(), Name)
	}
	if _, ok := p.(provider.PlainTextEmitter); !ok {
		t.Errorf("%s must implement provider.PlainTextEmitter", Name)
	}
}

// TestPromptArgsDiffOnly: without --with-context the argv is the plain
// one-shot `-p <prompt>` with no trust/approval flags — pre-ADR-0017
// behavior, where the agent only sees the prompt.
func TestPromptArgsDiffOnly(t *testing.T) {
	got := promptArgs("PROMPT", false)
	if strings.Join(got, " ") != "-p PROMPT" {
		t.Errorf("diff-only argv = %v, want [-p PROMPT]", got)
	}
}

// TestPromptArgsWithContext: context mode needs BOTH --approval-mode plan
// (read-only) AND --skip-trust (untrusted-dir gate; without it plan is
// silently downgraded and reads are blocked — confirmed by the 2026-05-29
// smoke). plan mode never grants writes.
func TestPromptArgsWithContext(t *testing.T) {
	joined := strings.Join(promptArgs("PROMPT", true), " ")
	for _, want := range []string{"--approval-mode plan", "--skip-trust", "-p PROMPT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("context argv missing %q; got %q", want, joined)
		}
	}
	for _, write := range []string{"yolo", "auto_edit"} {
		if strings.Contains(joined, write) {
			t.Errorf("context argv must not enable write-capable mode %q; got %q", write, joined)
		}
	}
}

// fakeGemini stages a `gemini` stand-in on PATH that records any invocation
// by touching a marker file, so a test can prove the binary was (not) run.
// The real gemini binary is never executed.
func fakeGemini(t *testing.T) (marker string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fake not supported on Windows")
	}
	dir := t.TempDir()
	marker = filepath.Join(dir, "ran")
	body := fmt.Sprintf("#!/bin/sh\ntouch %q\necho review-ok\n", marker)
	if err := os.WriteFile(filepath.Join(dir, binaryName), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return marker
}

// TestOversizedDiffFailsBeforeExec: a >128 KB prompt cannot travel in argv;
// the provider must return the actionable ARG_MAX diagnostic without ever
// starting `gemini`.
func TestOversizedDiffFailsBeforeExec(t *testing.T) {
	marker := fakeGemini(t)
	p, err := provider.New(Name, config.ProviderConfig{})
	if err != nil {
		t.Fatal(err)
	}
	huge := strings.Repeat("+ added line of a very large diff\n", 6000) // ~200 KB
	if len(huge) <= 128*1024 {
		t.Fatalf("synthetic diff too small: %d bytes", len(huge))
	}
	_, err = p.Review(context.Background(), provider.Request{
		SystemPrompt: "rules",
		UserPrompt:   huge,
	})
	if err == nil {
		t.Fatal("expected an ARG_MAX error")
	}
	if !errors.Is(err, clireview.ErrArgTooLong) {
		t.Errorf("error should match clireview.ErrArgTooLong; got %v", err)
	}
	for _, want := range []string{"gemini-cli", "KB", "--file/--dir", "claude-cli"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %s", want, err.Error())
		}
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("gemini was executed despite the preflight rejection")
	}
}

// TestPromptJustOverOurCapFailsBeforeExec: 124 KiB is over the 120 KiB cap
// but under Linux's 128 KiB MAX_ARG_STRLEN, so the kernel would accept it.
// The marker staying absent therefore proves the preflight (CheckArgSize),
// not an E2BIG from exec, stopped the call on every platform.
func TestPromptJustOverOurCapFailsBeforeExec(t *testing.T) {
	marker := fakeGemini(t)
	p, _ := provider.New(Name, config.ProviderConfig{})
	_, err := p.Review(context.Background(), provider.Request{
		UserPrompt: strings.Repeat("d", 124*1024),
	})
	if !errors.Is(err, clireview.ErrArgTooLong) {
		t.Fatalf("expected ErrArgTooLong from the preflight; got %v", err)
	}
	var atl *clireview.ArgTooLongError
	if !errors.As(err, &atl) || atl.Kind != clireview.ArgSingleTooLong {
		t.Errorf("expected the single-argument budget; got %+v", atl)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("gemini was executed; the preflight did not stop the call")
	}
}

// TestOversizedDiffMessageIsTurkishUnderTR: the diagnostic is localized.
func TestOversizedDiffMessageIsTurkishUnderTR(t *testing.T) {
	fakeGemini(t)
	p, _ := provider.New(Name, config.ProviderConfig{})
	_, err := p.Review(context.Background(), provider.Request{
		UserPrompt: strings.Repeat("d", 200*1024),
		Lang:       "tr",
	})
	if err == nil || !strings.Contains(err.Error(), "claude-cli kullanın") {
		t.Fatalf("expected the Turkish diagnostic; got %v", err)
	}
}

// TestNormalDiffStillRuns: the preflight must not reject a prompt that fits.
func TestNormalDiffStillRuns(t *testing.T) {
	marker := fakeGemini(t)
	p, _ := provider.New(Name, config.ProviderConfig{})
	resp, err := p.Review(context.Background(), provider.Request{UserPrompt: "small diff"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "review-ok" {
		t.Errorf("Content = %q, want review-ok", resp.Content)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("gemini should have run for a small prompt")
	}
}
