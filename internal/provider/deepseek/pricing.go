// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

import "github.com/CommitBrief/commitbrief/internal/provider"

// DeepSeek per-1M-token pricing snapshot (USD, standard/cache-miss rates).
// Source: https://api-docs.deepseek.com — refresh on price change.
// CachedInputPer1M reflects DeepSeek's cache-hit discount; whether the
// OpenAI-compatible usage payload reports cached tokens varies by model.
var pricingTable = map[string]provider.Pricing{
	ModelChat:     {InputPer1M: 0.27, OutputPer1M: 1.10, CachedInputPer1M: 0.07},
	ModelReasoner: {InputPer1M: 0.55, OutputPer1M: 2.19, CachedInputPer1M: 0.14},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
