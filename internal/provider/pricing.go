// SPDX-License-Identifier: GPL-3.0-or-later

package provider

type Pricing struct {
	InputPer1M  float64
	OutputPer1M float64

	// CachedInputPer1M is the per-1M-token price for cached input tokens.
	// Zero means "same as InputPer1M" (no cache discount or not supported).
	CachedInputPer1M float64
}

func (p Pricing) Cost(u Usage) float64 {
	cached := u.CachedInputTokens
	if cached > u.InputTokens {
		cached = u.InputTokens
	}
	uncached := u.InputTokens - cached

	cachedRate := p.CachedInputPer1M
	if cachedRate == 0 {
		cachedRate = p.InputPer1M
	}

	return (float64(uncached)*p.InputPer1M +
		float64(cached)*cachedRate +
		float64(u.OutputTokens)*p.OutputPer1M) / 1_000_000
}
