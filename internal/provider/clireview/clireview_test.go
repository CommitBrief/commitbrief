// SPDX-License-Identifier: GPL-3.0-or-later

package clireview

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
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
		PromptArgs: func(prompt string, _ bool) []string {
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

func TestBackendReviewContextOptionsPlumbing(t *testing.T) {
	// Deterministic end-to-end of the --with-context plumbing (ADR-0017),
	// no model/network: a fake binary echoes its cwd + argv. With
	// ContextOptions{Enabled:true, RepoRoot}, the Backend must (1) pass
	// withContext=true to PromptArgs (so the adapter's read flags land in
	// argv) and (2) run the subprocess in RepoRoot.
	scriptPath(t, "fake-cli", `printf 'CWD=%s ARGS=%s' "$(pwd)" "$*"`)

	spec := Spec{
		Name:   "fake-cli",
		Binary: "fake-cli",
		PromptArgs: func(prompt string, withContext bool) []string {
			args := []string{"-p", prompt}
			if withContext {
				args = append(args, "--ctx-flag")
			}
			return args
		},
		Timeout: 5 * time.Second,
	}
	b := New(spec)

	repoRoot := t.TempDir()
	// The shell's `pwd` reports the logical path (matching cmd.Dir as set),
	// but on macOS /tmp symlinks to /private/tmp, so accept either form.
	resolved, _ := filepath.EvalSymlinks(repoRoot)
	ranInRepoRoot := func(content string) bool {
		return strings.Contains(content, "CWD="+repoRoot) ||
			(resolved != "" && strings.Contains(content, "CWD="+resolved))
	}

	// Context enabled: read flag present, cwd pinned to repoRoot.
	respOn, err := b.Review(context.Background(), provider.Request{
		UserPrompt:   "x",
		ProviderOpts: provider.ContextOptions{Enabled: true, RepoRoot: repoRoot},
	})
	if err != nil {
		t.Fatalf("Review (context on): %v", err)
	}
	if !strings.Contains(respOn.Content, "--ctx-flag") {
		t.Errorf("context-on argv must carry the read flag; got %q", respOn.Content)
	}
	if !ranInRepoRoot(respOn.Content) {
		t.Errorf("context-on must run in repoRoot %q; got %q", repoRoot, respOn.Content)
	}

	// Context disabled: no read flag, cwd NOT forced to repoRoot.
	respOff, err := b.Review(context.Background(), provider.Request{
		UserPrompt:   "x",
		ProviderOpts: provider.ContextOptions{Enabled: false, RepoRoot: repoRoot},
	})
	if err != nil {
		t.Fatalf("Review (context off): %v", err)
	}
	if strings.Contains(respOff.Content, "--ctx-flag") {
		t.Errorf("context-off argv must NOT carry the read flag; got %q", respOff.Content)
	}
	if ranInRepoRoot(respOff.Content) {
		t.Errorf("context-off must not pin cwd to repoRoot; got %q", respOff.Content)
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
		PromptArgs: func(p string, _ bool) []string { return []string{"-p", p} },
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
		PromptArgs: func(p string, _ bool) []string { return []string{"-p", p} },
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
		PromptArgs: func(p string, _ bool) []string { return []string{p} },
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
		PromptArgs: func(p string, _ bool) []string { return []string{p} },
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
		PromptArgs: func(p string, _ bool) []string { return []string{"-p", p} },
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
		PromptArgs: func(p string, _ bool) []string {
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
		PromptArgs:  func(p string, _ bool) []string { return []string{"-p", p} },
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

func TestBackendSetTimeoutRaisesTheSpecCap(t *testing.T) {
	// --timeout must be able to LENGTHEN a CLI invocation, not just
	// shorten it: a ctx deadline can only cut a run short, so a host CLI
	// still chewing on a large diff would die at Spec.Timeout regardless.
	scriptPath(t, "slow-cli", "sleep 0.4; echo done")

	b := New(Spec{
		Name:       "slow-cli",
		Binary:     "slow-cli",
		PromptArgs: func(p string, _ bool) []string { return []string{p} },
		Timeout:    50 * time.Millisecond,
	})
	if _, err := b.Review(context.Background(), provider.Request{UserPrompt: "x"}); err == nil {
		t.Fatal("expected the 50ms spec cap to fire before the script finished")
	}

	b.SetTimeout(5 * time.Second)
	resp, err := b.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err != nil {
		t.Fatalf("after SetTimeout the same call should succeed; got: %v", err)
	}
	if resp.Content != "done" {
		t.Errorf("Content = %q, want %q", resp.Content, "done")
	}
}

func TestBackendSetTimeoutIgnoresNonPositive(t *testing.T) {
	b := New(Spec{Name: "x-cli", Binary: "x", Timeout: 2 * time.Minute})
	b.SetTimeout(0)
	b.SetTimeout(-time.Second)
	if b.spec.Timeout != 2*time.Minute {
		t.Errorf("spec.Timeout = %v, want the original 2m left untouched", b.spec.Timeout)
	}
}

// Compile-time proof that the CLI layer's type assertion will succeed.
var _ provider.TimeoutSetter = (*Backend)(nil)

func TestArgLimitPerPlatform(t *testing.T) {
	big := strings.Repeat("x", 200*1024)
	small := strings.Repeat("x", 10*1024)
	env := []string{"PATH=/usr/bin", "HOME=/home/u"}
	bigEnv := []string{"HUGE=" + strings.Repeat("e", 850_000)}

	tests := []struct {
		name      string
		goos      string
		args      []string
		env       []string
		wantKind  ArgKind
		wantLimit int
	}{
		{"linux small", "linux", []string{"gemini", "-p", small}, env, ArgFits, posixMaxTotalBytes},
		{"linux single arg over cap", "linux", []string{"gemini", "-p", big}, env, ArgSingleTooLong, posixMaxArgBytes},
		{"darwin single arg over cap", "darwin", []string{"gemini", "-p", big}, env, ArgSingleTooLong, posixMaxArgBytes},
		{"linux total over budget via many args", "linux", manyArgs(small, 100), env, ArgTotalTooLong, posixMaxTotalBytes},
		// A modest prompt that only overflows because of a huge environment
		// must be reported as the total budget, not as a "big prompt".
		{"linux total over budget via env", "linux", []string{"gemini", "-p", strings.Repeat("x", 100*1024)}, bigEnv, ArgTotalTooLong, posixMaxTotalBytes},
		{"windows small", "windows", []string{"gemini", "-p", small}, env, ArgFits, windowsMaxCmdLine},
		{"windows over", "windows", []string{"gemini", "-p", strings.Repeat("x", 33_000)}, env, ArgSingleTooLong, windowsMaxCmdLine},
		// Escaping worst case: 20k quotes double on the wire and tip a
		// line that is under the limit in raw characters.
		{"windows escape overhead", "windows", []string{"gemini", "-p", strings.Repeat(`"`, 20_000)}, env, ArgSingleTooLong, windowsMaxCmdLine},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			size, limit, kind := ArgLimit(tc.goos, tc.args, tc.env)
			if kind != tc.wantKind || limit != tc.wantLimit {
				t.Fatalf("ArgLimit = (%d, %d, %v), want kind=%v limit=%d",
					size, limit, kind, tc.wantKind, tc.wantLimit)
			}
			if size <= 0 {
				t.Errorf("size = %d, want > 0", size)
			}
		})
	}
}

func manyArgs(chunk string, n int) []string {
	out := []string{"gemini"}
	for i := 0; i < n; i++ {
		out = append(out, chunk)
	}
	return out
}

// TestBackendReviewArgPreflightSkipsExec: with CheckArgSize an oversized
// prompt is rejected before the subprocess is started. The fake CLI drops a
// marker file the moment it runs; the marker must not exist afterwards.
func TestBackendReviewArgPreflightSkipsExec(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	scriptPath(t, "argmax-cli", fmt.Sprintf("touch %q; echo ok", marker))

	b := New(Spec{
		Name:         "argmax-cli",
		Binary:       "argmax-cli",
		PromptArgs:   func(p string, _ bool) []string { return []string{"-p", p} },
		CheckArgSize: true,
		Timeout:      5 * time.Second,
	})
	_, err := b.Review(context.Background(), provider.Request{
		UserPrompt: strings.Repeat("d", 256*1024),
	})
	if err == nil {
		t.Fatal("expected an error for an oversized prompt")
	}
	if !errors.Is(err, ErrArgTooLong) {
		t.Errorf("error should match ErrArgTooLong; got %v", err)
	}
	var atl *ArgTooLongError
	if !errors.As(err, &atl) || atl.Size <= atl.Limit {
		t.Errorf("ArgTooLongError should carry size > limit; got %+v", atl)
	}
	for _, want := range []string{"argmax-cli", "KB", "--file", "claude-cli"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message missing %q: %s", want, err.Error())
		}
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Error("the CLI was executed despite the preflight rejection")
	}
}

// TestBackendReviewArgPreflightSkipsExecInKernelAcceptedRange pins the
// preflight itself, not the kernel: 124 KiB is over our 120 KiB cap but under
// Linux's 128 KiB MAX_ARG_STRLEN (and far under macOS's 1 MiB), so exec would
// have succeeded. Deleting CheckArgSize makes the marker appear on every
// platform and this test fail.
func TestBackendReviewArgPreflightSkipsExecInKernelAcceptedRange(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	scriptPath(t, "argmax-range-cli", fmt.Sprintf("touch %q; echo ok", marker))
	spec := Spec{
		Name:       "argmax-range-cli",
		Binary:     "argmax-range-cli",
		PromptArgs: func(p string, _ bool) []string { return []string{"-p", p} },
		Timeout:    5 * time.Second,
	}
	req := provider.Request{UserPrompt: strings.Repeat("d", 124*1024)}

	spec.CheckArgSize = true
	_, err := New(spec).Review(context.Background(), req)
	if !errors.Is(err, ErrArgTooLong) {
		t.Fatalf("preflight should reject a 124 KiB prompt; got %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("the CLI was executed despite the preflight rejection")
	}

	// Control: without the preflight the kernel accepts it, so the marker
	// proves the rejection above came from CheckArgSize alone.
	spec.CheckArgSize = false
	if _, err := New(spec).Review(context.Background(), req); err != nil {
		t.Fatalf("control run should succeed without the preflight: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Error("control run should have executed the CLI")
	}
}

// TestArgTooLongMessageNamesTheBudget: the text must describe what was
// measured — an unpredicted OS rejection quotes no limit, and a total-budget
// overflow says the environment counts.
func TestArgTooLongMessageNamesTheBudget(t *testing.T) {
	osMsg := newArgTooLongError("x-cli", "en", 300*1024, 0, ArgRejectedByOS).Error()
	if !strings.Contains(osMsg, "operating system rejected") || strings.Contains(osMsg, "over the") {
		t.Errorf("OS-rejected message should not quote a limit: %s", osMsg)
	}
	total := newArgTooLongError("x-cli", "en", 950_000, posixMaxTotalBytes, ArgTotalTooLong).Error()
	if !strings.Contains(total, "plus environment") {
		t.Errorf("total-budget message should name the environment: %s", total)
	}
	single := newArgTooLongError("x-cli", "en", 200*1024, posixMaxArgBytes, ArgSingleTooLong).Error()
	if !strings.Contains(single, "the prompt is 200 KB") {
		t.Errorf("single-arg message should name the prompt: %s", single)
	}
	tr := newArgTooLongError("x-cli", "tr", 950_000, posixMaxTotalBytes, ArgTotalTooLong).Error()
	if !strings.Contains(tr, "ortam") {
		t.Errorf("Turkish total-budget message should mention the environment: %s", tr)
	}
}

// TestBackendReviewArgPreflightLocalized: the message follows the request
// language.
func TestBackendReviewArgPreflightLocalized(t *testing.T) {
	scriptPath(t, "argmax-tr-cli", "echo ok")
	b := New(Spec{
		Name:         "argmax-tr-cli",
		Binary:       "argmax-tr-cli",
		PromptArgs:   func(p string, _ bool) []string { return []string{"-p", p} },
		CheckArgSize: true,
	})
	_, err := b.Review(context.Background(), provider.Request{
		UserPrompt: strings.Repeat("d", 256*1024),
		Lang:       "tr",
	})
	if err == nil || !strings.Contains(err.Error(), "sınırını aşıyor") {
		t.Fatalf("expected the Turkish message; got %v", err)
	}
}

// TestBackendReviewArgPreflightPassesSmallPrompt: the check must not reject
// a prompt that fits, and is not applied when CheckArgSize is off.
func TestBackendReviewArgPreflightPassesSmallPrompt(t *testing.T) {
	scriptPath(t, "argmax-ok-cli", "echo fine")
	b := New(Spec{
		Name:         "argmax-ok-cli",
		Binary:       "argmax-ok-cli",
		PromptArgs:   func(p string, _ bool) []string { return []string{"-p", p} },
		CheckArgSize: true,
	})
	resp, err := b.Review(context.Background(), provider.Request{UserPrompt: "small"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "fine" {
		t.Errorf("Content = %q, want fine", resp.Content)
	}
}

// TestBackendReviewMapsE2BIG: with the preflight off, the kernel's own E2BIG
// from exec (a real 2 MiB argument — over both Linux's per-string cap and
// macOS's 1 MiB total) is mapped to the same actionable error.
func TestBackendReviewMapsE2BIG(t *testing.T) {
	scriptPath(t, "e2big-cli", "echo unreachable")
	b := New(Spec{
		Name:       "e2big-cli",
		Binary:     "e2big-cli",
		PromptArgs: func(p string, _ bool) []string { return []string{"-p", p} },
		Timeout:    10 * time.Second,
	})
	_, err := b.Review(context.Background(), provider.Request{
		UserPrompt: strings.Repeat("d", 2*1024*1024),
	})
	if err == nil {
		t.Fatal("expected exec to fail with E2BIG")
	}
	if !errors.Is(err, ErrArgTooLong) {
		t.Fatalf("E2BIG should map to ErrArgTooLong; got %v", err)
	}
	var atl *ArgTooLongError
	if !errors.As(err, &atl) || atl.Kind == ArgFits {
		t.Errorf("mapped error must never carry ArgFits; got %+v", atl)
	}
	if !strings.Contains(err.Error(), "claude-cli") || strings.Contains(err.Error(), "invocation failed") {
		t.Errorf("expected the actionable message, got %q", err.Error())
	}
}

func TestIsArgTooLong(t *testing.T) {
	if isArgTooLong(nil) {
		t.Error("nil is not E2BIG")
	}
	if !isArgTooLong(fmt.Errorf("fork/exec /x: %w", syscall.E2BIG)) {
		t.Error("wrapped syscall.E2BIG must match")
	}
	if !isArgTooLong(errors.New("fork/exec /x: Argument list too long")) {
		t.Error("message form must match")
	}
	if isArgTooLong(errors.New("exit status 1")) {
		t.Error("an ordinary exit error must not match")
	}
}
