// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// resolvePricing returns the effective per-model pricing: the provider's
// built-in rate table, with any user override from
// `providers.<active>.pricing.<model>` merged on top (OQ-09). Only
// non-zero override fields apply, so a partial override (e.g. just
// output_per_1m) keeps the built-in value for the rest. Used everywhere a
// dollar figure is computed (cost preflight, verbose footer, cached cost,
// dry-run, compress savings) so the override is honored uniformly.
func resolvePricing(cfg *config.Config, prov provider.Provider, model string) provider.Pricing {
	base := prov.Pricing(model)
	if cfg == nil {
		return base
	}
	pc, ok := cfg.Providers[cfg.Provider]
	if !ok {
		return base
	}
	mp, ok := pc.Pricing[model]
	if !ok {
		return base
	}
	if mp.InputPer1M != 0 {
		base.InputPer1M = mp.InputPer1M
	}
	if mp.OutputPer1M != 0 {
		base.OutputPer1M = mp.OutputPer1M
	}
	if mp.CachedInputPer1M != 0 {
		base.CachedInputPer1M = mp.CachedInputPer1M
	}
	return base
}
