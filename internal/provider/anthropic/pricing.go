// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import "github.com/CommitBrief/commitbrief/internal/provider"

// Anthropic per-1M-token pricing snapshot. The actual rates are maintained
// at https://anthropic.com/pricing; this table is a release-time copy and
// should be refreshed when a model price changes. Cached input is the
// "prompt cache read" rate per Anthropic's ephemeral cache discount.
var pricingTable = map[string]provider.Pricing{
	ModelOpus47: {
		InputPer1M:       15.00,
		OutputPer1M:      75.00,
		CachedInputPer1M: 1.50,
	},
	ModelSonnet46: {
		InputPer1M:       3.00,
		OutputPer1M:      15.00,
		CachedInputPer1M: 0.30,
	},
	ModelHaiku45: {
		InputPer1M:       1.00,
		OutputPer1M:      5.00,
		CachedInputPer1M: 0.10,
	},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
