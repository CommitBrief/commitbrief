// SPDX-License-Identifier: GPL-3.0-or-later

package mistral

import "github.com/CommitBrief/commitbrief/internal/provider"

// Mistral per-1M-token pricing snapshot (USD). Source:
// https://docs.mistral.ai/inference/pricing (accessed 2026-11-20) — refresh
// on price change. The -latest aliases resolve to Mistral Large 4, Mistral
// Small 4 and Codestral 25.08. The pricing page lists cached-input rates, but
// the OpenAI-compatible usage payload does not report cached tokens, so
// CachedInputPer1M is left at 0.
var pricingTable = map[string]provider.Pricing{
	ModelLarge:     {InputPer1M: 0.70, OutputPer1M: 2.10},
	ModelSmall:     {InputPer1M: 0.15, OutputPer1M: 0.60},
	ModelCodestral: {InputPer1M: 0.30, OutputPer1M: 0.90},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
