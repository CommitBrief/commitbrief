// SPDX-License-Identifier: GPL-3.0-or-later

package claudecli

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

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

// TestPromptArgsDiffOnly: without --with-context the argv is the original
// stdin-transport one-shot with NO tool grant — preserving pre-ADR-0017
// behavior (the agent cannot read beyond the piped prompt).
func TestPromptArgsDiffOnly(t *testing.T) {
	got := strings.Join(promptArgs("", false), " ")
	if got != "-p - --output-format text" {
		t.Errorf("diff-only argv = %q, want %q", got, "-p - --output-format text")
	}
	if strings.Contains(got, "allowedTools") {
		t.Errorf("diff-only mode must NOT grant tools; got %q", got)
	}
}

// TestPromptArgsWithContext: context mode appends the read-only tool grant.
// The list must be COMMA-separated (the flag is variadic; a space-separated
// list would swallow a following positional arg). No write tools.
func TestPromptArgsWithContext(t *testing.T) {
	args := promptArgs("", true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--allowedTools Read,Grep,Glob") {
		t.Errorf("context argv must grant Read,Grep,Glob (comma-separated); got %q", joined)
	}
	for _, write := range []string{"Edit", "Write", "Bash", "dangerously"} {
		if strings.Contains(joined, write) {
			t.Errorf("context argv must not grant %q; got %q", write, joined)
		}
	}
}
