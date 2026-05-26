package setup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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
	cfg := Apply(Choices{
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
	cfg := Apply(Choices{
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
