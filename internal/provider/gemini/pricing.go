// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import "github.com/CommitBrief/commitbrief/internal/provider"

// Gemini per-1M-token pricing snapshot (paid "Standard" tier).
// Source: https://ai.google.dev/gemini-api/docs/pricing (accessed 2026-09-24)
// gemini-3.1-pro-preview has tiered pricing (≤200K vs >200K input tokens:
// $2/$12 vs $4/$18); we snapshot the ≤200K base, so the verbose cost footer
// may under-report on very large inputs. CachedInputPer1M is the page's
// published "Context caching" read price (context-caching storage cost is
// out of scope for this table).
// gemini-3.8-flash's rates below are the through-2026-12-31 tier; the page
// lists a scheduled increase to $1.50/$7.50/$0.15 starting 2027-01-01.
var pricingTable = map[string]provider.Pricing{
	ModelPro31: {
		InputPer1M:       2.00,
		OutputPer1M:      12.00,
		CachedInputPer1M: 0.20,
	},
	ModelFlash35: {
		InputPer1M:       1.50,
		OutputPer1M:      9.00,
		CachedInputPer1M: 0.15,
	},
	ModelFlashLite31: {
		InputPer1M:       0.25,
		OutputPer1M:      1.50,
		CachedInputPer1M: 0.025,
	},
	ModelFlash38: {
		InputPer1M:       0.75,
		OutputPer1M:      3.75,
		CachedInputPer1M: 0.075,
	},
	ModelFlashLite35: {
		InputPer1M:       0.30,
		OutputPer1M:      2.50,
		CachedInputPer1M: 0.03,
	},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
