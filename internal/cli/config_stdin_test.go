// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/config"
)

func TestConfigSetStdinReadsValue(t *testing.T) {
	e := newCLIEnv(t)
	e.stdin = strings.NewReader("sk-ant-from-stdin")
	if err := e.run("config", "set", "providers.anthropic.api_key", "-"); err != nil {
		t.Fatalf("config set -: %v", err)
	}
	if got := loadCfg(t, e.homeDir).Providers["anthropic"].APIKey; got != "sk-ant-from-stdin" {
		t.Errorf("api_key = %q, want sk-ant-from-stdin", got)
	}
	if strings.Contains(e.out.String()+e.errOut.String(), "sk-ant-from-stdin") {
		t.Errorf("value echoed in command output:\n%s%s", e.out.String(), e.errOut.String())
	}
}

func TestConfigSetStdinTrimsOneTrailingNewline(t *testing.T) {
	cases := map[string]string{
		"LF":        "sk-x\n",
		"CRLF":      "sk-x\r\n",
		"none":      "sk-x",
		"double LF": "sk-x\n\n", // only one terminator is removed
	}
	want := map[string]string{"LF": "sk-x", "CRLF": "sk-x", "none": "sk-x", "double LF": "sk-x\n"}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			e.stdin = strings.NewReader(in)
			if err := e.run("config", "set", "providers.anthropic.api_key", "-"); err != nil {
				t.Fatalf("config set -: %v", err)
			}
			if got := loadCfg(t, e.homeDir).Providers["anthropic"].APIKey; got != want[name] {
				t.Errorf("api_key = %q, want %q", got, want[name])
			}
		})
	}
}

func TestConfigSetStdinRejectsOversize(t *testing.T) {
	e := newCLIEnv(t)
	e.stdin = strings.NewReader(strings.Repeat("a", configStdinValueMax+1))
	err := e.run("config", "set", "providers.anthropic.api_key", "-")
	if err == nil {
		t.Fatal("want error for oversize stdin, got nil")
	}
	if !strings.Contains(err.Error(), "limit") {
		t.Errorf("error %q should mention the limit", err.Error())
	}
	if _, ok := loadCfg(t, e.homeDir).Providers["anthropic"]; ok {
		t.Error("config was written despite the oversize value")
	}
}

func TestConfigSetStdinAcceptsExactlyMax(t *testing.T) {
	e := newCLIEnv(t)
	e.stdin = strings.NewReader(strings.Repeat("a", configStdinValueMax))
	if err := e.run("config", "set", "providers.anthropic.api_key", "-"); err != nil {
		t.Fatalf("value of exactly the cap should be accepted: %v", err)
	}
}

func TestConfigSetStdinRejectsEmpty(t *testing.T) {
	for name, in := range map[string]string{"empty": "", "newline only": "\n"} {
		t.Run(name, func(t *testing.T) {
			e := newCLIEnv(t)
			e.stdin = strings.NewReader(in)
			if err := e.run("config", "set", "providers.anthropic.api_key", "-"); err == nil {
				t.Fatal("want error for empty stdin value, got nil")
			}
			if _, ok := loadCfg(t, e.homeDir).Providers["anthropic"]; ok {
				t.Error("config was written for an empty stdin value")
			}
		})
	}
}

func TestConfigSetStdinRejectsTTY(t *testing.T) {
	old := configStdinIsTTY
	configStdinIsTTY = func(io.Reader) bool { return true }
	t.Cleanup(func() { configStdinIsTTY = old })

	e := newCLIEnv(t)
	e.stdin = strings.NewReader("sk-should-not-be-read")
	err := e.run("config", "set", "providers.anthropic.api_key", "-")
	if err == nil {
		t.Fatal("want error when stdin is a terminal, got nil")
	}
	if !strings.Contains(err.Error(), "terminal") || !strings.Contains(err.Error(), "--literal") {
		t.Errorf("error %q should explain the terminal rejection and the --literal escape", err.Error())
	}
	if _, ok := loadCfg(t, e.homeDir).Providers["anthropic"]; ok {
		t.Error("config was written although stdin was a terminal")
	}
}

