// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func TestMetadataCoversSupportedModels(t *testing.T) {
	md := Metadata()

	if md.Name != Name {
		t.Fatalf("Metadata().Name = %q, want %q", md.Name, Name)
	}
	if md.Kind != provider.KindLocal {
		t.Fatalf("Metadata().Kind = %q, want %q", md.Kind, provider.KindLocal)
	}
	if md.DefaultModel != DefaultModel {
		t.Fatalf("Metadata().DefaultModel = %q, want %q", md.DefaultModel, DefaultModel)
	}
	if md.DefaultBaseURL != DefaultBaseURL {
		t.Errorf("Metadata().DefaultBaseURL = %q, want %q", md.DefaultBaseURL, DefaultBaseURL)
	}
	// A local runtime needs no credential; publishing an env var here
	// would send readers looking for a key that does not exist.
	if md.APIKeyEnv != "" {
		t.Errorf("Metadata().APIKeyEnv = %q, want empty for a local provider", md.APIKeyEnv)
	}

	// Pricing is deliberately not asserted: the models run on the user's
	// own hardware, so a zero Pricing is the correct answer, not a hole.
	windows := make(map[string]int, len(md.Models))
	for _, m := range md.Models {
		windows[m.ID] = m.ContextWindow
	}
	for _, id := range Models() {
		ctx, ok := windows[id]
		if !ok {
			t.Fatalf("suggested model %q missing from Metadata()", id)
		}
		if ctx <= 0 {
			t.Errorf("model %q has no context window", id)
		}
	}
	if len(md.Models) != len(Models()) {
		t.Errorf("Metadata() lists %d models, Models() has %d",
			len(md.Models), len(Models()))
	}
}

func TestDefaultModelIsMarkedDefault(t *testing.T) {
	var defaults []string
	for _, m := range Metadata().Models {
		if m.Default {
			defaults = append(defaults, m.ID)
		}
	}
	if len(defaults) != 1 {
		t.Fatalf("Metadata() marks %d models as default (%v), want exactly one",
			len(defaults), defaults)
	}
	if defaults[0] != DefaultModel {
		t.Errorf("default-marked model is %q, want %q", defaults[0], DefaultModel)
	}
}
