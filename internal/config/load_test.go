// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	p := writeConfig(t, "config.yml", "guard:\n  secret_patterns:\n    - name: x\n      regexp: y\n")

	_, err := Load(p, "")
	if err == nil {
		t.Fatal("Load = nil error, want an unknown-key error")
	}
	var uk UnknownKey
	if !errors.As(err, &uk) {
		t.Fatalf("Load error %T (%v), want an UnknownKey", err, err)
	}
	if uk.Path != "guard.secret_patterns[0].regexp" {
		t.Errorf("Path = %q, want %q", uk.Path, "guard.secret_patterns[0].regexp")
	}
	// The layer that was wrong must be named, not "the config".
	if !strings.Contains(err.Error(), p) {
		t.Errorf("error %q does not name the offending file %q", err.Error(), p)
	}
}

// The repo layer is validated too, and it is named rather than the global one.
func TestLoadRejectsUnknownKeyInRepoLayer(t *testing.T) {
	global := writeConfig(t, "global.yml", "provider: anthropic\n")
	repo := writeConfig(t, "repo.yml", "review:\n  flakey: true\n")

	_, err := Load(global, repo)
	if err == nil {
		t.Fatal("Load = nil error, want an unknown-key error")
	}
	if !strings.Contains(err.Error(), repo) || strings.Contains(err.Error(), global) {
		t.Errorf("error %q should name only the repo file %q", err.Error(), repo)
	}
}

func TestLoadFileRejectsUnknownKey(t *testing.T) {
	p := writeConfig(t, "config.yml", "outputt:\n  lang: tr\n")

	_, err := LoadFile(p)
	if err == nil {
		t.Fatal("LoadFile = nil error, want an unknown-key error")
	}
	var uk UnknownKey
	if !errors.As(err, &uk) {
		t.Fatalf("LoadFile error %T (%v), want an UnknownKey", err, err)
	}
	if uk.Path != "outputt" {
		t.Errorf("Path = %q, want %q", uk.Path, "outputt")
	}
}

// Every key in .ssot/contracts/config-schema.md must still load. This is the
// regression guard against the validator over-rejecting.
func TestLoadStillAcceptsEveryDocumentedKey(t *testing.T) {
	const full = `
version: 1
provider: anthropic
providers:
  anthropic:
    api_key: sk-ant-x
    model: claude-opus-4-8
    base_url: https://api.anthropic.com
    pricing:
      claude-opus-4-8:
        input_per_1m: 15
        output_per_1m: 75
        cached_input_per_1m: 1.5
  deepseek:
    api_key: sk-ds-x
    model: deepseek-chat
output:
  lang: tr
  stream: false
  color: never
cache:
  enabled: true
  ttl_days: 14
  max_size_mb: 256
guard:
  secret_scan: true
  token_preflight: true
  injection_scan: false
  secret_patterns:
    - name: internal token
      regex: "ACME_[A-Z0-9]{32}"
cost:
  warn_threshold_usd: 1.25
command:
  default: "--unstaged --cli gemini"
commit:
  type: conventional
  generate: 3
review:
  flaky: true
  baseline: false
  architecture: true
  architecture_file: arch.json
  sandbox_rerun: 5
  sandbox_command: ["go", "test", "-run", "{{.Test}}"]
  timeout: "10m"
`
	p := writeConfig(t, "config.yml", full)

	cfg, err := Load(p, "")
	if err != nil {
		t.Fatalf("Load = %v, want nil", err)
	}
	if cfg.Output.Lang != "tr" || cfg.Cache.MaxSizeMB != 256 || cfg.Review.Timeout != "10m" {
		t.Errorf("documented keys did not land: lang=%q max_size_mb=%d timeout=%q",
			cfg.Output.Lang, cfg.Cache.MaxSizeMB, cfg.Review.Timeout)
	}
	if len(cfg.Guard.SecretPatterns) != 1 || cfg.Guard.SecretPatterns[0].Regex == "" {
		t.Errorf("guard.secret_patterns did not land: %+v", cfg.Guard.SecretPatterns)
	}
	if _, ok := cfg.Providers["deepseek"]; !ok {
		t.Error("providers.deepseek did not land")
	}

	if _, err := LoadFile(p); err != nil {
		t.Fatalf("LoadFile = %v, want nil", err)
	}
}
