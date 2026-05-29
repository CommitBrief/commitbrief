// SPDX-License-Identifier: GPL-3.0-or-later

package codexcli

import (
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
