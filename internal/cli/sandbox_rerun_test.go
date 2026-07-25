// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/flaky"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// sandboxTestApp builds an appContext with a real catalog and the default
// config (review.sandbox_rerun = 0, off).
func sandboxTestApp(t *testing.T) *appContext {
	t.Helper()
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	return &appContext{Config: config.Default(), Catalog: cat}
}

// bareCmd returns a command with a context, used so applySandboxRerun's
// cmd.Flags().Changed("sandbox-rerun") is false (flag absent ⇒ config drives N).
func bareCmd() *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	return cmd
}

func sampleFlaky() []render.Finding {
	return []render.Finding{
		{Severity: render.SeverityMedium, File: "a_test.go", Line: 11, Title: "sleep", Suggestion: "Use a wait."},
		{Severity: render.SeverityLow, File: "b_test.go", Line: 20, Title: "random", Suggestion: "Seed it."},
	}
}

// withRunner temporarily binds the package rerun seam to a runner built around
// exec, restoring the shipped default (an empty sandbox_command ⇒ no runner) on
// cleanup so other tests still see the no-op.
func withRunner(t *testing.T, needsTest bool, exec flaky.Executor) {
	t.Helper()
	prev := buildSandboxRunner
	buildSandboxRunner = func(*appContext) (*sandboxRunner, error) {
		return &sandboxRunner{exec: exec, needsTest: needsTest, display: "fake-runner"}, nil
	}
	t.Cleanup(func() { buildSandboxRunner = prev })
}

func TestApplySandboxRerun_DefaultOffIsNoOp(t *testing.T) {
	// review.sandbox_rerun defaults to 0 and the flag is absent: the static
	// findings must come back byte-identical, and a bound executor must never
	// be invoked (so existing behaviour is unchanged).
	app := sandboxTestApp(t)
	calls := 0
	withRunner(t, false, func(context.Context, flaky.Target) (bool, error) { calls++; return true, nil })

	in := sampleFlaky()
	out, err := applySandboxRerun(bareCmd(), app, in)
	if err != nil {
		t.Fatalf("applySandboxRerun errored: %v", err)
	}

	if calls != 0 {
		t.Errorf("executor called %d times with sandbox-rerun off, want 0", calls)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("finding %d changed while off:\n got %+v\nwant %+v", i, out[i], in[i])
		}
	}
}

