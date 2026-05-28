// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

func TestResolvePricing(t *testing.T) {
	const model = "mock-model"
	base := provider.Pricing{InputPer1M: 1.0, OutputPer1M: 2.0, CachedInputPer1M: 0.5}
	prov := mock.New()
	prov.PricingValue = base

	withOverride := func(mp config.ModelPricing) *config.Config {
		return &config.Config{
			Provider:  "mock",
			Providers: map[string]config.ProviderConfig{"mock": {Pricing: map[string]config.ModelPricing{model: mp}}},
		}
	}

	// nil cfg → built-in.
	if got := resolvePricing(nil, prov, model); got != base {
		t.Errorf("nil cfg should yield built-in, got %+v", got)
	}

	// No pricing map → built-in.
	cfgNone := &config.Config{Provider: "mock", Providers: map[string]config.ProviderConfig{"mock": {}}}
	if got := resolvePricing(cfgNone, prov, model); got != base {
		t.Errorf("no override should yield built-in, got %+v", got)
	}

	// Full override replaces all three.
	got := resolvePricing(withOverride(config.ModelPricing{InputPer1M: 9, OutputPer1M: 8, CachedInputPer1M: 7}), prov, model)
	if got.InputPer1M != 9 || got.OutputPer1M != 8 || got.CachedInputPer1M != 7 {
		t.Errorf("full override = %+v, want {9,8,7}", got)
	}

	// Partial override (only output) keeps built-in for the rest.
	got = resolvePricing(withOverride(config.ModelPricing{OutputPer1M: 99}), prov, model)
	if got.InputPer1M != 1.0 || got.OutputPer1M != 99 || got.CachedInputPer1M != 0.5 {
		t.Errorf("partial override = %+v, want {1.0, 99, 0.5}", got)
	}

	// Override for a different model → no effect on this model.
	cfgOther := &config.Config{
		Provider:  "mock",
		Providers: map[string]config.ProviderConfig{"mock": {Pricing: map[string]config.ModelPricing{"other": {InputPer1M: 50}}}},
	}
	if got := resolvePricing(cfgOther, prov, model); got != base {
		t.Errorf("override for a different model should not apply, got %+v", got)
	}
}
