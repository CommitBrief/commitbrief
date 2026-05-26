package gemini

import "github.com/CommitBrief/commitbrief/internal/provider"

// Gemini per-1M-token pricing snapshot (paid tier).
// Source: https://ai.google.dev/gemini-api/docs/pricing
// CachedInputPer1M reflects Gemini's context-caching discount (separate
// API; we track the per-token rate so cost reporting is accurate when a
// cache is wired up in a future phase).
var pricingTable = map[string]provider.Pricing{
	ModelPro2_5: {
		InputPer1M:       1.25,
		OutputPer1M:      10.00,
		CachedInputPer1M: 0.31,
	},
	ModelFlash2_5: {
		InputPer1M:       0.30,
		OutputPer1M:      2.50,
		CachedInputPer1M: 0.075,
	},
	ModelFlash1_5: {
		InputPer1M:       0.075,
		OutputPer1M:      0.30,
		CachedInputPer1M: 0.01875,
	},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
