// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/i18n"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

// registerOnce ensures the mock provider is registered exactly once across
// all tests in this package. provider.Register panics on duplicate names.
var registerOnce sync.Once

func ensureMockRegistered(t *testing.T) {
	t.Helper()
	registerOnce.Do(func() {
		mock.Register()
	})
}

func TestApplyMergesIntoDefaults(t *testing.T) {
	cfg := Apply(nil, Choices{
		Provider: "anthropic",
		APIKey:   "sk-test",
		Model:    "claude-opus-4-7",
		Lang:     "tr",
	})
	if cfg.Provider != "anthropic" {
		t.Errorf("Provider = %q", cfg.Provider)
	}
	got := cfg.Providers["anthropic"]
	if got.APIKey != "sk-test" {
		t.Errorf("APIKey = %q", got.APIKey)
	}
	if got.Model != "claude-opus-4-7" {
		t.Errorf("Model = %q", got.Model)
	}
	if cfg.Output.Lang != "tr" {
		t.Errorf("Lang = %q", cfg.Output.Lang)
	}
	// Untouched provider should retain default URL from config.Default
	if cfg.Providers["openai"].BaseURL == "" {
		t.Error("openai BaseURL lost — Apply should preserve defaults for other providers")
	}
}

func TestApplyOllama(t *testing.T) {
	cfg := Apply(nil, Choices{
		Provider: "ollama",
		BaseURL:  "http://gpu.lan:11434",
		Model:    "qwen2.5-coder:14b",
	})
	got := cfg.Providers["ollama"]
	if got.BaseURL != "http://gpu.lan:11434" {
		t.Errorf("BaseURL = %q", got.BaseURL)
	}
	if got.Model != "qwen2.5-coder:14b" {
		t.Errorf("Model = %q", got.Model)
	}
	if got.APIKey != "" {
		t.Errorf("Ollama should not have API key, got %q", got.APIKey)
	}
}

// TestApplyPreservesExistingKeys is the regression guard for the v0.6 bug
// that wiped already-configured API keys whenever `commitbrief setup` was
// re-run for a different provider. Loading the existing config and
// passing it as base must keep other providers' fields intact.
func TestApplyPreservesExistingKeys(t *testing.T) {
	base := config.Default()
	base.Providers["anthropic"] = config.ProviderConfig{
		APIKey:  "sk-ant-existing",
		Model:   "claude-opus-4-7",
		BaseURL: "https://api.anthropic.com",
	}
	base.Providers["gemini"] = config.ProviderConfig{
		APIKey: "AIza-existing",
		Model:  "gemini-2.5-pro",
	}
	base.Provider = "anthropic"

	cfg := Apply(base, Choices{
		Provider: "openai",
		APIKey:   "sk-openai-new",
		Model:    "gpt-4o",
	})

	// New provider written through.
	if got := cfg.Providers["openai"].APIKey; got != "sk-openai-new" {
		t.Errorf("openai APIKey = %q, want sk-openai-new", got)
	}
	if cfg.Provider != "openai" {
		t.Errorf("Active provider should follow the latest setup; got %q", cfg.Provider)
	}

	// Pre-existing providers must survive intact — the whole point of the fix.
	if got := cfg.Providers["anthropic"].APIKey; got != "sk-ant-existing" {
		t.Errorf("anthropic APIKey wiped (regression): got %q", got)
	}
	if got := cfg.Providers["anthropic"].Model; got != "claude-opus-4-7" {
		t.Errorf("anthropic Model lost: got %q", got)
	}
	if got := cfg.Providers["gemini"].APIKey; got != "AIza-existing" {
		t.Errorf("gemini APIKey wiped (regression): got %q", got)
	}
}

