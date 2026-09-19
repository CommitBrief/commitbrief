// SPDX-License-Identifier: GPL-3.0-or-later

package codexcli

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func TestMetadataDescribesTheCLIBackend(t *testing.T) {
	md := Metadata()

	if md.Name != Name {
		t.Fatalf("Metadata().Name = %q, want %q", md.Name, Name)
	}
	if md.Kind != provider.KindCLI {
		t.Fatalf("Metadata().Kind = %q, want %q", md.Kind, provider.KindCLI)
	}
	if md.Binary != "codex" {
		t.Errorf("Metadata().Binary = %q, want %q", md.Binary, "codex")
	}
	// The host CLI owns model selection, so there is nothing to publish.
	// DefaultModel in particular must stay empty: clireview synthesises a
	// "binary@version" cache-key identifier at runtime, which is not a
	// model and would mislead anyone reading the generated inventory.
	if len(md.Models) != 0 {
		t.Errorf("Metadata().Models = %v, want none for a CLI backend", md.Models)
	}
	if md.DefaultModel != "" {
		t.Errorf("Metadata().DefaultModel = %q, want empty for a CLI backend", md.DefaultModel)
	}
	if md.APIKeyEnv != "" {
		t.Errorf("Metadata().APIKeyEnv = %q, want empty for a CLI backend", md.APIKeyEnv)
	}
}
