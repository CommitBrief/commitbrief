// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These tests pin the three data losses 06-config-set-write-path.md exists
// to close, on both commands that share the write path (`config set` and
// `providers use`). Each asserts on the FILE CONTENTS, not just the exit
// code — the whole point is that a byte can be lost while the command still
// reports success.

// ---------- (1) zero-value destruction ----------

// newCLIEnv seeds a config with no guard/cost/command/review section at
// all (see writeUserConfig in integration_test.go) — exactly the "missing
// fields" shape the bug needed. A `config set` that decodes into the typed
// Config and marshals the whole thing back out would stamp every one of
// those sections in at its Go zero value (secret_scan: false, cache.enabled
// already true→false is a different field, warn_threshold_usd: 0, ...).
func TestConfigSetDoesNotStampZeroValuedSections(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")

	if err := e.run("config", "set", "output.lang", "tr"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, unwanted := range []string{"secret_scan", "injection_scan", "token_preflight", "warn_threshold_usd", "flaky:", "baseline:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("config set must not stamp unrelated zero-valued fields; found %q in:\n%s", unwanted, out)
		}
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
}

func TestProvidersUseDoesNotStampZeroValuedSections(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")

	if err := e.run("providers", "use", "mock"); err != nil {
		t.Fatalf("providers use mock: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, unwanted := range []string{"secret_scan", "injection_scan", "token_preflight", "warn_threshold_usd", "flaky:", "baseline:"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("providers use must not stamp unrelated zero-valued fields; found %q in:\n%s", unwanted, out)
		}
	}
}

// ---------- (3) x- anchor-holder keys ----------

const xPrefixedAnchorConfig = `version: 1
provider: mock
x-defaults: &d
  ttl_days: 3
cache:
  <<: *d
  enabled: true
`

func TestConfigSetPreservesXPrefixedAnchorKey(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	writeRawUserConfig(t, e.homeDir, xPrefixedAnchorConfig)

	if err := e.run("config", "set", "output.lang", "tr"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "x-defaults:") {
		t.Errorf("x-defaults anchor holder was silently dropped; file:\n%s", out)
	}
	if !strings.Contains(out, "<<:") {
		t.Errorf("the cache merge key was lost; file:\n%s", out)
	}
	if !strings.Contains(out, "lang: tr") {
		t.Errorf("output.lang was not set; file:\n%s", out)
	}
}

func TestProvidersUsePreservesXPrefixedAnchorKey(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	writeRawUserConfig(t, e.homeDir, xPrefixedAnchorConfig)

	if err := e.run("providers", "use", "mock"); err != nil {
		t.Fatalf("providers use mock: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if !strings.Contains(out, "x-defaults:") {
		t.Errorf("x-defaults anchor holder was silently dropped; file:\n%s", out)
	}
	if !strings.Contains(out, "<<:") {
		t.Errorf("the cache merge key was lost; file:\n%s", out)
	}
}

// ---------- comments + first-run skeleton still works ----------

func TestConfigSetPreservesUserComment(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	writeRawUserConfig(t, e.homeDir, "# benim notum\nversion: 1\nprovider: mock\n")

	if err := e.run("config", "set", "output.lang", "tr"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# benim notum") {
		t.Errorf("user comment was dropped; file:\n%s", data)
	}
}

// First-run UX is unchanged: with no config file at all yet, `config set`
// still writes a fully-populated config.Default()-based skeleton (nothing
// on disk to preserve, so there's nothing to lose by doing so) rather than
// a bare one-key document.
func TestConfigSetOnFreshHomeWritesFullSkeleton(t *testing.T) {
	e := newCLIEnv(t)
	cfgPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")
	if err := os.Remove(cfgPath); err != nil {
		t.Fatal(err)
	}

	if err := e.run("config", "set", "output.lang", "tr"); err != nil {
		t.Fatalf("config set: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	for _, want := range []string{"secret_scan", "warn_threshold_usd", "cache:", "lang: tr"} {
		if !strings.Contains(out, want) {
			t.Errorf("fresh-install skeleton missing %q; file:\n%s", want, out)
		}
	}
}
