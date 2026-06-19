// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

// ---------- token preflight (guard.token_preflight, ADR-0003) ----------

func TestHandleTokenPreflightWithinWindowSilent(t *testing.T) {
	resetGlobalFlags(t)
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)

	prov := mock.New() // default ContextWindow is 100_000
	p := prompt.Prompt{System: "short system", User: "short user"}

	if handleTokenPreflight(cmd, app, prov, p, "mock-model", emptyStdin()) {
		t.Error("prompt that fits the window must not abort")
	}
	if errBuf.Len() > 0 {
		t.Errorf("within-window preflight must be silent; got stderr:\n%s", errBuf.String())
	}
}

func TestHandleTokenPreflightExceedsNonInteractiveAborts(t *testing.T) {
	resetGlobalFlags(t)
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)

	prov := mock.New()
	prov.Window = 10 // tiny context window

	// EstimatedTokens is chars/4; a few hundred chars easily clears 10.
	p := prompt.Prompt{
		System: strings.Repeat("system prompt content ", 40),
		User:   strings.Repeat("diff line content ", 40),
	}
	if !p.ExceedsContext(prov.ContextWindow("mock-model")) {
		t.Fatal("test setup: prompt should exceed the tiny window")
	}

	// Test stdin is not a TTY → non-interactive abort path.
	if !handleTokenPreflight(cmd, app, prov, p, "mock-model", emptyStdin()) {
		t.Error("over-window prompt in non-interactive mode must abort")
	}
	got := errBuf.String()
	if !strings.Contains(got, "context window") {
		t.Errorf("expected the over-window warning on stderr; got:\n%s", got)
	}
	if !strings.Contains(got, "non-interactive") {
		t.Errorf("expected the non-interactive abort notice; got:\n%s", got)
	}
}

// ---------- guard.token_preflight config round-trip ----------

func TestConfigFieldTokenPreflightRoundTrip(t *testing.T) {
	cfg := config.Default()

	// Default is opt-in → false.
	if got, err := configFieldGet(cfg, "guard.token_preflight"); err != nil || got != "false" {
		t.Fatalf("default guard.token_preflight = %q, err=%v; want \"false\"", got, err)
	}

	if err := configFieldSet(cfg, "guard.token_preflight", "true"); err != nil {
		t.Fatalf("set guard.token_preflight: %v", err)
	}
	if !cfg.Guard.TokenPreflight {
		t.Error("set did not flip the struct field")
	}
	if got, err := configFieldGet(cfg, "guard.token_preflight"); err != nil || got != "true" {
		t.Errorf("after set, guard.token_preflight = %q, err=%v; want \"true\"", got, err)
	}
}

func TestConfigFieldGuardUnknownFieldListsTokenPreflight(t *testing.T) {
	cfg := config.Default()
	_, err := configFieldGet(cfg, "guard.bogus")
	if err == nil || !strings.Contains(err.Error(), "token_preflight") {
		t.Errorf("unknown guard field error should list token_preflight; got: %v", err)
	}
	// The v1.7.0 additions must show up in the allowed-fields list too.
	if !strings.Contains(err.Error(), "secret_patterns") || !strings.Contains(err.Error(), "injection_scan") {
		t.Errorf("unknown guard field error should list secret_patterns + injection_scan; got: %v", err)
	}
}

// ---------- guard.secret_patterns get/set (ADR-0024) ----------

func TestConfigFieldSecretPatternsGetListsNames(t *testing.T) {
	cfg := config.Default()
	// Empty by default.
	if got, err := configFieldGet(cfg, "guard.secret_patterns"); err != nil || got != "" {
		t.Fatalf("default guard.secret_patterns = %q, err=%v; want empty", got, err)
	}
	cfg.Guard.SecretPatterns = []config.SecretPatternConfig{
		{Name: "Internal Token", Regex: `INT-[0-9]{10}`},
		{Name: "Acme Key", Regex: `acme_[a-z]{12}`},
	}
	got, err := configFieldGet(cfg, "guard.secret_patterns")
	if err != nil {
		t.Fatalf("get guard.secret_patterns: %v", err)
	}
	if !strings.Contains(got, "Internal Token") || !strings.Contains(got, "Acme Key") {
		t.Errorf("get should list both pattern names; got %q", got)
	}
}

func TestConfigFieldSecretPatternsSetRejected(t *testing.T) {
	cfg := config.Default()
	err := configFieldSet(cfg, "guard.secret_patterns", "anything")
	if err == nil {
		t.Fatal("expected config set guard.secret_patterns to be rejected")
	}
	if !strings.Contains(err.Error(), "edit the config file directly") {
		t.Errorf("rejection should point the user at the config file; got %v", err)
	}
}

// ---------- guard.injection_scan config round-trip (ADR-0025) ----------

func TestConfigFieldInjectionScanRoundTrip(t *testing.T) {
	cfg := config.Default()

	// Default is on → true.
	if got, err := configFieldGet(cfg, "guard.injection_scan"); err != nil || got != "true" {
		t.Fatalf("default guard.injection_scan = %q, err=%v; want \"true\"", got, err)
	}
	if err := configFieldSet(cfg, "guard.injection_scan", "false"); err != nil {
		t.Fatalf("set guard.injection_scan: %v", err)
	}
	if cfg.Guard.InjectionScan {
		t.Error("set did not flip the struct field")
	}
	if got, err := configFieldGet(cfg, "guard.injection_scan"); err != nil || got != "false" {
		t.Errorf("after set, guard.injection_scan = %q, err=%v; want \"false\"", got, err)
	}
}

// ---------- --show-prompt ----------

func TestShowPromptEmitsPromptAndSkipsProvider(t *testing.T) {
	e := newCLIEnv(t)

	if err := e.run("--staged", "--show-prompt"); err != nil {
		t.Fatalf("--show-prompt: %v\nstderr:\n%s", err, e.errOut.String())
	}
	out := e.out.String()

	for _, want := range []string{"===== SYSTEM PROMPT =====", "===== USER PROMPT ====="} {
		if !strings.Contains(out, want) {
			t.Errorf("--show-prompt output missing %q; got:\n%s", want, out)
		}
	}
	// The staged diff body must appear in the user prompt.
	if !strings.Contains(out, "func Login") {
		t.Errorf("--show-prompt should include the staged diff; got:\n%s", out)
	}
	// Proof no review ran: the mock provider's canned finding title must
	// not appear — --show-prompt exits before any provider call.
	if strings.Contains(out, "mock review output") {
		t.Errorf("--show-prompt must not invoke the provider; saw mock review output:\n%s", out)
	}
}
