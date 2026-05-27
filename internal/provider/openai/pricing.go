// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import "github.com/CommitBrief/commitbrief/internal/provider"

// OpenAI per-1M-token pricing snapshot. Rates from
// https://openai.com/api/pricing; refresh when a model price changes.
// `CachedInputPer1M` reflects OpenAI's automatic prompt-caching discount
// (kicks in at >=1024 tokens of repeated prefix); cached tokens are
// reported under `usage.prompt_tokens_details.cached_tokens`.
var pricingTable = map[string]provider.Pricing{
	ModelGPT4o: {
		InputPer1M:       2.50,
		OutputPer1M:      10.00,
		CachedInputPer1M: 1.25,
	},
	ModelGPT4oMini: {
		InputPer1M:       0.15,
		OutputPer1M:      0.60,
		CachedInputPer1M: 0.075,
	},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
