package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/config"
)

func TestProvidersList(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("providers", "list"); err != nil {
		t.Fatalf("providers list: %v", err)
	}
	out := e.out.String()

	for _, want := range []string{"Configured providers", "mock"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n%s", want, out)
		}
	}
	// Active provider gets the '*' marker on its line.
	if !strings.Contains(out, "* mock") {
		t.Errorf("active provider should be flagged with '*'; got:\n%s", out)
	}
	// Registered-but-unconfigured providers still appear so the user can see
	// what's possible to set up.
	for _, name := range []string{"anthropic", "openai", "gemini", "ollama"} {
		if !strings.Contains(out, name) {
			t.Errorf("provider %q missing from list; got:\n%s", name, out)
		}
	}
}

func TestProvidersListMasksAPIKey(t *testing.T) {
	// Write a config whose mock key is long enough to be masked rather than
	// summarised as "(configured)".
	cfgDir := filepath.Join(t.TempDir(), ".commitbrief")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const verboseKey = "sk-fake-abcdefghijklmnop1234"
	body := "version: 1\nprovider: mock\nproviders:\n  mock:\n    api_key: " + verboseKey + "\n    model: mock-model\noutput:\n  lang: en\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	e := newCLIEnv(t)
	t.Setenv("HOME", filepath.Dir(cfgDir))
	t.Setenv("USERPROFILE", filepath.Dir(cfgDir))

	if err := e.run("providers", "list"); err != nil {
		t.Fatalf("providers list: %v", err)
	}
	out := e.out.String()

	// The verbose key must not appear verbatim — that's the whole point of
	// the mask.
	if strings.Contains(out, verboseKey) {
		t.Errorf("API key leaked verbatim into output:\n%s", out)
	}
}

func TestProvidersUseSwitchesActive(t *testing.T) {
	// Integration tests only register `mock` (real providers are blank-
	// imported by cmd/commitbrief, not test binaries). To exercise the
	// switch-back path we flip the seeded config to a placeholder name
	// and then `use mock` brings it back. Other-provider keys staying
	// intact across the switch is covered by TestProvidersUseKeepsOtherKeys.
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	setActiveInConfig(t, cfgPath, "placeholder")

	if err := e.run("providers", "use", "mock"); err != nil {
		t.Fatalf("providers use mock: %v", err)
	}
	if !strings.Contains(e.out.String(), `"mock"`) {
		t.Errorf("success message should name the new provider; got:\n%s", e.out.String())
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "mock" {
		t.Errorf("persisted Provider = %q, want mock", cfg.Provider)
	}
}

func TestProvidersUseKeepsOtherKeys(t *testing.T) {
	// Switching the active provider must not touch any provider's APIKey,
	// Model, or BaseURL — `use` is a pointer flip, nothing more.
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	// Seed two extra entries the registry doesn't know about; they should
	// still round-trip untouched.
	addProviderToConfig(t, cfgPath, "anthropic", "sk-ant-seeded", "claude-opus-4-7")
	addProviderToConfig(t, cfgPath, "gemini", "AIza-seeded", "gemini-2.5-pro")
	setActiveInConfig(t, cfgPath, "anthropic")

	if err := e.run("providers", "use", "mock"); err != nil {
		t.Fatalf("providers use mock: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "mock" {
		t.Errorf("Provider = %q, want mock", cfg.Provider)
	}
	if got := cfg.Providers["anthropic"].APIKey; got != "sk-ant-seeded" {
		t.Errorf("anthropic key lost across `use`: got %q", got)
	}
	if got := cfg.Providers["gemini"].APIKey; got != "AIza-seeded" {
		t.Errorf("gemini key lost across `use`: got %q", got)
	}
}

func TestProvidersUseUnknownErrors(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("providers", "use", "no-such-provider")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error %q does not mention 'unknown provider'", err.Error())
	}
}

func TestProvidersTestSuccess(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("providers", "test", "mock"); err != nil {
		t.Fatalf("providers test mock: %v", err)
	}
	out := e.out.String()
	if !strings.Contains(out, "mock: ok") {
		t.Errorf("expected success line; got:\n%s", out)
	}
}

func TestProvidersTestUnknownErrors(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("providers", "test", "no-such-provider")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"long key", "sk-ant-abcdefghij1234", "sk-ant-…1234"},
		{"short key", "sk-ant", "(configured)"},
		{"empty", "", "(configured)"},
		{"exact 11 chars", "sk-ant-abcd", "(configured)"},
		{"exact 12 chars", "sk-ant-abcde", "sk-ant-…bcde"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := maskAPIKey(tc.in); got != tc.want {
				t.Errorf("maskAPIKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func setActiveInConfig(t *testing.T, path, name string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Provider = name
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func addProviderToConfig(t *testing.T, path, name, apiKey, model string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Providers == nil {
		cfg.Providers = map[string]config.ProviderConfig{}
	}
	cfg.Providers[name] = config.ProviderConfig{APIKey: apiKey, Model: model}
	out, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
