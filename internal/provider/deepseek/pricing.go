// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

import "github.com/CommitBrief/commitbrief/internal/provider"

// DeepSeek per-1M-token pricing snapshot (USD, off-peak cache-miss rates).
// Source: https://api-docs.deepseek.com/quick_start/pricing (accessed
// 2026-11-20) — refresh on price change. Peak hours (01:00–04:00 and
// 06:00–10:00 UTC, Monday–Friday) bill at 2x these rates; the off-peak card
// is snapshotted, so the verbose cost footer may under-report during peak.
// CachedInputPer1M reflects DeepSeek's cache-hit discount; whether the
// OpenAI-compatible usage payload reports cached tokens varies by model.
var pricingTable = map[string]provider.Pricing{
	ModelFlash: {InputPer1M: 0.15, OutputPer1M: 0.60, CachedInputPer1M: 0.003},
	ModelV4Pro: {InputPer1M: 0.66, OutputPer1M: 1.98, CachedInputPer1M: 0.022},
}

func pricingFor(model string) provider.Pricing {
	if p, ok := pricingTable[model]; ok {
		return p
	}
	return provider.Pricing{}
}
