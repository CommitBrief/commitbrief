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
