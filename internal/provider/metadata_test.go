// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"testing"
)

func TestRegisterMetadataAndLookup(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()

	RegisterMetadata(Metadata{
		Name:         "stub",
		Kind:         KindAPI,
		DefaultModel: "stub-model",
		APIKeyEnv:    "STUB_API_KEY",
		Models: []ModelInfo{
			{
				ID:            "stub-model",
				Default:       true,
				ContextWindow: 1024,
				Pricing:       Pricing{InputPer1M: 3.0, OutputPer1M: 15.0},
			},
		},
	})

	md, ok := MetadataFor("stub")
	if !ok {
		t.Fatal("MetadataFor(\"stub\") = _, false; want true")
	}
	if md.Kind != KindAPI {
		t.Errorf("Kind = %q, want %q", md.Kind, KindAPI)
	}
	if md.DefaultModel != "stub-model" {
		t.Errorf("DefaultModel = %q, want stub-model", md.DefaultModel)
	}
	if md.APIKeyEnv != "STUB_API_KEY" {
		t.Errorf("APIKeyEnv = %q, want STUB_API_KEY", md.APIKeyEnv)
	}
	if len(md.Models) != 1 {
		t.Fatalf("len(Models) = %d, want 1", len(md.Models))
	}
	if md.Models[0].ContextWindow != 1024 {
		t.Errorf("Models[0].ContextWindow = %d, want 1024", md.Models[0].ContextWindow)
	}
	if md.Models[0].Pricing.OutputPer1M != 15.0 {
		t.Errorf("Models[0].Pricing.OutputPer1M = %f, want 15.0", md.Models[0].Pricing.OutputPer1M)
	}
}

func TestAllMetadataIsSorted(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()

	for _, name := range []string{"openai", "anthropic", "gemini"} {
		RegisterMetadata(Metadata{Name: name, Kind: KindAPI})
	}

	got := AllMetadata()
	want := []string{"anthropic", "gemini", "openai"}
	if len(got) != len(want) {
		t.Fatalf("AllMetadata() has %d entries, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Name != want[i] {
			t.Errorf("AllMetadata()[%d].Name = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

func TestAllMetadataReturnsDefensiveCopy(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()

	RegisterMetadata(Metadata{
		Name:         "stub",
		Kind:         KindAPI,
		DefaultModel: "stub-model",
		Models: []ModelInfo{
			{ID: "stub-model", Default: true, ContextWindow: 1024},
		},
	})

	first := AllMetadata()
	if len(first) != 1 || len(first[0].Models) != 1 {
		t.Fatalf("unexpected first snapshot: %+v", first)
	}
	// Mutate both the outer slice element and the nested Models slice.
	first[0].Name = "tampered"
	first[0].DefaultModel = "tampered-model"
	first[0].Models[0].ID = "tampered-model"
	first[0].Models[0].ContextWindow = 1

	second := AllMetadata()
	if len(second) != 1 {
		t.Fatalf("len(AllMetadata()) = %d, want 1", len(second))
	}
	if second[0].Name != "stub" {
		t.Errorf("Name = %q after caller mutation, want stub", second[0].Name)
	}
	if second[0].DefaultModel != "stub-model" {
		t.Errorf("DefaultModel = %q after caller mutation, want stub-model", second[0].DefaultModel)
	}
	if second[0].Models[0].ID != "stub-model" {
		t.Errorf("Models[0].ID = %q after caller mutation, want stub-model", second[0].Models[0].ID)
	}
	if second[0].Models[0].ContextWindow != 1024 {
		t.Errorf("Models[0].ContextWindow = %d after caller mutation, want 1024",
			second[0].Models[0].ContextWindow)
	}

	// The same protection has to hold for the single-provider lookup, which
	// shares the registry's backing arrays through the same code path.
	one, ok := MetadataFor("stub")
	if !ok {
		t.Fatal("MetadataFor(\"stub\") = _, false; want true")
	}
	one.Models[0].ID = "tampered-model"
	again, _ := MetadataFor("stub")
	if again.Models[0].ID != "stub-model" {
		t.Errorf("MetadataFor Models[0].ID = %q after caller mutation, want stub-model",
			again.Models[0].ID)
	}
}

func TestRegisterMetadataPanicsOnEmptyName(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on empty name")
		}
	}()
	RegisterMetadata(Metadata{Kind: KindAPI})
}

func TestRegisterMetadataPanicsOnDuplicate(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()

	RegisterMetadata(Metadata{Name: "dupe", Kind: KindAPI})
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	RegisterMetadata(Metadata{Name: "dupe", Kind: KindLocal})
}

func TestMetadataForUnknownReturnsFalse(t *testing.T) {
	t.Cleanup(resetMetadataForTest)
	resetMetadataForTest()

	md, ok := MetadataFor("nonexistent")
	if ok {
		t.Errorf("MetadataFor(\"nonexistent\") = _, true; want false")
	}
	if md.Name != "" {
		t.Errorf("zero Metadata expected, got %+v", md)
	}
}
