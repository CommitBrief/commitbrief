// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/setup"
)

// The config below is the exact shape the (then-wrong) site docs advertised:
// `pattern:` where guard.secret_patterns takes `regex:`. Before the strict
// validator it decoded to a zero-valued pattern, so the user believed they
// had a custom secret rule and had none.
const unknownKeyUserConfig = `version: 1
provider: mock
providers:
  mock:
    api_key: test
    model: lenient-model
output:
  lang: en
  color: never
guard:
  secret_patterns:
    - name: Acme Internal Token
      pattern: 'acme_[A-Za-z0-9]{32}'
`

// writeRawUserConfig overwrites the sandbox HOME's config.yml verbatim.
// newCLIEnv seeds a valid one; these tests need a deliberately broken file.
func writeRawUserConfig(t *testing.T, home, content string) {
	t.Helper()
	cfgDir := filepath.Join(home, ".commitbrief")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.yml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// captureStderr swaps the process stderr for a pipe while fn runs.
// resolveContext has no cobra command in hand (it is called from ~30 call
// sites, most of them without one), so its warning goes to os.Stderr
// directly and cmd.SetErr cannot see it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w

	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	func() {
		// Restore even if fn calls t.Fatal (runtime.Goexit), or every later
		// test in the binary would write into a closed pipe.
		defer func() {
			os.Stderr = orig
			_ = w.Close()
			<-done
			_ = r.Close()
		}()
		fn()
	}()

	return buf.String()
}

func TestUnknownConfigKeyFailsByDefault(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)

	err := e.run("list")
	if err == nil {
		t.Fatal("an unknown config key must fail the run by default")
	}
	if !strings.Contains(err.Error(), "guard.secret_patterns[0].pattern") {
		t.Errorf("error must name the offending key; got: %v", err)
	}
}

func TestIgnoreUnknownConfigProceedsWithWarning(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)

	var err error
	stderr := captureStderr(t, func() {
		err = e.run("list", "--ignore-unknown-config")
	})

	// Both halves matter. Asserting only the exit status would let a silent
	// bypass — the precise failure mode this wave exists to remove — pass.
	if err != nil {
		t.Fatalf("--ignore-unknown-config must let the run proceed; got: %v", err)
	}
	if !strings.Contains(stderr, "guard.secret_patterns[0].pattern") {
		t.Errorf("the warning must name the ignored key on stderr; stderr was:\n%s", stderr)
	}
	if !strings.Contains(stderr, "config.yml") {
		t.Errorf("the warning must name the file the key came from; stderr was:\n%s", stderr)
	}
	// stdout carries --json output that CI parses; the warning must never
	// leak into it.
	if strings.Contains(e.out.String(), "guard.secret_patterns[0].pattern") {
		t.Errorf("the warning must not reach stdout; stdout was:\n%s", e.out.String())
	}
}

// Faz 03's headline decision: --quiet suppresses *info* (progress) messages,
// not the unknown-key warning — resolveContext writes it straight to
// os.Stderr rather than through infof precisely so --quiet can never mute
// it (see context.go's warnUnknownConfigKeys). Nothing before this test
// exercised --quiet, so a refactor that "for consistency" routed the
// warning through infof would have passed every existing test while
// silencing it for every scripted/CI run that passes --quiet.
func TestIgnoreUnknownConfigWarnsEvenUnderQuiet(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)

	var err error
	stderr := captureStderr(t, func() {
		err = e.run("list", "--ignore-unknown-config", "--quiet")
	})

	if err != nil {
		t.Fatalf("--ignore-unknown-config --quiet must let the run proceed; got: %v", err)
	}
	if !strings.Contains(stderr, "guard.secret_patterns[0].pattern") {
		t.Errorf("--quiet must not suppress the unknown-key warning; stderr was:\n%s", stderr)
	}
}

func TestIgnoreUnknownConfigStillAppliesTheRestOfTheConfig(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)

	var err error
	_ = captureStderr(t, func() {
		err = e.run("list", "--ignore-unknown-config")
	})
	if err != nil {
		t.Fatalf("list --ignore-unknown-config: %v", err)
	}

	// `list` prints the resolved provider/model. Both come from the same
	// file that carries the unknown key, so seeing them proves the lenient
	// path skipped one key rather than discarding the whole layer.
	out := e.out.String()
	if !strings.Contains(out, "lenient-model") {
		t.Errorf("valid keys from the same file must still apply; output:\n%s", out)
	}
	if !strings.Contains(out, "mock") {
		t.Errorf("provider from the same file must still apply; output:\n%s", out)
	}
}

