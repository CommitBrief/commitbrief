package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefault(t *testing.T) {
	c := Default()
	if c.Version != CurrentSchemaVersion {
		t.Errorf("Version = %d, want %d", c.Version, CurrentSchemaVersion)
	}
	if c.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", c.Provider, "anthropic")
	}
	for _, name := range []string{"anthropic", "openai", "gemini", "ollama"} {
		if _, ok := c.Providers[name]; !ok {
			t.Errorf("Providers missing %q", name)
		}
	}
	if c.Output.Lang != "en" {
		t.Errorf("Output.Lang = %q, want %q", c.Output.Lang, "en")
	}
	if !c.Output.Stream {
		t.Error("Output.Stream = false, want true")
	}
	if c.Cache.TTLDays != 7 {
		t.Errorf("Cache.TTLDays = %d, want 7", c.Cache.TTLDays)
	}
}

func TestLoadNoFiles(t *testing.T) {
	c, err := Load("", "")
	if err != nil {
		t.Fatalf("Load(\"\",\"\"): %v", err)
	}
	if c.Provider != "anthropic" {
		t.Errorf("Provider = %q, want default %q", c.Provider, "anthropic")
	}
}

func TestLoadMissingFilesFallsThroughToDefaults(t *testing.T) {
	c, err := Load("/nonexistent/global.yml", "/nonexistent/repo.yml")
	if err != nil {
		t.Fatalf("missing files should not error: %v", err)
	}
	if c.Provider != "anthropic" {
		t.Errorf("Provider = %q, want default", c.Provider)
	}
}

func TestLoadGlobalOnly(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yml")
	writeFile(t, global, `
version: 1
provider: openai
providers:
  openai:
    api_key: sk-global
    model: gpt-4o
output:
  lang: tr
`)
	c, err := Load(global, "")
	if err != nil {
		t.Fatal(err)
	}
	if c.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", c.Provider)
	}
	if c.Providers["openai"].APIKey != "sk-global" {
		t.Errorf("OpenAI APIKey = %q, want sk-global", c.Providers["openai"].APIKey)
	}
	if c.Output.Lang != "tr" {
		t.Errorf("Lang = %q, want tr", c.Output.Lang)
	}
	if !c.Output.Stream {
		t.Error("Stream should inherit default true; got false")
	}
}

func TestLoadRepoOverridesField(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yml")
	repo := filepath.Join(dir, "repo.yml")
	writeFile(t, global, `
provider: anthropic
providers:
  anthropic:
    api_key: sk-ant-key
    model: claude-opus-4-7
output:
  lang: en
  stream: true
`)
	writeFile(t, repo, `
output:
  lang: tr
`)
	c, err := Load(global, repo)
	if err != nil {
		t.Fatal(err)
	}
	if c.Output.Lang != "tr" {
		t.Errorf("Lang = %q, want tr (from repo)", c.Output.Lang)
	}
	if !c.Output.Stream {
		t.Error("Stream lost — should inherit true from global")
	}
	if c.Providers["anthropic"].APIKey != "sk-ant-key" {
		t.Error("api_key lost — should inherit from global since repo did not touch providers")
	}
}

func TestLoadRepoFieldOverrideKeepsSiblings(t *testing.T) {
	dir := t.TempDir()
	global := filepath.Join(dir, "global.yml")
	repo := filepath.Join(dir, "repo.yml")
	writeFile(t, global, `
providers:
  anthropic:
    api_key: sk-ant-key
    model: claude-opus-4-7
    base_url: https://api.anthropic.com
`)
	writeFile(t, repo, `
providers:
  anthropic:
    model: claude-sonnet-4-6
`)
	c, err := Load(global, repo)
	if err != nil {
		t.Fatal(err)
	}
	got := c.Providers["anthropic"]
	if got.Model != "claude-sonnet-4-6" {
		t.Errorf("Model = %q, want override claude-sonnet-4-6", got.Model)
	}
	if got.APIKey != "sk-ant-key" {
		t.Errorf("APIKey = %q, want inherited sk-ant-key (field-level merge broken)", got.APIKey)
	}
	if got.BaseURL != "https://api.anthropic.com" {
		t.Errorf("BaseURL = %q, want inherited (field-level merge broken)", got.BaseURL)
	}
}

func TestLoadBadYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yml")
	writeFile(t, bad, "provider: [unclosed")
	if _, err := Load(bad, ""); err == nil {
		t.Error("expected error for malformed YAML, got nil")
	}
}

func TestLoadFutureVersionErrors(t *testing.T) {
	dir := t.TempDir()
	future := filepath.Join(dir, "future.yml")
	writeFile(t, future, "version: 99\nprovider: anthropic\n")
	if _, err := Load(future, ""); err == nil {
		t.Error("expected error for future schema version, got nil")
	}
}

func TestApplyEnvOverridesProvider(t *testing.T) {
	t.Setenv("COMMITBRIEF_PROVIDER", "ollama")
	c := Default()
	ApplyEnv(c)
	if c.Provider != "ollama" {
		t.Errorf("Provider = %q, want ollama from env", c.Provider)
	}
}

func TestApplyEnvOverridesAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-env-key")
	c := Default()
	ApplyEnv(c)
	if c.Providers["anthropic"].APIKey != "sk-env-key" {
		t.Errorf("APIKey not overridden by ANTHROPIC_API_KEY")
	}
}

func TestApplyEnvModelTargetsActiveProvider(t *testing.T) {
	t.Setenv("COMMITBRIEF_PROVIDER", "openai")
	t.Setenv("COMMITBRIEF_MODEL", "gpt-5")
	c := Default()
	ApplyEnv(c)
	if c.Providers["openai"].Model != "gpt-5" {
		t.Errorf("OpenAI Model = %q, want gpt-5", c.Providers["openai"].Model)
	}
	if c.Providers["anthropic"].Model == "gpt-5" {
		t.Error("COMMITBRIEF_MODEL leaked into anthropic provider; should only touch active provider")
	}
}

func TestApplyEnvOllamaHost(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "http://gpu.lan:11434")
	c := Default()
	ApplyEnv(c)
	if c.Providers["ollama"].BaseURL != "http://gpu.lan:11434" {
		t.Errorf("Ollama BaseURL not overridden by OLLAMA_HOST: %q", c.Providers["ollama"].BaseURL)
	}
}

func TestApplyEnvOverridesRepo(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo.yml")
	writeFile(t, repo, `
provider: anthropic
providers:
  anthropic:
    api_key: sk-from-repo
`)
	c, err := Load("", repo)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANTHROPIC_API_KEY", "sk-from-env")
	ApplyEnv(c)
	if c.Providers["anthropic"].APIKey != "sk-from-env" {
		t.Errorf("ENV did not override repo: APIKey = %q, want sk-from-env", c.Providers["anthropic"].APIKey)
	}
}

func TestLoadFileMissingReturnsNil(t *testing.T) {
	c, err := LoadFile("/does/not/exist.yml")
	if err != nil {
		t.Fatalf("LoadFile missing: %v", err)
	}
	if c != nil {
		t.Errorf("LoadFile missing should return nil, got %+v", c)
	}
}

func TestLoadFileEmptyPathReturnsNil(t *testing.T) {
	c, err := LoadFile("")
	if err != nil || c != nil {
		t.Errorf("LoadFile(\"\") = (%+v, %v), want (nil, nil)", c, err)
	}
}

func TestLoadFileRawNoDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.yml")
	writeFile(t, path, `
output:
  lang: tr
`)
	c, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Output.Lang != "tr" {
		t.Errorf("Lang = %q, want tr", c.Output.Lang)
	}
	if c.Provider != "" {
		t.Errorf("Provider = %q, want empty (no defaults applied in LoadFile)", c.Provider)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
