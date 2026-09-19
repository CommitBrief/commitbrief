// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

// Wave 0 review turu 2, item 6/7: `config set` and `providers use` decode
// the target file into a typed config.Config and rewrite the WHOLE thing.
// An unknown key has nowhere to land in that struct, so a naive
// --ignore-unknown-config (load leniently, write back the typed struct)
// would silently destroy the user's original value — a mistyped
// guard.secret_patterns[0].pattern coming back as regex: "". Both commands
// must refuse to write instead: name the key, exit non-zero, and leave the
// file's bytes untouched. The byte-for-byte comparison is deliberate — an
// assertion that only checks "exited non-zero" would still pass if the file
// had already been clobbered before the command decided to fail.

func TestConfigSetRefusesToWriteWhenUnknownKeyIgnored(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)
	configPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	stderr := captureStderr(t, func() {
		err = e.run("config", "set", "output.lang", "tr", "--ignore-unknown-config")
	})
	if err == nil {
		t.Fatal("config set --ignore-unknown-config must refuse to write when the file carries an unknown key")
	}
	// Review turu 3, item 11: the old strict LoadFile path ALSO returns a
	// non-nil error naming this exact key (it fails validation before ever
	// writing), so those two assertions alone pass under either behavior —
	// a `go test -overlay` revert of the refusal code confirmed this test
	// stayed green with refuseUnknownKeysWrite deleted entirely. Pinning on
	// "refusing to write" (from config.write_refused_unknown_keys, the
	// message only the NEW refusal path emits) is what makes this test
	// actually fail if that code regresses.
	if !strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("must be the refusal path (config.write_refused_unknown_keys), not the strict loader's own error; got: %v", err)
	}
	if !strings.Contains(err.Error(), "guard.secret_patterns[0].pattern") {
		t.Errorf("refusal must name the key that would be lost; got: %v", err)
	}
	// The load-time warning still fires (it comes from resolveContext,
	// before the refusal); the point of this test is what happens next.
	if !strings.Contains(stderr, "guard.secret_patterns[0].pattern") {
		t.Errorf("expected the usual ignore-warning on stderr too; stderr was:\n%s", stderr)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("config set must leave the file untouched on refusal.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func TestProvidersUseRefusesToWriteWhenUnknownKeyIgnored(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)
	configPath := filepath.Join(e.homeDir, ".commitbrief", "config.yml")

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}

	_ = captureStderr(t, func() {
		// "mock" is registered by newCLIEnv via registerMockOnce; a
		// production binary would use a real provider name here instead.
		err = e.run("providers", "use", "mock", "--ignore-unknown-config")
	})
	if err == nil {
		t.Fatal("providers use --ignore-unknown-config must refuse to write when the file carries an unknown key")
	}
	// Same reasoning as TestConfigSetRefusesToWriteWhenUnknownKeyIgnored
	// (review turu 3, item 11): without this, the old strict LoadFile's own
	// validation error satisfies every other assertion here too.
	if !strings.Contains(err.Error(), "refusing to write") {
		t.Errorf("must be the refusal path (config.write_refused_unknown_keys), not the strict loader's own error; got: %v", err)
	}
	if !strings.Contains(err.Error(), "guard.secret_patterns[0].pattern") {
		t.Errorf("refusal must name the key that would be lost; got: %v", err)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("providers use must leave the file untouched on refusal.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// setup is the deliberate exception to the refuse-to-write rule: it rewrites
// the file from scratch by design and is itself the recovery route for a
// config broken enough to need --ignore-unknown-config. This exercises the
// CLI wiring end to end (newSetupCmd's RunE → setup.RunOptions.IgnoreUnknownKeys
// → setup.Run), covering the specific regression item 7 calls out: reverting
// wizard.go's LoadFileWith back to the strict LoadFile would relock setup
// behind the exact config error it exists to rescue the user from.
//
// This test environment has no TTY, so Run cannot complete the wizard; the
// assertion that matters is which error it fails with. Getting past config
// validation to a TTY-only failure — instead of an "unknown key" config
// error — is exactly what proves the escape hatch reached setup.
func TestSetupCommandIgnoreUnknownConfigReachesThePrompt(t *testing.T) {
	e := newCLIEnv(t)
	writeRawUserConfig(t, e.homeDir, unknownKeyUserConfig)

	var err error
	_ = captureStderr(t, func() {
		err = e.run("setup", "--ignore-unknown-config")
	})
	if err == nil {
		t.Fatal("setup should still fail in this headless test environment (no TTY) — " +
			"if it now succeeds, this test needs a different way to prove the load got past config validation")
	}
	if strings.Contains(err.Error(), "unknown key") {
		t.Errorf("setup --ignore-unknown-config must not die on the config; got a config error instead of a TTY failure: %v", err)
	}
}