func TestConfigSetStdinWithLocal(t *testing.T) {
	e := newCLIEnv(t)
	e.stdin = strings.NewReader("sk-local-key\n")
	if err := e.run("config", "set", "--local", "providers.anthropic.api_key", "-"); err != nil {
		t.Fatalf("config set --local -: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(e.repoRoot, ".commitbrief", "config.yml"))
	if err != nil {
		t.Fatalf("local config not written: %v", err)
	}
	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	if got := cfg.Providers["anthropic"].APIKey; got != "sk-local-key" {
		t.Errorf("local api_key = %q, want sk-local-key", got)
	}
	if _, ok := loadCfg(t, e.homeDir).Providers["anthropic"]; ok {
		t.Error("--local must not touch the user-level config")
	}
}

func TestConfigSetStdinLiteralDash(t *testing.T) {
	e := newCLIEnv(t)
	e.stdin = strings.NewReader("must-not-be-read")
	if err := e.run("config", "set", "--literal", "providers.anthropic.model", "-"); err != nil {
		t.Fatalf("config set --literal: %v", err)
	}
	if got := loadCfg(t, e.homeDir).Providers["anthropic"].Model; got != "-" {
		t.Errorf("model = %q, want literal -", got)
	}
}

func TestConfigSetStdinLeavesArgvValueAlone(t *testing.T) {
	// A non-"-" value keeps its argv behaviour even when stdin carries data.
	e := newCLIEnv(t)
	e.stdin = strings.NewReader("ignored")
	if err := e.run("config", "set", "providers.anthropic.api_key", "sk-argv"); err != nil {
		t.Fatal(err)
	}
	if got := loadCfg(t, e.homeDir).Providers["anthropic"].APIKey; got != "sk-argv" {
		t.Errorf("api_key = %q, want sk-argv", got)
	}
}

func TestConfigSetStdinNeverLeaksValueInErrors(t *testing.T) {
	const secret = "sk-ant-VERYSECRET\"quoted"
	// Each key echoes the offending value in its own validation message.
	for _, key := range []string{"cache.ttl_days", "cache.max_size_mb", "cost.warn_threshold_usd", "output.color", "review.timeout", "cache.enabled"} {
		t.Run(key, func(t *testing.T) {
			e := newCLIEnv(t)
			e.stdin = strings.NewReader(secret + "\n")
			err := e.run("config", "set", key, "-")
			if err == nil {
				t.Fatalf("want validation error for %s, got nil", key)
			}
			all := err.Error() + e.out.String() + e.errOut.String()
			if strings.Contains(all, "VERYSECRET") {
				t.Errorf("stdin value leaked for %s:\n%s", key, all)
			}
		})
	}
}

func TestConfigSetStdinRedactsEchoedValue(t *testing.T) {
	if err := redactConfigValue(io.ErrUnexpectedEOF); err != io.ErrUnexpectedEOF {
		t.Errorf("an error without a quoted literal must pass through unchanged, got %v", err)
	}
	got := redactConfigValue(errString(`config: cache.ttl_days must be an integer; got "a\"b c"`))
	const want = `config: cache.ttl_days must be an integer; got "<redacted>"`
	if got.Error() != want {
		t.Errorf("redaction = %q, want %q", got.Error(), want)
	}
}

// A short value that also occurs in the fixed message text must neither mangle
// the message nor reveal itself through where the mask lands.
func TestConfigSetStdinRedactionKeepsMessageReadable(t *testing.T) {
	for _, value := range []string{"e", "fig", "a", "/"} {
		t.Run(value, func(t *testing.T) {
			e := newCLIEnv(t)
			e.stdin = strings.NewReader(value + "\n")
			err := e.run("config", "set", "output.color", "-")
			if err == nil {
				t.Fatal("want validation error, got nil")
			}
			const want = `config: output.color must be auto/always/never; got "<redacted>"`
			if err.Error() != want {
				t.Errorf("error = %q, want %q", err.Error(), want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
