// SPDX-License-Identifier: GPL-3.0-or-later

package cohere

import "github.com/CommitBrief/commitbrief/internal/provider"

// Cohere per-1M-token pricing snapshot (USD). Source:
// https://cohere.com/pricing — refresh on price change. As of 2026-11-20 the
// page publishes per-token rates only in its legacy section (Command R+
// 08-2024 $2.50/$10); the Command R 08-2024 and Command A rates below are
// the last published list prices. No automatic
// prompt-cache discount is surfaced through the compatibility endpoint,
// so CachedInputPer1M is left at 0.
var pricingTable = map[string]provider.Pricing{
	ModelCommandRPlus: {InputPer1M: 2.50, OutputPer1M: 10.00},
	ModelCommandR:     {InputPer1M: 0.15, OutputPer1M: 0.60},
	ModelCommandA:     {InputPer1M: 2.50, OutputPer1M: 10.00},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
