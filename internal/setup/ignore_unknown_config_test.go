// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The config below carries the same shape ACTION_PLAN and internal/cli's own
// ignore_unknown_config_test.go use: `pattern:` where guard.secret_patterns
// takes `regex:`.
const unknownKeySetupConfig = `version: 1
provider: mock
guard:
  secret_patterns:
    - name: internal token
      pattern: 'ACME_[A-Z0-9]{32}'
`

func writeSetupTestConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Baseline: Run must still fail closed on an unknown key when
// IgnoreUnknownKeys is false (the default), same as before the escape hatch
// existed. Without this control, TestLoadRunConfigIgnoresUnknownKey below
// would not prove anything — both cases would look identical if Run stopped
// validating keys at all.
//
// This asserts through LoadRunConfig (the exact pre-prompt step Run itself
// runs first) rather than through Run, so it never touches huh — see
// LoadRunConfig's doc comment for why driving that through the real prompt
// is not viable on every platform.
func TestRunStrictFailsOnUnknownConfigKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	writeSetupTestConfig(t, path, unknownKeySetupConfig)

	_, _, err := LoadRunConfig(RunOptions{GlobalPath: path})
	if err == nil {
		t.Fatal("LoadRunConfig must fail on an unknown config key when IgnoreUnknownKeys is false")
	}
	if !strings.Contains(err.Error(), "guard.secret_patterns[0].pattern") {
		t.Errorf("error must name the offending key; got: %v", err)
	}
}

// Wave 0 review turu 2, item 7: `setup.RunOptions.IgnoreUnknownKeys` was
// wired in item 1 but never actually exercised by a test — a regression
// that silently reverted LoadFileWith back to the strict LoadFile (relocking
// setup, the one command the escape hatch exists to rescue) would have kept
// every existing test green.
//
// This used to drive the assertion through the real Run, relying on huh
// failing to open a TTY near-instantly in a headless test environment. That
// held on Linux/macOS (no TTY at all) but not on Windows CI, which still has
// a console attached: huh opened it and blocked on a real console read,
// which reads as a hang until the 10-minute per-package test timeout kills
// the whole binary. Asserting through LoadRunConfig directly — the exact
// pre-prompt step Run itself runs first — proves the same thing (config
// validation let an ignored unknown key through) without ever constructing
// a prompt, so it is deterministic on every platform.
func TestLoadRunConfigIgnoresUnknownKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	writeSetupTestConfig(t, path, unknownKeySetupConfig)

	_, base, err := LoadRunConfig(RunOptions{GlobalPath: path, IgnoreUnknownKeys: true})
	if err != nil {
		t.Fatalf("IgnoreUnknownKeys=true must let the config load proceed; got: %v", err)
	}
	if base == nil {
		t.Fatal("expected the loaded config back, got nil")
	}
	if base.Provider != "mock" {
		t.Errorf("expected the rest of the file's keys to still apply; got provider %q", base.Provider)
	}
}
