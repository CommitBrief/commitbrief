// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import "github.com/CommitBrief/commitbrief/internal/provider"

// Gemini per-1M-token pricing snapshot (paid "Standard" tier).
// Source: https://ai.google.dev/gemini-api/docs/pricing
// gemini-3.1-pro-preview has tiered pricing (≤200K vs >200K input tokens:
// $2/$12 vs $4/$18); we snapshot the ≤200K base, so the verbose cost footer
// may under-report on very large inputs. CachedInputPer1M reflects Gemini's
// implicit-cache read discount (~0.25× input); context caching is not wired
// into the client yet, so the rate is informational for now.
var pricingTable = map[string]provider.Pricing{
	ModelPro31: {
		InputPer1M:       2.00,
		OutputPer1M:      12.00,
		CachedInputPer1M: 0.50,
	},
	ModelFlash35: {
		InputPer1M:       1.50,
		OutputPer1M:      9.00,
		CachedInputPer1M: 0.375,
	},
	ModelFlashLite31: {
		InputPer1M:       0.25,
		OutputPer1M:      1.50,
		CachedInputPer1M: 0.0625,
	},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
