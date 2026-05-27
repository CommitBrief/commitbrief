// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/config"
)

// ---------- config show ----------

func TestConfigShowMasksAPIKeys(t *testing.T) {
	e := newCLIEnv(t)
	// Seed a long key so masking kicks in (short keys would just print
	// "(configured)" which would also not leak, but we want to verify the
	// substring path explicitly).
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	const verboseKey = "sk-anthropic-xxxxxxxxxxxxxxxxLAST"
	addProviderToConfig(t, cfgPath, "anthropic", verboseKey, "claude-opus-4-7")

	if err := e.run("config", "show"); err != nil {
		t.Fatalf("config show: %v", err)
	}
	out := e.out.String()
	if strings.Contains(out, verboseKey) {
		t.Errorf("API key leaked into `config show` output:\n%s", out)
	}
	if !strings.Contains(out, "anthropic") {
		t.Errorf("provider name should still appear:\n%s", out)
	}
}

func TestConfigShowEmitsValidYAML(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "show"); err != nil {
		t.Fatalf("config show: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(e.out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not valid YAML: %v\n%s", err, e.out.String())
	}
	for _, key := range []string{"version", "provider", "providers", "output", "cache"} {
		if _, ok := doc[key]; !ok {
			t.Errorf("config show missing top-level key %q", key)
		}
	}
}

// ---------- config get ----------

func TestConfigGetTopLevel(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "get", "provider"); err != nil {
		t.Fatalf("config get provider: %v", err)
	}
	if got := strings.TrimSpace(e.out.String()); got != "mock" {
		t.Errorf("provider = %q, want mock", got)
	}
}

func TestConfigGetNestedProvider(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "get", "providers.mock.model"); err != nil {
		t.Fatalf("config get providers.mock.model: %v", err)
	}
	if got := strings.TrimSpace(e.out.String()); got != "mock-model" {
		t.Errorf("providers.mock.model = %q, want mock-model", got)
	}
}

func TestConfigGetOutputAndCache(t *testing.T) {
	e := newCLIEnv(t)
	cases := []struct {
		key, want string
	}{
		{"output.lang", "en"},
		{"output.stream", "false"},
		{"cache.enabled", "true"},
		{"cache.ttl_days", "7"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			e.out.Reset()
			if err := e.run("config", "get", tc.key); err != nil {
				t.Fatalf("config get %s: %v", tc.key, err)
			}
			if got := strings.TrimSpace(e.out.String()); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.key, got, tc.want)
			}
		})
	}
}

func TestConfigGetUnknownKey(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "get", "this.is.bogus")
	if err == nil {
		t.Fatal("want error for bogus key, got nil")
	}
}

func TestConfigGetMaxSizeMBNoLongerSupported(t *testing.T) {
	// UC-02 cleanup: cache.max_size_mb was dead config — defined in
	// the struct but never read anywhere. It is gone in v0.9.1, so
	// `config get cache.max_size_mb` must now error with the standard
	// "unknown field" message rather than silently returning a number.
	e := newCLIEnv(t)
	err := e.run("config", "get", "cache.max_size_mb")
	if err == nil {
		t.Fatalf("max_size_mb should error after removal; got success: %s", e.out.String())
	}
	if !strings.Contains(err.Error(), "max_size_mb") || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error %q should name the offending field as unknown", err.Error())
	}
}

func TestConfigSetMaxSizeMBNoLongerSupported(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "set", "cache.max_size_mb", "200")
	if err == nil {
		t.Fatal("max_size_mb set should error after removal")
	}
	if !strings.Contains(err.Error(), "max_size_mb") || !strings.Contains(err.Error(), "unknown field") {
		t.Errorf("error %q should name the offending field as unknown", err.Error())
	}
}

func TestConfigGetUnknownProvider(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "get", "providers.nonexistent.model")
	if err == nil {
		t.Fatal("want error for unknown provider, got nil")
	}
}

// ---------- config set ----------

func TestConfigSetStringField(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "set", "providers.mock.model", "custom-model"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	cfg := loadCfg(t, e.homeDir)
	if got := cfg.Providers["mock"].Model; got != "custom-model" {
		t.Errorf("persisted model = %q, want custom-model", got)
	}
}

func TestConfigSetBool(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "set", "cache.enabled", "false"); err != nil {
		t.Fatalf("config set cache.enabled false: %v", err)
	}
	cfg := loadCfg(t, e.homeDir)
	if cfg.Cache.Enabled {
		t.Error("cache.enabled should be false after set; still true")
	}
}

