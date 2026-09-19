// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// apiKeyEnv aliases the constant internal/config actually reads this
// provider's credential from (internal/config/env.go), so a rename there
// cannot silently leave this package — and the generated inventory it
// feeds — pointing at a dead variable name.
const apiKeyEnv = config.GeminiAPIKeyEnv

// Metadata describes gemini without needing a credential, so documentation
// can list every model, context window and price for a reader who has never
// configured this provider. See ADR-0039.
//
// Every field is derived from this package's own tables — supportedModels
// for the set and its order, contextWindowFor and pricingFor for the
// numbers — so adding a model there surfaces it here automatically. A
// hand-written literal would just relocate the duplication.
func Metadata() provider.Metadata {
	models := make([]provider.ModelInfo, 0, len(supportedModels))
	for _, id := range supportedModels {
		models = append(models, provider.ModelInfo{
			ID:            id,
			Default:       id == DefaultModel,
			ContextWindow: contextWindowFor(id),
			Pricing:       pricingFor(id),
		})
	}

	return provider.Metadata{
		Name:         Name,
		Kind:         provider.KindAPI,
		DefaultModel: DefaultModel,
		APIKeyEnv:    apiKeyEnv,
		Models:       models,
	}
}
