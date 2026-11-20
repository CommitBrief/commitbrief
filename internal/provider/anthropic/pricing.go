// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import "github.com/CommitBrief/commitbrief/internal/provider"

// Anthropic per-1M-token pricing snapshot. The actual rates are maintained
// at https://anthropic.com/pricing; this table is a release-time copy and
// should be refreshed when a model price changes. Cached input is the
// "prompt cache read" rate per Anthropic's ephemeral cache discount.
//
// claude-haiku-5-5 has two rate cards chosen by prompt length: the base
// (<=100K input tokens) card is snapshotted below; prompts above 100K bill
// at $0.50 / $2.50, so the verbose cost footer may under-report on very
// large inputs (same simplification as the long-context tiers elsewhere).
var pricingTable = map[string]provider.Pricing{
	ModelFable52: {
		InputPer1M:       8.00,
		OutputPer1M:      40.00,
		CachedInputPer1M: 0.20,
	},
	ModelSonnet55: {
		InputPer1M:       2.00,
		OutputPer1M:      10.00,
		CachedInputPer1M: 0.10,
	},
	ModelHaiku55: {
		InputPer1M:       0.10,
		OutputPer1M:      0.50,
		CachedInputPer1M: 0.01,
	},
	ModelOpus55: {
		InputPer1M:       4.00,
		OutputPer1M:      20.00,
		CachedInputPer1M: 0.20,
	},
	ModelSonnet5: {
		InputPer1M:       2.00,
		OutputPer1M:      10.00,
		CachedInputPer1M: 0.20,
	},
	ModelFable51: {
		InputPer1M:       10.00,
		OutputPer1M:      50.00,
		CachedInputPer1M: 0.25,
	},
	ModelOpus48: {
		InputPer1M:       5.00,
		OutputPer1M:      25.00,
		CachedInputPer1M: 0.50,
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
