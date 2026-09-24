// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"slices"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider"

	// Side-effect imports: the provider metadata registry is populated by
	// each provider package's init(), which in the real binary happens
	// through the blank imports in cmd/commitbrief/main.go. A test that
	// reads AllMetadata() without linking those packages in observes an
	// EMPTY registry and every assertion below passes vacuously — which is
	// exactly the regression TestSpecsIsNotEmpty exists to catch. Keep this
	// list in sync with cmd/commitbrief/main.go.
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	_ "github.com/CommitBrief/commitbrief/internal/provider/claude-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/codex-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/cohere"
	_ "github.com/CommitBrief/commitbrief/internal/provider/deepseek"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini-cli"
	_ "github.com/CommitBrief/commitbrief/internal/provider/mistral"
	_ "github.com/CommitBrief/commitbrief/internal/provider/ollama"
	_ "github.com/CommitBrief/commitbrief/internal/provider/openai"
)

// TestSpecsIsNotEmpty guards the vacuous-pass failure mode head-on: if the
// blank imports above are ever dropped, or Specs() stops producing entries,
// this fails loudly instead of letting the richer assertions below succeed
// over an empty input.
func TestSpecsIsNotEmpty(t *testing.T) {
	if got := len(Specs()); got == 0 {
		t.Fatal("Specs() returned no providers; the wizard would show an empty list")
	}
	if got := len(provider.AllMetadata()); got == 0 {
		t.Fatal("provider metadata registry is empty; the blank imports in this file are missing or broken")
	}
}

// TestSpecsPresentationOrder locks the order the wizard shows providers in.
// It is deliberately independent of registry order (which is alphabetical):
// the wizard leads with the three first-party API providers users most often
// configure and ends with the keyless local option, and that sequence is a
// UX decision, not a provider fact.
func TestSpecsPresentationOrder(t *testing.T) {
	want := []string{"anthropic", "openai", "gemini", "deepseek", "mistral", "cohere", "ollama"}
	specs := Specs()
	if len(specs) != len(want) {
		t.Fatalf("Specs() has %d entries, want %d", len(specs), len(want))
	}
	for i, name := range want {
		if specs[i].Name != name {
			t.Errorf("Specs()[%d].Name = %q, want %q", i, specs[i].Name, name)
		}
	}
}

// TestSpecsMatchProviderMetadata is the whole point of the refactor: the
// wizard's model lists must be the registry's, in the registry's order, with
// no hand-copied literals in between.
//
// Scoped to NeedsKey specs because ollama is the deliberate exception — see
// TestOllamaSpecHasNoStaticModels.
func TestSpecsMatchProviderMetadata(t *testing.T) {
	for _, spec := range Specs() {
		if !spec.NeedsKey {
			continue
		}
		md, ok := provider.MetadataFor(spec.Name)
		if !ok {
			t.Errorf("spec %q has no registered provider metadata", spec.Name)
			continue
		}
		if len(spec.Models) != len(md.Models) {
			t.Errorf("spec %q lists %d models, metadata has %d", spec.Name, len(spec.Models), len(md.Models))
			continue
		}
		for i, m := range md.Models {
			if spec.Models[i] != m.ID {
				t.Errorf("spec %q model[%d] = %q, want %q", spec.Name, i, spec.Models[i], m.ID)
			}
		}
	}
}

// TestOllamaSpecHasNoStaticModels pins the one intentional gap. Ollama serves
// whatever the user has pulled, so selectModel asks the daemon via
// OllamaModels and never reads spec.Models; copying the registry's
// suggestion list in would present it as an authoritative catalogue.
func TestOllamaSpecHasNoStaticModels(t *testing.T) {
	spec := FindSpec("ollama")
	if spec == nil {
		t.Fatal("FindSpec(\"ollama\") = nil")
	}
	if !spec.NeedsURL {
		t.Error("ollama spec should set NeedsURL so selectModel discovers models at runtime")
	}
	if len(spec.Models) != 0 {
		t.Errorf("ollama spec should carry no static models, got %v", spec.Models)
	}
}

// TestSpecsCoverEveryRegisteredProvider fails when a new API/local provider
// is registered without wizard UI text, which would otherwise leave it
// unreachable from `commitbrief setup` with nothing to signal the omission.
//
// CLI-backed providers (KindCLI) are excluded on purpose: they take no API
// key and no model — the host CLI owns both — so there is nothing for the
// wizard to prompt for. "mock" is excluded because it is a test fixture.
func TestSpecsCoverEveryRegisteredProvider(t *testing.T) {
	have := make(map[string]bool)
	for _, spec := range Specs() {
		have[spec.Name] = true
	}
	for _, md := range provider.AllMetadata() {
		if md.Kind == provider.KindCLI || md.Name == "mock" {
			continue
		}
		if !have[md.Name] {
			t.Errorf("registered provider %q has no wizard spec", md.Name)
		}
	}
}

func TestFindSpec(t *testing.T) {
	spec := FindSpec("anthropic")
	if spec == nil {
		t.Fatal("FindSpec(\"anthropic\") = nil, want a spec")
	}
	if spec.Name != "anthropic" {
		t.Errorf("FindSpec returned spec for %q", spec.Name)
	}
	if spec.Label == "" {
		t.Error("FindSpec returned a spec with no Label")
	}
	if len(spec.Models) == 0 {
		t.Error("FindSpec(\"anthropic\") returned a spec with no models")
	}
	if got := FindSpec("no-such-provider"); got != nil {
		t.Errorf("FindSpec(\"no-such-provider\") = %+v, want nil", got)
	}
}

