// SPDX-License-Identifier: GPL-3.0-or-later

package mock

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func TestMetadataCoversSupportedModels(t *testing.T) {
	md := Metadata()
	ref := New()

	if md.Name != defaultName {
		t.Fatalf("Metadata().Name = %q, want %q", md.Name, defaultName)
	}
	if md.Kind != provider.KindAPI {
		t.Fatalf("Metadata().Kind = %q, want %q", md.Kind, provider.KindAPI)
	}
	if md.DefaultModel != ref.DefaultModel() {
		t.Fatalf("Metadata().DefaultModel = %q, want %q", md.DefaultModel, ref.DefaultModel())
	}

	// The mock serves exactly one synthetic model; pricing is legitimately
	// zero (nothing is billed), so only the context window is asserted.
	if len(md.Models) != 1 {
		t.Fatalf("Metadata() lists %d models, want exactly 1", len(md.Models))
	}
	m := md.Models[0]
	if m.ID != ref.DefaultModel() {
		t.Errorf("model ID = %q, want %q", m.ID, ref.DefaultModel())
	}
	if m.ContextWindow != ref.ContextWindow(m.ID) {
		t.Errorf("model %q context window = %d, want %d",
			m.ID, m.ContextWindow, ref.ContextWindow(m.ID))
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
	if defaults[0] != Metadata().DefaultModel {
		t.Errorf("default-marked model is %q, want %q", defaults[0], Metadata().DefaultModel)
	}
}
