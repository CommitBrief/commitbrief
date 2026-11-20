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
	// gpt-5.5 has tiered pricing: prompts above 272K input tokens incur 2x
	// input / 1.5x output for the whole session. We snapshot the base
	// (<=272K) tier; review diffs rarely exceed it, so the verbose cost
	// footer may under-report on very large inputs.
	ModelGPT55: {
		InputPer1M:       5.00,
		OutputPer1M:      30.00,
		CachedInputPer1M: 0.50,
	},
	ModelGPT54Mini: {
		InputPer1M:       0.75,
		OutputPer1M:      4.50,
		CachedInputPer1M: 0.075,
	},
	// gpt-5.5-pro does not offer a cached-input discount.
	ModelGPT55Pro: {
		InputPer1M:       30.00,
		OutputPer1M:      180.00,
		CachedInputPer1M: 0,
	},
	// The gpt-6 family (Astra/Sol/Luna) publishes tiered long-context
	// pricing (a higher rate above a per-model input-token threshold,
	// mirroring gpt-5.5 above). We deliberately snapshot the flat base tier
	// for all three rather than modeling the tiers, same simplification as
	// gpt-5.5 above; the verbose cost footer may under-report on very
	// large inputs.
	ModelGPT6Astra: {
		InputPer1M:       10.00,
		OutputPer1M:      50.00,
		CachedInputPer1M: 1.00,
	},
	ModelGPT6Sol: {
		InputPer1M:       2.00,
		OutputPer1M:      10.00,
		CachedInputPer1M: 0.20,
	},
	ModelGPT6Luna: {
		InputPer1M:       0.10,
		OutputPer1M:      0.50,
		CachedInputPer1M: 0.01,
	},
	// gpt-6.2-astra, gpt-6.2-sol and gpt-6.1-terra bill prompts above 272K
	// input tokens at 2x input / 1.5x output for the whole request; the base
	// tier is snapshotted, same simplification as gpt-5.5 above. Source:
	// https://developers.openai.com/api/docs/pricing (accessed 2026-11-20).
	ModelGPT62Astra: {
		InputPer1M:       8.00,
		OutputPer1M:      40.00,
		CachedInputPer1M: 0.40,
	},
	ModelGPT62Sol: {
		InputPer1M:       2.00,
		OutputPer1M:      10.00,
		CachedInputPer1M: 0.10,
	},
	ModelGPT61Terra: {
		InputPer1M:       1.00,
		OutputPer1M:      5.00,
		CachedInputPer1M: 0.05,
	},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
