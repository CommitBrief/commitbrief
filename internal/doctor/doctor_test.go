// SPDX-License-Identifier: GPL-3.0-or-later

package doctor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

// registerMockOnce mirrors the cli package guard — provider.Register
// panics on duplicate names and the doctor tests share the registry
// with everyone else in the test binary.
var registerMockOnce sync.Once

func ensureMockRegistered(t *testing.T) {
	t.Helper()
	registerMockOnce.Do(func() {
		mock.Register()
	})
}

// minimalRunner builds a Runner with a freshly-Default config so each
// test can mutate only the fields it cares about.
func minimalRunner(t *testing.T) *Runner {
	t.Helper()
	cfg := config.Default()
	return &Runner{
		Home:   t.TempDir(),
		Config: cfg,
		// RepoRoot and Catalog deliberately left zero; helpers fall
		// through to no-op paths.
	}
}

// ---------- per-check unit tests ----------

func TestCheckGitFailsWhenPATHMissing(t *testing.T) {
	t.Setenv("PATH", "")
	r := minimalRunner(t)
	got := r.checkGit()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want StatusFail when PATH is empty", got.Status)
	}
}

func TestCheckGitOKWhenOnPATH(t *testing.T) {
	// Don't touch PATH — rely on whatever the test host actually has.
	// CI runners ship git; if the test env lacks it (unusual) we skip.
	r := minimalRunner(t)
	got := r.checkGit()
	if got.Status == StatusFail {
		t.Skip("git not installed on this test host; cannot verify OK path")
	}
	if got.Status != StatusOK {
		t.Errorf("Status = %v, want StatusOK; detail=%q", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "git") {
		t.Errorf("Detail should include resolved path; got %q", got.Detail)
	}
}

func TestCheckConfigFailsOnNil(t *testing.T) {
	r := &Runner{Config: nil}
	got := r.checkConfig()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want StatusFail for nil config", got.Status)
	}
}

func TestCheckConfigFailsOnEmptyProvider(t *testing.T) {
	r := &Runner{Config: &config.Config{Provider: ""}}
	got := r.checkConfig()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want StatusFail when Provider is empty", got.Status)
	}
}

func TestCheckProviderConfiguredOKWithAPIKey(t *testing.T) {
	r := minimalRunner(t)
	r.Config.Providers["anthropic"] = config.ProviderConfig{APIKey: "sk-test"}
	got := r.checkProviderConfigured()
	if got.Status != StatusOK {
		t.Errorf("Status = %v, want StatusOK with anthropic key seeded", got.Status)
	}
}

func TestCheckProviderConfiguredOKWithOllamaActive(t *testing.T) {
	// Ollama is the no-API-key provider; a configured base URL counts
	// — but only when it's the active provider, otherwise the default
	// base_url from config.Default() would inflate the check.
	r := minimalRunner(t)
	// Strip any seeded keys from Default(), then leave ollama base_url
	// and make ollama the active selection.
	for n, pc := range r.Config.Providers {
		pc.APIKey = ""
		r.Config.Providers[n] = pc
	}
	pc := r.Config.Providers["ollama"]
	pc.BaseURL = "http://localhost:11434"
	r.Config.Providers["ollama"] = pc
	r.Config.Provider = "ollama"

	got := r.checkProviderConfigured()
	if got.Status != StatusOK {
		t.Errorf("Status = %v, want StatusOK when ollama is active and has a base URL", got.Status)
	}
}

func TestCheckProviderConfiguredFailsWhenOllamaConfiguredButInactive(t *testing.T) {
	// Regression guard: a fresh `config.Default()` always has ollama's
	// localhost:11434 base_url, but that's the *default value*, not user
	// intent. If no API keys are set and ollama isn't the active
	// provider, the check should still Fail — otherwise users would
	// never see the "run setup" hint.
	r := minimalRunner(t)
	for n, pc := range r.Config.Providers {
		pc.APIKey = ""
		r.Config.Providers[n] = pc
	}
	r.Config.Provider = "anthropic" // anthropic has no key, ollama not active

	got := r.checkProviderConfigured()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want StatusFail when no keys and ollama not active", got.Status)
	}
}