// Faz 06 (06-config-set-write-path.md) replaced the decode-into-typed-
// Config-then-marshal-the-whole-thing write path with a raw YAML patch that
// only ever touches the one changed key. That removes the reason Faz 05
// added a refuse-to-write gate here (review turu 2, item 6/7): a naive
// --ignore-unknown-config write used to silently destroy an unknown key's
// value (guard.secret_patterns[0].pattern coming back as regex: "") because
// the typed struct had nowhere to put it. A patch can't drop it — it never
// decodes the offending key at all — so `config set` and `providers use`
// now succeed under --ignore-unknown-config and leave the unknown key's
// line byte-for-byte untouched while still changing only the field asked
// for. The load-time warning (config.unknown_key_ignored) still fires; only
// the refusal is gone.

func TestConfigSetPatchesThroughAnIgnoredUnknownKey(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)
	configPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")

	var err error
	stderr := captureStderr(t, func() {
		err = e.run("config", "set", "output.lang", "tr", "--ignore-unknown-config")
	})
	if err != nil {
		t.Fatalf("config set --ignore-unknown-config must succeed once the write path can't lose the unknown key; got: %v", err)
	}
	// The load-time warning (resolveContext) still fires — --ignore-unknown-
	// config silences the failure, not the visibility that a key is inert.
	if !strings.Contains(stderr, "guard.secret_patterns[0].pattern") {
		t.Errorf("expected the usual ignore-warning on stderr; stderr was:\n%s", stderr)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "pattern: 'acme_[A-Za-z0-9]{32}'") {
		t.Errorf("the unknown key's line must survive the patch verbatim; file:\n%s", after)
	}
	if !strings.Contains(string(after), "lang: tr") {
		t.Errorf("output.lang must have been updated; file:\n%s", after)
	}
}

func TestProvidersUsePatchesThroughAnIgnoredUnknownKey(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)
	configPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")

	var err error
	_ = captureStderr(t, func() {
		// "mock" is registered by newCLIEnv via registerMockOnce; a
		// production binary would use a real provider name here instead.
		err = e.run("providers", "use", "mock", "--ignore-unknown-config")
	})
	if err != nil {
		t.Fatalf("providers use --ignore-unknown-config must succeed once the write path can't lose the unknown key; got: %v", err)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "pattern: 'acme_[A-Za-z0-9]{32}'") {
		t.Errorf("the unknown key's line must survive the patch verbatim; file:\n%s", after)
	}
	if !strings.Contains(string(after), "provider: mock") {
		t.Errorf("provider must have been switched to mock; file:\n%s", after)
	}
}

// setup is the deliberate exception to the refuse-to-write rule: it rewrites
// the file from scratch by design and is itself the recovery route for a
// config broken enough to need --ignore-unknown-config. This exercises the
// CLI wiring end to end (newSetupCmd's RunE → setup.RunOptions.IgnoreUnknownKeys
// → setup.Run's own pre-prompt config load), covering the specific
// regression item 7 calls out: reverting wizard.go's LoadFileWith back to
// the strict LoadFile would relock setup behind the exact config error it
// exists to rescue the user from.
//
// This used to run the real `setup` command and rely on it failing with a
// TTY error in this headless test environment — true on Linux/macOS (no TTY
// at all) but not on Windows CI, which still has a console attached: huh
// opened it and blocked on a real console read, hanging until the
// 10-minute per-package test timeout killed the whole binary. huh cannot be
// driven headlessly (it observes neither context cancellation nor a
// redirected stdin), so this substitutes setupRun with a stub that runs the
// exact same pre-prompt step the real setup.Run runs first
// (setup.LoadRunConfig) and stops there — proving the CLI flag reached
// RunOptions.IgnoreUnknownKeys and that config validation let the load
// through, without ever constructing a prompt.
func TestSetupCommandIgnoreUnknownConfigReachesThePrompt(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)

	var captured *setup.RunOptions
	stubErr := errors.New("stub: interactive prompt not exercised in tests")
	orig := setupRun
	setupRun = func(_ context.Context, opts setup.RunOptions) (*config.Config, error) {
		captured = &opts
		if _, _, err := setup.LoadRunConfig(opts); err != nil {
			return nil, err
		}
		return nil, stubErr
	}
	t.Cleanup(func() { setupRun = orig })

	var err error
	_ = captureStderr(t, func() {
		err = e.run("setup", "--ignore-unknown-config")
	})
	if err == nil {
		t.Fatal("setup should still fail here (the stub always errors after its config load) — " +
			"if it now succeeds, this test needs a different way to prove the load got past config validation")
	}
	if strings.Contains(err.Error(), "unknown key") {
		t.Errorf("setup --ignore-unknown-config must not die on the config; got a config error instead of the stub failure: %v", err)
	}
	if err != stubErr {
		t.Errorf("expected the stub's own failure once the config load succeeded; got: %v", err)
	}
	if captured == nil || !captured.IgnoreUnknownKeys {
		t.Fatal("expected --ignore-unknown-config to reach setup.RunOptions.IgnoreUnknownKeys")
	}
}