func TestConfigSetBoolAliases(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"true", true},
		{"yes", true},
		{"1", true},
		{"on", true},
		{"false", false},
		{"no", false},
		{"0", false},
		{"off", false},
		{"TRUE", true}, // case-insensitive
		{"False", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			e := newCLIEnv(t)
			if err := e.run("config", "set", "cache.enabled", tc.input); err != nil {
				t.Fatalf("config set: %v", err)
			}
			cfg := loadCfg(t, e.homeDir)
			if cfg.Cache.Enabled != tc.want {
				t.Errorf("input %q → cache.enabled = %v, want %v", tc.input, cfg.Cache.Enabled, tc.want)
			}
		})
	}
}

func TestConfigSetInt(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "set", "cache.ttl_days", "30"); err != nil {
		t.Fatalf("config set cache.ttl_days 30: %v", err)
	}
	cfg := loadCfg(t, e.homeDir)
	if cfg.Cache.TTLDays != 30 {
		t.Errorf("cache.ttl_days = %d, want 30", cfg.Cache.TTLDays)
	}
}

func TestConfigSetIntRejectsNonNumeric(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "set", "cache.ttl_days", "thirty")
	if err == nil {
		t.Fatal("want error for non-numeric ttl_days, got nil")
	}
	if !strings.Contains(err.Error(), "integer") {
		t.Errorf("error %q should mention integer", err.Error())
	}
}

func TestConfigSetIntRejectsNegative(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "set", "cache.ttl_days", "-1")
	if err == nil {
		t.Fatal("want error for negative ttl_days, got nil")
	}
}

func TestConfigSetColorEnum(t *testing.T) {
	e := newCLIEnv(t)
	if err := e.run("config", "set", "output.color", "always"); err != nil {
		t.Fatalf("config set output.color always: %v", err)
	}
	cfg := loadCfg(t, e.homeDir)
	if cfg.Output.Color != "always" {
		t.Errorf("output.color = %q, want always", cfg.Output.Color)
	}
}

func TestConfigSetColorRejectsInvalid(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "set", "output.color", "rainbow")
	if err == nil {
		t.Fatal("want error for invalid color, got nil")
	}
	if !strings.Contains(err.Error(), "auto/always/never") {
		t.Errorf("error %q should hint at allowed values", err.Error())
	}
}

func TestConfigSetProviderValidates(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "set", "provider", "no-such-provider")
	if err == nil {
		t.Fatal("want error for unknown provider, got nil")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error %q should mention unknown provider", err.Error())
	}
}

func TestConfigSetVersionRejected(t *testing.T) {
	e := newCLIEnv(t)
	err := e.run("config", "set", "version", "2")
	if err == nil {
		t.Fatal("want error setting version, got nil")
	}
	if !strings.Contains(err.Error(), "migrations") {
		t.Errorf("error %q should reference migrations", err.Error())
	}
}

func TestConfigSetUnknownFieldErrors(t *testing.T) {
	cases := []string{
		"output.bogus",
		"cache.bogus",
		"providers.mock.bogus",
		"bogus",
	}
	for _, key := range cases {
		t.Run(key, func(t *testing.T) {
			e := newCLIEnv(t)
			err := e.run("config", "set", key, "value")
			if err == nil {
				t.Fatalf("want error for %q, got nil", key)
			}
		})
	}
}

func TestConfigSetCreatesProvider(t *testing.T) {
	// Setting providers.<newname>.api_key should create the map entry —
	// useful when scripting setup of a provider without running the wizard.
	e := newCLIEnv(t)
	if err := e.run("config", "set", "providers.anthropic.api_key", "sk-ant-scripted"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	cfg := loadCfg(t, e.homeDir)
	if got := cfg.Providers["anthropic"].APIKey; got != "sk-ant-scripted" {
		t.Errorf("api_key = %q, want sk-ant-scripted", got)
	}
}

func TestConfigSetPreservesOtherFields(t *testing.T) {
	// The cardinal rule: setting one field must not clobber others.
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	addProviderToConfig(t, cfgPath, "anthropic", "sk-ant-existing", "claude-opus-4-7")

	if err := e.run("config", "set", "cache.ttl_days", "30"); err != nil {
		t.Fatalf("config set: %v", err)
	}
	cfg := loadCfg(t, e.homeDir)
	if got := cfg.Providers["anthropic"].APIKey; got != "sk-ant-existing" {
		t.Errorf("anthropic api_key lost: got %q", got)
	}
	if got := cfg.Providers["mock"].APIKey; got != "test" {
		t.Errorf("mock api_key lost: got %q", got)
	}
}

// ---------- helpers ----------

func loadCfg(t *testing.T, home string) *config.Config {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".commitbrief", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	return &cfg
}