func TestCheckActiveProviderFailsWhenActiveLacksCredentials(t *testing.T) {
	// UC-03 regression guard. Even when *another* provider has a key,
	// the doctor must fail the active provider check if the active one
	// has no credentials of its own — otherwise users hit a silent 401
	// at first review. This is precisely the gap checkProviderConfigured
	// papered over.
	r := minimalRunner(t)
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	r.Catalog = cat
	for n := range r.Config.Providers {
		r.Config.Providers[n] = config.ProviderConfig{}
	}
	r.Config.Providers["anthropic"] = config.ProviderConfig{APIKey: "sk-test"}
	r.Config.Provider = "openai" // openai is the active selection, but it has no key

	got := r.checkActiveProvider()
	if got.Status != StatusFail {
		t.Errorf("active=openai with only anthropic key should Fail; got %v (detail=%q)", got.Status, got.Detail)
	}
	if !strings.Contains(got.Detail, "openai") {
		t.Errorf("detail should name the misconfigured provider; got %q", got.Detail)
	}
}

func TestCheckActiveProviderOKWhenActiveHasKey(t *testing.T) {
	r := minimalRunner(t)
	for n := range r.Config.Providers {
		r.Config.Providers[n] = config.ProviderConfig{}
	}
	r.Config.Providers["openai"] = config.ProviderConfig{APIKey: "sk-openai-test"}
	r.Config.Provider = "openai"

	got := r.checkActiveProvider()
	if got.Status != StatusOK {
		t.Errorf("active=openai with own key should be OK; got %v (detail=%q)", got.Status, got.Detail)
	}
}

func TestCheckActiveProviderOKWhenActiveIsOllamaWithBaseURL(t *testing.T) {
	r := minimalRunner(t)
	for n := range r.Config.Providers {
		r.Config.Providers[n] = config.ProviderConfig{}
	}
	r.Config.Providers["ollama"] = config.ProviderConfig{BaseURL: "http://localhost:11434"}
	r.Config.Provider = "ollama"

	got := r.checkActiveProvider()
	if got.Status != StatusOK {
		t.Errorf("ollama-active with base_url should be OK; got %v", got.Status)
	}
}

func TestCheckActiveProviderFailsWhenActiveNotInProvidersMap(t *testing.T) {
	// A typo or hand-edited config can leave Provider pointing at a key
	// that doesn't exist in Providers — surface this as a distinct Fail
	// instead of misreporting it as "no credentials".
	r := minimalRunner(t)
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	r.Catalog = cat
	r.Config.Provider = "nonexistent-provider"
	delete(r.Config.Providers, "nonexistent-provider")

	got := r.checkActiveProvider()
	if got.Status != StatusFail {
		t.Errorf("unknown active provider should Fail; got %v", got.Status)
	}
	if !strings.Contains(got.Detail, "nonexistent-provider") {
		t.Errorf("detail should name the offending provider; got %q", got.Detail)
	}
}

func TestCheckProviderConfiguredFailsWhenAllEmpty(t *testing.T) {
	r := minimalRunner(t)
	// Wipe every provider key + ollama base_url.
	for n := range r.Config.Providers {
		r.Config.Providers[n] = config.ProviderConfig{}
	}
	got := r.checkProviderConfigured()
	if got.Status != StatusFail {
		t.Errorf("Status = %v, want StatusFail with no keys at all", got.Status)
	}
}

func TestCheckCacheNoRepoSkips(t *testing.T) {
	r := minimalRunner(t)
	// RepoRoot empty (zero value) → skip path.
	got := r.checkCache()
	if got.Status != StatusOK {
		t.Errorf("Status = %v, want StatusOK skip when RepoRoot empty", got.Status)
	}
}

func TestCheckCacheCreatesAndProbesDir(t *testing.T) {
	r := minimalRunner(t)
	r.RepoRoot = t.TempDir()
	got := r.checkCache()
	if got.Status != StatusOK {
		t.Errorf("Status = %v, want StatusOK in writable tmp repo; detail=%q", got.Status, got.Detail)
	}
	// The dir should now exist; the probe file should NOT (cleanup
	// happened inside the check).
	dir := filepath.Join(r.RepoRoot, ".commitbrief", "cache")
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("expected cache dir %s to exist; err=%v", dir, err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "doctor-probe-") {
			t.Errorf("probe file not cleaned up: %s", e.Name())
		}
	}
}

