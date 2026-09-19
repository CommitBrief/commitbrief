// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
// existed. Without this control, TestRunIgnoreUnknownKeysReachesThePrompt
// below would not prove anything — both cases would look identical if Run
// stopped validating keys at all.
func TestRunStrictFailsOnUnknownConfigKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	writeSetupTestConfig(t, path, unknownKeySetupConfig)

	_, err := Run(context.Background(), RunOptions{GlobalPath: path})
	if err == nil {
		t.Fatal("Run must fail on an unknown config key when IgnoreUnknownKeys is false")
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
// Run cannot be driven fully headless (selectProvider et al. need a real
// TTY), but the config load happens BEFORE any prompt, so the assertion that
// actually matters is reachable without one: with IgnoreUnknownKeys true,
// Run must get past the load and fail for an entirely different reason (no
// TTY here), never for the unknown key. The short timeout is a safety net,
// not a requirement — huh fails to open a TTY near-instantly in this
// environment.
func TestRunIgnoreUnknownKeysReachesThePrompt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	writeSetupTestConfig(t, path, unknownKeySetupConfig)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := Run(ctx, RunOptions{GlobalPath: path, IgnoreUnknownKeys: true})
	if err == nil {
		t.Fatal("Run should still fail in this headless test environment (no TTY) — " +
			"if it now succeeds, this test needs a different way to prove the load got past config validation")
	}
	if strings.Contains(err.Error(), "unknown key") {
		t.Errorf("IgnoreUnknownKeys=true must let the config load proceed; "+
			"got a config error instead of an interactive-prompt failure: %v", err)
	}
}