func TestApplySandboxRerun_UnboundExecutorIsNoOp(t *testing.T) {
	// Opted in (N>0) but no runner bound (the shipped default): still a no-op,
	// findings untouched. This is the production state until a user configures
	// review.sandbox_command.
	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 5 // opt in via config

	prev := buildSandboxRunner
	buildSandboxRunner = func(*appContext) (*sandboxRunner, error) { return nil, nil }
	t.Cleanup(func() { buildSandboxRunner = prev })

	in := sampleFlaky()
	out, err := applySandboxRerun(bareCmd(), app, in)
	if err != nil {
		t.Fatalf("applySandboxRerun errored: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
	for i := range in {
		if out[i] != in[i] {
			t.Errorf("finding %d changed with unbound executor", i)
		}
	}
}

func TestApplySandboxRerun_ConfigDrivesAnnotation(t *testing.T) {
	// Opted in via config with a bound fake executor that always passes ⇒ every
	// finding is reclassified transient (demoted to info, suggestion annotated).
	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 3
	withRunner(t, false, func(context.Context, flaky.Target) (bool, error) { return true, nil })

	out, err := applySandboxRerun(bareCmd(), app, sampleFlaky())
	if err != nil {
		t.Fatalf("applySandboxRerun errored: %v", err)
	}
	for _, f := range out {
		if f.Severity != render.SeverityInfo {
			t.Errorf("all-pass rerun should demote to info, got %q for %s", f.Severity, f.File)
		}
		if !strings.Contains(strings.ToLower(f.Suggestion), "did not reproduce") {
			t.Errorf("transient verdict not annotated: %q", f.Suggestion)
		}
	}
}

func TestApplySandboxRerun_MixedConfirmsFlaky(t *testing.T) {
	// A fake that returns pass then fail on alternating calls makes each test
	// confirm flaky; severity is preserved (a confirmed flake still matters).
	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 4
	flip := false
	withRunner(t, false, func(context.Context, flaky.Target) (bool, error) {
		flip = !flip
		return flip, nil
	})

	in := sampleFlaky()
	out, err := applySandboxRerun(bareCmd(), app, in)
	if err != nil {
		t.Fatalf("applySandboxRerun errored: %v", err)
	}
	if len(out) != len(in) {
		t.Fatalf("len(out) = %d, want %d", len(out), len(in))
	}
	for i, f := range out {
		if f.Severity != in[i].Severity {
			t.Errorf("confirmed flaky should keep severity, got %q want %q", f.Severity, in[i].Severity)
		}
		if !strings.Contains(strings.ToLower(f.Suggestion), "confirmed flaky") {
			t.Errorf("flaky verdict not annotated: %q", f.Suggestion)
		}
	}
}

func TestApplySandboxRerun_SkipsWhenTestNameUnresolved(t *testing.T) {
	// needsTest with an unresolvable name must skip THAT finding only, never
	// fall back to a command that would run the whole suite N times.
	var seen []flaky.Target
	withRunner(t, true, func(_ context.Context, tgt flaky.Target) (bool, error) {
		seen = append(seen, tgt)
		return true, nil
	})

	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 5
	app.RepoRoot = t.TempDir() // nothing on disk ⇒ EnclosingTest cannot resolve
	in := []render.Finding{{File: "definitely-not-on-disk_test.go", Line: 3, Title: "sleep"}}

	out, err := applySandboxRerun(bareCmd(), app, in)
	if err != nil {
		t.Fatalf("applySandboxRerun errored: %v", err)
	}
	if len(seen) != 0 {
		t.Errorf("executor was called %d times; want 0", len(seen))
	}
	if out[0].Title != in[0].Title {
		t.Errorf("finding was altered despite the skip: %+v", out[0])
	}
}

func TestApplySandboxRerun_InvalidCommandAborts(t *testing.T) {
	// A malformed template must abort the review before any provider call,
	// not silently disable the feature.
	prev := buildSandboxRunner
	buildSandboxRunner = func(*appContext) (*sandboxRunner, error) {
		return nil, errors.New("sandbox_command[0]: unclosed action")
	}
	t.Cleanup(func() { buildSandboxRunner = prev })

	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 5

	if _, err := applySandboxRerun(bareCmd(), app, sampleFlaky()); err == nil {
		t.Fatal("a malformed sandbox_command did not abort the review")
	}
}

func TestApplySandboxRerun_ResolvesTestName(t *testing.T) {
	// The resolved enclosing test name must reach the executor, since that is
	// what {{.Test}} renders from.
	dir := t.TempDir()
	src := "package a\n\nimport \"testing\"\n\nfunc TestLogin(t *testing.T) {\n\ttime.Sleep(1)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen []flaky.Target
	withRunner(t, true, func(_ context.Context, tgt flaky.Target) (bool, error) {
		seen = append(seen, tgt)
		return true, nil
	})

	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 1
	app.RepoRoot = dir

	if _, err := applySandboxRerun(bareCmd(), app, []render.Finding{{File: "a_test.go", Line: 6}}); err != nil {
		t.Fatalf("applySandboxRerun errored: %v", err)
	}
	if len(seen) != 1 || seen[0].Test != "TestLogin" {
		t.Fatalf("target = %+v; want Test=TestLogin", seen)
	}
}

func TestApplySandboxRerun_FlagOverridesConfig(t *testing.T) {
	// --sandbox-rerun explicitly passed must win over review.sandbox_rerun. We
	// simulate the flag being Changed by registering it on the command and
	// setting global.sandboxRerun, then assert the count the resolver returns.
	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 9 // config says 9

	cmd := bareCmd()
	cmd.Flags().IntVar(&global.sandboxRerun, "sandbox-rerun", 0, "")
	prev := global.sandboxRerun
	t.Cleanup(func() { global.sandboxRerun = prev })
	if err := cmd.Flags().Set("sandbox-rerun", "2"); err != nil {
		t.Fatal(err)
	}

	if got := sandboxRerunCount(cmd, app); got != 2 {
		t.Errorf("flag should override config: sandboxRerunCount = %d, want 2", got)
	}
}

func TestSandboxRerunCount_ConfigWhenFlagAbsent(t *testing.T) {
	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 4
	if got := sandboxRerunCount(bareCmd(), app); got != 4 {
		t.Errorf("flag absent ⇒ config drives: got %d, want 4", got)
	}
}

func TestConfigGetSet_SandboxRerun(t *testing.T) {
	cfg := config.Default()
	if err := configFieldSet(cfg, "review.sandbox_rerun", "5"); err != nil {
		t.Fatalf("set review.sandbox_rerun: %v", err)
	}
	if cfg.Review.SandboxRerun != 5 {
		t.Errorf("SandboxRerun = %d, want 5", cfg.Review.SandboxRerun)
	}
	got, err := configFieldGet(cfg, "review.sandbox_rerun")
	if err != nil {
		t.Fatalf("get review.sandbox_rerun: %v", err)
	}
	if got != "5" {
		t.Errorf("get = %q, want \"5\"", got)
	}
	if err := configFieldSet(cfg, "review.sandbox_rerun", "-1"); err == nil {
		t.Errorf("negative sandbox_rerun should be rejected")
	}
	if err := configFieldSet(cfg, "review.sandbox_rerun", "notanint"); err == nil {
		t.Errorf("non-integer sandbox_rerun should be rejected")
	}
}
