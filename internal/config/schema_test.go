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

// A top-level `x-`-prefixed key exists purely to hold a YAML anchor for
// `<<:` merging — a pattern the strict validator broke because an anchor
// needs *some* key to hang off of. The user-approved fix (2026-09-19) is to
// exempt that one shape rather than reopen top-level validation generally.
func TestValidateKeysAllowsTopLevelXPrefixedAnchorHolder(t *testing.T) {
	m := decodeYAML(t, `
x-defaults: &d
  ttl_days: 3
cache:
  <<: *d
  enabled: true
`)
	if got := ValidateKeys(m, "cfg.yml"); len(got) != 0 {
		t.Fatalf("ValidateKeys = %v, want none (x- prefixed top-level keys are exempt)", paths(got))
	}
}

// The same shape without the "x-" prefix is exactly the typo class the
// validator exists to catch, so it must keep failing — and the error must
// point the user at the escape valve that would have let it through.
func TestValidateKeysRejectsTopLevelKeyWithoutXPrefix(t *testing.T) {
	m := decodeYAML(t, `
defaults: &d
  ttl_days: 3
cache:
  <<: *d
  enabled: true
`)
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	if got[0].Path != "defaults" {
		t.Errorf("Path = %q, want %q", got[0].Path, "defaults")
	}
	if !strings.Contains(got[0].Error(), "x-defaults") {
		t.Errorf("Error() = %q, want it to suggest the x- prefix (\"x-defaults\")", got[0].Error())
	}
}

// The x- exemption is deliberately top-level only: inside a known section
// an "x-" key is far more likely a typo than an anchor holder, so it must
// still be rejected there.
func TestValidateKeysRejectsXPrefixedKeyInsideKnownSection(t *testing.T) {
	m := decodeYAML(t, "guard:\n  x-secret_scan: true\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	if got[0].Path != "guard.x-secret_scan" {
		t.Errorf("Path = %q, want %q", got[0].Path, "guard.x-secret_scan")
	}
	if strings.Contains(got[0].Error(), "x-x-secret_scan") {
		t.Errorf("Error() = %q, must not suggest an x- prefix for a non-top-level key", got[0].Error())
	}
}

// Wave 0 review turu 2, item 8: the "x-" hint is a silencer — suggesting it
// for an actual typo would make the typo permanent (the config never takes
// effect, but the error never comes back either). A key close enough to a
// real one must get "did you mean" instead, never the x- hint.
// Review turu 3, item 12: a near-miss and a genuine anchor holder are not
// mutually exclusive (`common: &d` is 2 edits from "command"), so both
// hints must be offered — the "did you mean" suggestion for the likely-typo
// case, and the x- prefix for the case where the key really was meant to
// hold nothing but an anchor.
func TestValidateKeysOffersBothHintsWhenNearMiss(t *testing.T) {
	m := decodeYAML(t, "cahce:\n  enabled: true\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	errMsg := got[0].Error()
	if !strings.Contains(errMsg, `did you mean "cache"`) {
		t.Errorf("Error() = %q, want a \"did you mean\" suggestion for the near-miss \"cache\"", errMsg)
	}
	if !strings.Contains(errMsg, "x-cahce") {
		t.Errorf("Error() = %q, want the x- prefix also offered alongside the near-miss suggestion", errMsg)
	}
}

// The exact scenario item 12 calls out: a real anchor-holder name that
// happens to be 2 edits from an allowed key. The "did you mean" guess is
// wrong here, so the x- route must still be reachable.
func TestValidateKeysAnchorHolderNearMissStillOffersXPrefix(t *testing.T) {
	m := decodeYAML(t, "common: &d\n  ttl_days: 3\ncache:\n  <<: *d\n  enabled: true\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	errMsg := got[0].Error()
	if !strings.Contains(errMsg, `did you mean "command"`) {
		t.Errorf("Error() = %q, want the near-miss suggestion for \"common\"", errMsg)
	}
	if !strings.Contains(errMsg, "x-common") {
		t.Errorf("Error() = %q, must still offer the x- route — \"common\" here is a genuine anchor holder, not a typo of \"command\"", errMsg)
	}
}

// Review turu 3, item 13: an empty key has no actionable "x-" spelling
// (`try "x-"` tells the user nothing) and nothing plausible to near-match,
// so neither hint should appear.
func TestValidateKeysSkipsHintForEmptyKey(t *testing.T) {
	m := decodeYAML(t, "\"\": true\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	errMsg := got[0].Error()
	if strings.Contains(errMsg, "x-") {
		t.Errorf("Error() = %q, must not suggest an x- prefix for an empty key", errMsg)
	}
	if strings.Contains(errMsg, "did you mean") {
		t.Errorf("Error() = %q, must not offer a near-miss suggestion for an empty key", errMsg)
	}
}

// A key that is NOT a plausible typo of anything real keeps the x- hint —
// this is the control for the test above and re-covers the exact case item
// 2's decision introduced (a deliberate anchor-holder name, not a mistake).
func TestValidateKeysKeepsXPrefixHintWhenNoNearMiss(t *testing.T) {
	m := decodeYAML(t, "defaults:\n  ttl_days: 3\n")
	got := ValidateKeys(m, "cfg.yml")
	if len(got) != 1 {
		t.Fatalf("ValidateKeys = %v, want exactly one finding", paths(got))
	}
	errMsg := got[0].Error()
	if strings.Contains(errMsg, "did you mean") {
		t.Errorf("Error() = %q, \"defaults\" should not near-match any allowed top-level key", errMsg)
	}
	if !strings.Contains(errMsg, "x-defaults") {
		t.Errorf("Error() = %q, want the x- prefix hint when there is no near miss", errMsg)
	}
}

func TestLevenshteinKnownDistances(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"cache", "cache", 0},
		{"", "cache", 5},
		{"cahce", "cache", 2},
		{"revieww", "review", 1},
		{"kitten", "sitting", 3},
	}
	for _, c := range cases {
		if got := levenshtein(c.a, c.b); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := levenshtein(c.b, c.a); got != c.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d (symmetry)", c.b, c.a, got, c.want)
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