// TestApplyKeepsKeyWhenReconfiguringSameProvider guards the wizard's
// "leave blank to keep the existing key" behavior: re-running setup for a
// provider that already has a key, submitting an empty key but a new model,
// must preserve the stored key and update only the model. This is the
// contract the promptAPIKey existing-key branch relies on.
func TestApplyKeepsKeyWhenReconfiguringSameProvider(t *testing.T) {
	base := config.Default()
	base.Providers["anthropic"] = config.ProviderConfig{
		APIKey:  "sk-ant-existing",
		Model:   "claude-opus-4-8",
		BaseURL: "https://api.anthropic.com",
	}
	base.Provider = "anthropic"

	// Empty APIKey (user left the prompt blank), new model only.
	cfg := Apply(base, Choices{
		Provider: "anthropic",
		Model:    "claude-sonnet-4-6",
	})

	if got := cfg.Providers["anthropic"].APIKey; got != "sk-ant-existing" {
		t.Errorf("APIKey not preserved on empty input: got %q, want sk-ant-existing", got)
	}
	if got := cfg.Providers["anthropic"].Model; got != "claude-sonnet-4-6" {
		t.Errorf("Model not updated: got %q, want claude-sonnet-4-6", got)
	}
	if got := cfg.Providers["anthropic"].BaseURL; got != "https://api.anthropic.com" {
		t.Errorf("BaseURL lost: got %q", got)
	}
}

func TestApplyFirstRunStartsFromDefault(t *testing.T) {
	// Passing nil base mimics first-time setup: result must be a clean
	// Default config with the choices layered on, no leftover state.
	cfg := Apply(nil, Choices{Provider: "openai", APIKey: "sk-x", Model: "gpt-4o"})
	if len(cfg.Providers) < 4 {
		t.Errorf("first-run config should include all known providers from Default; got %d", len(cfg.Providers))
	}
	if cfg.Providers["anthropic"].APIKey != "" {
		t.Error("first-run shouldn't ship an anthropic key out of nowhere")
	}
}

func TestFindSpec(t *testing.T) {
	if FindSpec("anthropic") == nil {
		t.Error("anthropic spec not found")
	}
	if FindSpec("nonexistent") != nil {
		t.Error("FindSpec on unknown should return nil")
	}
}

func TestGlobalConfigPath(t *testing.T) {
	path, err := GlobalConfigPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, filepath.Join(".commitbrief", "config.yml")) {
		t.Errorf("GlobalConfigPath = %q, expected to end with .commitbrief/config.yml", path)
	}
}

func TestRepoConfigPath(t *testing.T) {
	p := RepoConfigPath("/repo")
	want := filepath.Join("/repo", ".commitbrief", "config.yml")
	if p != want {
		t.Errorf("RepoConfigPath = %q, want %q", p, want)
	}
}

func TestWriteConfigCreatesParent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deeper", "config.yml")
	cfg := config.Default()
	cfg.Providers["anthropic"] = config.ProviderConfig{APIKey: "sk", Model: "claude-opus-4-7"}

	if err := WriteConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var loaded config.Config
	if err := yaml.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Providers["anthropic"].APIKey != "sk" {
		t.Errorf("APIKey round-trip lost: %+v", loaded.Providers["anthropic"])
	}
}

func TestWriteConfigPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows uses ACLs rather than POSIX mode bits; os.WriteFile's mode argument is informational there")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := WriteConfig(path, config.Default()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Mode bits for owner read+write only (0600). API keys live here.
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o, want 0600", info.Mode().Perm())
	}
}

func TestWriteRepoConfigUpdatesGitignore(t *testing.T) {
	repo := t.TempDir()
	updated, err := WriteRepoConfig(repo, config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if !updated {
		t.Error("first-time WriteRepoConfig should create/update .gitignore")
	}
	gi, err := os.ReadFile(filepath.Join(repo, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gi), ".commitbrief/") {
		t.Errorf(".gitignore missing entry; content:\n%s", gi)
	}
	cfg, err := os.ReadFile(filepath.Join(repo, ".commitbrief", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg) == 0 {
		t.Error("config.yml is empty")
	}
}

func TestWriteRepoConfigRequiresRoot(t *testing.T) {
	if _, err := WriteRepoConfig("", config.Default()); err == nil {
		t.Error("empty repoRoot should error")
	}
}

func TestTestConnectionSuccess(t *testing.T) {
	ensureMockRegistered(t)
	if err := TestConnection(context.Background(), "mock", config.ProviderConfig{}); err != nil {
		t.Errorf("TestConnection on healthy mock: %v", err)
	}
}

func TestTestConnectionUnknownProvider(t *testing.T) {
	if err := TestConnection(context.Background(), "no-such-provider", config.ProviderConfig{}); !errors.Is(err, provider.ErrUnknownProvider) {
		t.Errorf("err = %v, want ErrUnknownProvider", err)
	}
}

func TestOllamaModelsHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{"name": "qwen2.5-coder:14b"},
				{"name": "llama3:latest"},
			},
		})
	}))
	defer srv.Close()

	got, err := OllamaModels(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "qwen2.5-coder:14b" || got[1] != "llama3:latest" {
		t.Errorf("OllamaModels = %v", got)
	}
}

