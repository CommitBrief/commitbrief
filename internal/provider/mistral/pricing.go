// SPDX-License-Identifier: GPL-3.0-or-later

package mistral

import "github.com/CommitBrief/commitbrief/internal/provider"

// Mistral per-1M-token pricing snapshot (USD). Source:
// https://mistral.ai/pricing — refresh on price change. Mistral does not
// expose an automatic prompt-cache discount in the OpenAI-compatible
// usage payload, so CachedInputPer1M is left at 0.
var pricingTable = map[string]provider.Pricing{
	ModelLarge:     {InputPer1M: 2.00, OutputPer1M: 6.00},
	ModelSmall:     {InputPer1M: 0.20, OutputPer1M: 0.60},
	ModelCodestral: {InputPer1M: 0.30, OutputPer1M: 0.90},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
