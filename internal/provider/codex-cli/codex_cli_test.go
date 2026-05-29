// SPDX-License-Identifier: GPL-3.0-or-later

package codexcli

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// TestRegistersAsPlainTextProvider asserts the blank-import registration
// wired the codex-cli provider into the registry and that it is a
// PlainTextEmitter (so the review pipeline takes the verbatim CLI-output
// path, not the JSON-findings contract).
func TestRegistersAsPlainTextProvider(t *testing.T) {
	p, err := provider.New(Name, config.ProviderConfig{})
	if err != nil {
		t.Fatalf("provider.New(%q): %v", Name, err)
	}
	if p.Name() != Name {
		t.Errorf("Name() = %q, want %q", p.Name(), Name)
	}
	if _, ok := p.(provider.PlainTextEmitter); !ok {
		t.Errorf("%s must implement provider.PlainTextEmitter", Name)
	}
}

// TestPromptArgsContextInvariant: codex permits reads under its read-only
// sandbox, so --with-context (ADR-0017) must NOT change the argv. Both
// modes keep the read-only sandbox and never grant writes.
func TestPromptArgsContextInvariant(t *testing.T) {
	off := promptArgs("PROMPT", false)
	on := promptArgs("PROMPT", true)
	if strings.Join(off, " ") != strings.Join(on, " ") {
		t.Errorf("context must not change codex argv:\n off=%v\n on =%v", off, on)
	}
	joined := strings.Join(on, " ")
	if !strings.Contains(joined, "--sandbox read-only") {
		t.Errorf("argv must pin read-only sandbox; got %q", joined)
	}
	if strings.Contains(joined, "workspace-write") || strings.Contains(joined, "danger") {
		t.Errorf("argv must never grant writes; got %q", joined)
	}
}
