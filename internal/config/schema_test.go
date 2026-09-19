// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// decodeYAML is the same shape readLayer produces: a free-form map decoded
// straight from the user's file, before any typed decode has had a chance to
// silently drop the keys we are here to catch.
func decodeYAML(t *testing.T, src string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := yaml.Unmarshal([]byte(src), &m); err != nil {
		t.Fatalf("yaml.Unmarshal: %v", err)
	}
	return m
}

func paths(keys []UnknownKey) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.Path)
	}
	return out
}

// The strongest single case: whatever Default() serializes to must validate
// clean. If the reflection walk is wrong in any direction this fails first.
func TestValidateKeysAcceptsTheDefaultConfig(t *testing.T) {
	b, err := yaml.Marshal(Default())
	if err != nil {
		t.Fatalf("marshal defaults: %v", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal defaults: %v", err)
	}
	if got := ValidateKeys(m, "defaults"); len(got) != 0 {
		t.Fatalf("ValidateKeys(Default()) = %v, want none", paths(got))
	}
}

func TestValidateKeysRejectsUnknownSection(t *testing.T) {
	m := decodeYAML(t, "revieww:\n  flaky: true\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	if got[0].Path != "revieww" {
		t.Errorf("Path = %q, want %q", got[0].Path, "revieww")
	}
	if !contains(got[0].Allowed, "review") {
		t.Errorf("Allowed = %v, want it to list the real section %q", got[0].Allowed, "review")
	}
}

func TestValidateKeysRejectsUnknownKeyInSection(t *testing.T) {
	m := decodeYAML(t, "guard:\n  secert_scan: true\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	if got[0].Path != "guard.secert_scan" {
		t.Errorf("Path = %q, want %q", got[0].Path, "guard.secert_scan")
	}
	if !contains(got[0].Allowed, "secret_scan") {
		t.Errorf("Allowed = %v, want it to list %q", got[0].Allowed, "secret_scan")
	}
}

// ACTION_PLAN §0.3's headline case: `regexp:` instead of `regex:` used to
// yield a SecretPatternConfig with an empty Regex — silently unprotected.
func TestValidateKeysRejectsUnknownKeyInSecretPattern(t *testing.T) {
	m := decodeYAML(t, `
guard:
  secret_patterns:
    - name: internal token
      regexp: "ACME_[A-Z0-9]{32}"
`)
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	if got[0].Path != "guard.secret_patterns[0].regexp" {
		t.Errorf("Path = %q, want %q", got[0].Path, "guard.secret_patterns[0].regexp")
	}
	if len(got[0].Allowed) != 2 || !contains(got[0].Allowed, "name") || !contains(got[0].Allowed, "regex") {
		t.Errorf("Allowed = %v, want [name regex]", got[0].Allowed)
	}
}

// providers.<name> is user-chosen: Default() seeds four, but deepseek,
// mistral, cohere and the three CLI providers are all legal.
func TestValidateKeysAllowsArbitraryProviderNames(t *testing.T) {
	m := decodeYAML(t, `
providers:
  deepseek:
    api_key: sk-x
    model: deepseek-chat
    base_url: https://api.deepseek.com
  some-future-provider:
    model: m
`)
	if got := ValidateKeys(m, "cfg.yml"); len(got) != 0 {
		t.Fatalf("ValidateKeys = %v, want none", paths(got))
	}

	// The map key is free, but the struct under it is not.
	bad := decodeYAML(t, "providers:\n  deepseek:\n    api_kye: sk-x\n")
	got := ValidateKeys(bad, "cfg.yml")
	if len(got) != 1 || got[0].Path != "providers.deepseek.api_kye" {
		t.Fatalf("ValidateKeys = %v, want [providers.deepseek.api_kye]", paths(got))
	}
}

func TestValidateKeysAllowsArbitraryPricingModelIDs(t *testing.T) {
	m := decodeYAML(t, `
providers:
  mistral:
    pricing:
      mistral-large-2512:
        input_per_1m: 2.0
        output_per_1m: 6.0
      "some/vendor:tag-v9":
        cached_input_per_1m: 0.25
`)
	if got := ValidateKeys(m, "cfg.yml"); len(got) != 0 {
		t.Fatalf("ValidateKeys = %v, want none", paths(got))
	}

	bad := decodeYAML(t, "providers:\n  mistral:\n    pricing:\n      m1:\n        input_per_1k: 2.0\n")
	got := ValidateKeys(bad, "cfg.yml")
	if len(got) != 1 || got[0].Path != "providers.mistral.pricing.m1.input_per_1k" {
		t.Fatalf("ValidateKeys = %v, want [providers.mistral.pricing.m1.input_per_1k]", paths(got))
	}
}

func TestValidateKeysErrorNamesSourceAndAllowedSet(t *testing.T) {
	m := decodeYAML(t, "guard:\n  secret_patterns:\n    - name: x\n      regexp: y\n")
	got := ValidateKeys(m, "/home/u/.commitbrief/config.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	want := `config: /home/u/.commitbrief/config.yml: unknown key "guard.secret_patterns[0].regexp" (allowed: name, regex)`
	if got[0].Error() != want {
		t.Errorf("Error() =\n  %s\nwant\n  %s", got[0].Error(), want)
	}
}

// Findings must come back in a stable order even though Go randomizes map
// iteration; otherwise the error a user sees varies run to run.
func TestValidateKeysReturnsFindingsInStableOrder(t *testing.T) {
	src := "zzz: 1\naaa: 2\nguard:\n  bbb: 3\n"
	want := []string{"aaa", "guard.bbb", "zzz"}
	for i := 0; i < 20; i++ {
		got := paths(ValidateKeys(decodeYAML(t, src), "cfg.yml"))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("iteration %d: got %v, want %v", i, got, want)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