func TestOllamaModelsTrailingSlash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": []map[string]any{}})
	}))
	defer srv.Close()

	if _, err := OllamaModels(context.Background(), srv.URL+"/"); err != nil {
		t.Errorf("trailing slash should be tolerated: %v", err)
	}
}

func TestOllamaModelsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("ollama broken"))
	}))
	defer srv.Close()

	if _, err := OllamaModels(context.Background(), srv.URL); err == nil {
		t.Error("expected error on 500")
	}
}

func TestOllamaModelsDefaultBaseURL(t *testing.T) {
	// We can't easily intercept default URL without DNS hijinks, but we can
	// verify the function doesn't panic and at least returns an error
	// (since localhost:11434 is unlikely to be reachable in CI).
	_, err := OllamaModels(context.Background(), "")
	if err == nil {
		t.Skip("ollama appears to be running locally; skipping default-URL error check")
	}
}

func TestOllamaModelsBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	if _, err := OllamaModels(context.Background(), srv.URL); err == nil {
		t.Error("expected error on malformed body")
	}
}

func TestTrFallsBackToProvidedDefault(t *testing.T) {
	// UC-16 helper. nil catalog → English fallback so the wizard
	// stays usable in library/test contexts. Non-nil catalog with a
	// missing key also returns the fallback rather than echoing the
	// key back (which would be ugly UX).
	if got := tr(nil, "any.key", "default"); got != "default" {
		t.Errorf("nil catalog → %q, want default", got)
	}
	en, _ := i18n.Load("en")
	if got := tr(en, "no.such.key.xyzzy", "fallback"); got != "fallback" {
		t.Errorf("missing key → %q, want fallback", got)
	}
}

func TestTrResolvesTurkishCatalogForSetupKeys(t *testing.T) {
	// Pin the exact wizard strings we now wire from the catalog so a
	// future setup-prompt rewrite doesn't silently break TR users.
	cat, err := i18n.Load("tr")
	if err != nil {
		t.Fatalf("load tr catalog: %v", err)
	}
	cases := map[string]string{
		"setup.provider.prompt":       "Hangi sağlayıcıyı yapılandırmak istersiniz?",
		"setup.api_key.prompt":        "API anahtarınızı girin:",
		"setup.base_url.prompt":       "Ollama temel URL'si:",
		"setup.model.prompt":          "Bir model seçin:",
		"setup.model.discover_failed": "Model adı (Ollama'dan keşfedilemedi):",
		"setup.api_key.empty":         "API anahtarı boş olamaz.",
	}
	for key, want := range cases {
		got := tr(cat, key, "FALLBACK")
		if got != want {
			t.Errorf("tr catalog key %q = %q, want %q", key, got, want)
		}
	}
}

func TestNotEmptyForUsesCatalog(t *testing.T) {
	// Validator closure must emit the catalog's empty-input message.
	cat, _ := i18n.Load("tr")
	validator := notEmptyFor(cat)
	if err := validator(""); err == nil {
		t.Fatal("expected error on empty input")
	} else if !strings.Contains(err.Error(), "boş olamaz") {
		t.Errorf("error did not include TR message; got %q", err.Error())
	}
	if err := validator("filled"); err != nil {
		t.Errorf("non-empty input should not error; got %v", err)
	}
}