func TestCheckGitignoreWarnsWhenMissing(t *testing.T) {
	r := minimalRunner(t)
	r.RepoRoot = t.TempDir()
	got := r.checkGitignore()
	if got.Status != StatusWarn {
		t.Errorf("Status = %v, want StatusWarn when .gitignore absent", got.Status)
	}
}

func TestCheckGitignoreOKWhenEntryPresent(t *testing.T) {
	r := minimalRunner(t)
	r.RepoRoot = t.TempDir()
	gi := filepath.Join(r.RepoRoot, ".gitignore")
	if err := os.WriteFile(gi, []byte("node_modules/\n.commitbrief/\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.checkGitignore()
	if got.Status != StatusOK {
		t.Errorf("Status = %v, want StatusOK when .commitbrief/ listed", got.Status)
	}
}

func TestCheckGitignoreWarnsWhenEntryMissing(t *testing.T) {
	r := minimalRunner(t)
	r.RepoRoot = t.TempDir()
	gi := filepath.Join(r.RepoRoot, ".gitignore")
	if err := os.WriteFile(gi, []byte("node_modules/\nvendor/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := r.checkGitignore()
	if got.Status != StatusWarn {
		t.Errorf("Status = %v, want StatusWarn when .gitignore lacks .commitbrief/ entry", got.Status)
	}
}

// ---------- provider connection check ----------

func TestCheckProviderConnectionsPingsMock(t *testing.T) {
	ensureMockRegistered(t)
	r := minimalRunner(t)
	// Wipe every provider, then add a single mock entry. Mock's
	// TestConnection always succeeds when no errors are injected.
	r.Config.Providers = map[string]config.ProviderConfig{
		"mock": {APIKey: "x"},
	}
	results := r.checkProviderConnections(context.Background())
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Status != StatusOK {
		t.Errorf("mock connection should be OK; got %v / %q", results[0].Status, results[0].Detail)
	}
}

func TestCheckProviderConnectionsWarnsOnFailure(t *testing.T) {
	ensureMockRegistered(t)
	r := minimalRunner(t)
	// Use a real provider name (anthropic) with a bogus API key but
	// pointed at a base URL that won't resolve — the SDK will fail
	// the ping fast.
	r.Config.Providers = map[string]config.ProviderConfig{
		"anthropic": {APIKey: "sk-ant-doctor-test", BaseURL: "http://127.0.0.1:1"},
	}
	results := r.checkProviderConnections(context.Background())
	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Status != StatusWarn {
		t.Errorf("unreachable provider should be StatusWarn; got %v / %q", results[0].Status, results[0].Detail)
	}
}

// ---------- summary / aggregate ----------

func TestSummarizeCountsByStatus(t *testing.T) {
	results := []Result{
		{Status: StatusOK},
		{Status: StatusOK},
		{Status: StatusWarn},
		{Status: StatusFail},
	}
	got := Summarize(results)
	if got.Total != 4 || got.OK != 2 || got.Warnings != 1 || got.Failed != 1 {
		t.Errorf("Summary = %+v, want {Total:4 OK:2 Warnings:1 Failed:1}", got)
	}
}

func TestRunAllProducesAllChecks(t *testing.T) {
	r := minimalRunner(t)
	r.Config.Providers["anthropic"] = config.ProviderConfig{APIKey: "sk-test"}
	results := r.RunAll(context.Background())

	// We don't assert exact length because checkProviderConnections
	// may fan out per-configured-provider; assert the core checks all
	// appear by name (i18n-less labels).
	wantNames := []string{
		"doctor.check.git",
		"doctor.check.config",
		"doctor.check.rules",
		"doctor.check.output",
		"doctor.check.provider_configured",
		"doctor.check.cache",
		"doctor.check.gitignore",
	}
	got := map[string]bool{}
	for _, r := range results {
		got[r.Name] = true
	}
	for _, want := range wantNames {
		if !got[want] {
			t.Errorf("RunAll missing check %q; got names: %v", want, got)
		}
	}
}

func TestStatusString(t *testing.T) {
	// Sanity check for the debug label; used in error messages and
	// test failure output.
	cases := map[Status]string{
		StatusOK:   "ok",
		StatusWarn: "warn",
		StatusFail: "fail",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("Status(%d).String() = %q, want %q", s, got, want)
		}
	}
}

// guard against unused-import drift if I refactor away one of these
// later — the test file itself is what justifies them.
var _ = provider.ErrUnauthorized
