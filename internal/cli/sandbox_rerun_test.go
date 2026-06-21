// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
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

// withExecutor temporarily binds the package rerun seam to exec for the test,
// restoring the (nil) default on cleanup so other tests see the shipped no-op.
func withExecutor(t *testing.T, exec flaky.Executor) {
	t.Helper()
	prev := sandboxRerunExecutor
	sandboxRerunExecutor = func(*appContext) flaky.Executor { return exec }
	t.Cleanup(func() { sandboxRerunExecutor = prev })
}

func TestApplySandboxRerun_DefaultOffIsNoOp(t *testing.T) {
	// review.sandbox_rerun defaults to 0 and the flag is absent: the static
	// findings must come back byte-identical, and a bound executor must never
	// be invoked (so existing behaviour is unchanged).
	app := sandboxTestApp(t)
	calls := 0
	withExecutor(t, func(context.Context, string) (bool, error) { calls++; return true, nil })

	in := sampleFlaky()
	out := applySandboxRerun(bareCmd(), app, in)

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
	// findings untouched. This is the production state until a runner increment.
	app := sandboxTestApp(t)
	app.Config.Review.SandboxRerun = 5 // opt in via config
	// sandboxRerunExecutor is the shipped nil-returning default here.

	in := sampleFlaky()
	out := applySandboxRerun(bareCmd(), app, in)
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
	withExecutor(t, func(context.Context, string) (bool, error) { return true, nil })

	out := applySandboxRerun(bareCmd(), app, sampleFlaky())
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
	withExecutor(t, func(context.Context, string) (bool, error) {
		flip = !flip
		return flip, nil
	})

	in := sampleFlaky()
	out := applySandboxRerun(bareCmd(), app, in)
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
