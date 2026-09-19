// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// seenModel is the slice of ModelInfo these assertions care about. The
// point of the test is that Metadata() is derived from the package's own
// tables, so every fact it publishes must be traceable back to one.
type seenModel struct {
	ctx   int
	price provider.Pricing
}

func TestMetadataCoversSupportedModels(t *testing.T) {
	md := Metadata()

	if md.Name != Name {
		t.Fatalf("Metadata().Name = %q, want %q", md.Name, Name)
	}
	if md.Kind != provider.KindAPI {
		t.Fatalf("Metadata().Kind = %q, want %q", md.Kind, provider.KindAPI)
	}
	if md.DefaultModel != DefaultModel {
		t.Fatalf("Metadata().DefaultModel = %q, want %q", md.DefaultModel, DefaultModel)
	}
	if md.APIKeyEnv != "DEEPSEEK_API_KEY" {
		t.Errorf("Metadata().APIKeyEnv = %q, want %q", md.APIKeyEnv, "DEEPSEEK_API_KEY")
	}
	if md.DefaultBaseURL != defaultBaseURL {
		t.Errorf("Metadata().DefaultBaseURL = %q, want %q", md.DefaultBaseURL, defaultBaseURL)
	}
	got := make(map[string]seenModel, len(md.Models))
	for _, m := range md.Models {
		got[m.ID] = seenModel{ctx: m.ContextWindow, price: m.Pricing}
	}
	for _, id := range supportedModels {
		seen, ok := got[id]
		if !ok {
			t.Fatalf("supported model %q missing from Metadata()", id)
		}
		if seen.ctx <= 0 {
			t.Errorf("model %q has no context window", id)
		}
		if seen.price == (provider.Pricing{}) {
			t.Errorf("model %q has no pricing row", id)
		}
	}
	if len(md.Models) != len(supportedModels) {
		t.Errorf("Metadata() lists %d models, supportedModels has %d",
			len(md.Models), len(supportedModels))
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
