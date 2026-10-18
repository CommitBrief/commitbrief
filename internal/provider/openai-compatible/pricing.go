// SPDX-License-Identifier: GPL-3.0-or-later

package openaicompat

import (
	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// pricingFor returns the user-declared price for the model, or the zero
// Pricing when none was declared. There is no built-in table: the endpoint
// and model are arbitrary. A zero price is read by the CLI as "unknown".
func pricingFor(declared map[string]config.ModelPricing, model string) provider.Pricing {
	p, ok := declared[model]
	if !ok {
		return provider.Pricing{}
	}
	return provider.Pricing{
		InputPer1M:       p.InputPer1M,
		OutputPer1M:      p.OutputPer1M,
		CachedInputPer1M: p.CachedInputPer1M,
	}
}