// TestModelLabelsShowPriceAndSignal checks every static model entry in the
// picker: it must carry a cost signal ($ per 1M tokens, from the provider
// catalog) and a quality signal. No benchmark results ship with the binary,
// so the quality signal is the honest "not measured"; the provider's
// DefaultModel additionally carries the "default" marker, and only it does.
func TestModelLabelsShowPriceAndSignal(t *testing.T) {
	checked := 0
	for _, spec := range Specs() {
		if !spec.NeedsKey {
			continue
		}
		md, ok := provider.MetadataFor(spec.Name)
		if !ok {
			t.Fatalf("no metadata for %q", spec.Name)
		}
		for _, m := range spec.Models {
			label := modelLabel(m, md, true, nil)
			checked++
			if !strings.HasPrefix(label, m+" · ") {
				t.Errorf("%s/%s: label %q does not start with the model ID", spec.Name, m, label)
			}
			if !strings.Contains(label, "$") || !strings.Contains(label, "/1M in") || !strings.Contains(label, "/1M out") {
				t.Errorf("%s/%s: label %q has no list price", spec.Name, m, label)
			}
			if !strings.Contains(label, "not measured") {
				t.Errorf("%s/%s: label %q has no quality signal", spec.Name, m, label)
			}
			if strings.Contains(strings.ReplaceAll(label, "not measured", ""), "measured") {
				t.Errorf("%s/%s: label %q claims a measurement", spec.Name, m, label)
			}
			isDefault := strings.HasSuffix(label, " · default")
			if want := m == md.DefaultModel; isDefault != want {
				t.Errorf("%s/%s: default marker = %v, want %v (label %q)", spec.Name, m, isDefault, want, label)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no static models checked; the test passed vacuously")
	}
}

// TestModelLabelFormatsPrice pins the exact label shape with a synthetic
// catalog entry, independent of any real model name or price.
func TestModelLabelFormatsPrice(t *testing.T) {
	md := provider.Metadata{
		DefaultModel: "m-default",
		Models: []provider.ModelInfo{
			{ID: "m-default", Pricing: provider.Pricing{InputPer1M: 3, OutputPer1M: 15}},
			{ID: "m-cheap", Pricing: provider.Pricing{InputPer1M: 0.15, OutputPer1M: 0.6}},
		},
	}
	cases := map[string]string{
		"m-default":  "m-default · $3/1M in · $15/1M out · not measured · default",
		"m-cheap":    "m-cheap · $0.15/1M in · $0.6/1M out · not measured",
		"m-uncosted": "m-uncosted · local",
	}
	for m, want := range cases {
		if got := modelLabel(m, md, true, nil); got != want {
			t.Errorf("modelLabel(%q) = %q, want %q", m, got, want)
		}
	}
	if got := modelLabel("qwen2.5-coder:14b", provider.Metadata{}, false, nil); got != "qwen2.5-coder:14b · local" {
		t.Errorf("discovered model label = %q, want %q", got, "qwen2.5-coder:14b · local")
	}
}

// TestModelSelectPreselectsDefaultModel is the ADR-0043 Enter fix: for the
// static lists the picker must open on the provider's DefaultModel, not on
// whichever model the catalog lists first. The expected value is read from
// the registry so a DefaultModel change does not break this test.
func TestModelSelectPreselectsDefaultModel(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "gemini"} {
		spec := FindSpec(name)
		if spec == nil {
			t.Fatalf("FindSpec(%q) = nil", name)
		}
		md, ok := provider.MetadataFor(name)
		if !ok || md.DefaultModel == "" {
			t.Fatalf("%s: no DefaultModel in metadata", name)
		}
		if !slices.Contains(spec.Models, md.DefaultModel) {
			t.Fatalf("%s: DefaultModel %q is not in the static model list", name, md.DefaultModel)
		}
		var choices Choices
		sel := newModelSelect(spec, spec.Models, &choices, nil)
		if choices.Model != md.DefaultModel {
			t.Errorf("%s: choices.Model = %q, want DefaultModel %q", name, choices.Model, md.DefaultModel)
		}
		if got, _ := sel.GetValue().(string); got != md.DefaultModel {
			t.Errorf("%s: select value = %q, want DefaultModel %q", name, got, md.DefaultModel)
		}
	}
}

// TestModelSelectKeepsExplicitChoice guards the "only when empty" half of
// the preselect: a model already in choices is not overwritten.
func TestModelSelectKeepsExplicitChoice(t *testing.T) {
	spec := FindSpec("anthropic")
	if spec == nil || len(spec.Models) < 2 {
		t.Skip("anthropic spec needs at least two models")
	}
	md, _ := provider.MetadataFor("anthropic")
	pick := spec.Models[0]
	if pick == md.DefaultModel {
		pick = spec.Models[1]
	}
	choices := Choices{Model: pick}
	newModelSelect(spec, spec.Models, &choices, nil)
	if choices.Model != pick {
		t.Errorf("choices.Model = %q, want the explicit %q", choices.Model, pick)
	}
}

// TestModelSelectDiscoveredListHasNoDefault: ollama's list is discovered at
// runtime, so no default is injected and Enter keeps huh's behaviour of
// picking the first discovered entry.
func TestModelSelectDiscoveredListHasNoDefault(t *testing.T) {
	spec := FindSpec("ollama")
	if spec == nil {
		t.Fatal("FindSpec(\"ollama\") = nil")
	}
	var choices Choices
	sel := newModelSelect(spec, []string{"llama3:8b", "qwen2.5-coder:14b"}, &choices, nil)
	if got, _ := sel.GetValue().(string); got != "llama3:8b" {
		t.Errorf("select value = %q, want the first discovered model", got)
	}
}
