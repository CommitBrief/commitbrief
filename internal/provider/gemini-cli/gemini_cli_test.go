// SPDX-License-Identifier: GPL-3.0-or-later

package geminicli

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

// TestPromptArgsDiffOnly: without --with-context the argv is the plain
// one-shot `-p <prompt>` with no trust/approval flags — pre-ADR-0017
// behavior, where the agent only sees the prompt.
func TestPromptArgsDiffOnly(t *testing.T) {
	got := promptArgs("PROMPT", false)
	if strings.Join(got, " ") != "-p PROMPT" {
		t.Errorf("diff-only argv = %v, want [-p PROMPT]", got)
	}
}

// TestPromptArgsWithContext: context mode needs BOTH --approval-mode plan
// (read-only) AND --skip-trust (untrusted-dir gate; without it plan is
// silently downgraded and reads are blocked — confirmed by the 2026-05-29
// smoke). plan mode never grants writes.
func TestPromptArgsWithContext(t *testing.T) {
	joined := strings.Join(promptArgs("PROMPT", true), " ")
	for _, want := range []string{"--approval-mode plan", "--skip-trust", "-p PROMPT"} {
		if !strings.Contains(joined, want) {
			t.Errorf("context argv missing %q; got %q", want, joined)
		}
	}
	for _, write := range []string{"yolo", "auto_edit"} {
		if strings.Contains(joined, write) {
			t.Errorf("context argv must not enable write-capable mode %q; got %q", write, joined)
		}
	}
}
