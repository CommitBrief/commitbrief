// SPDX-License-Identifier: GPL-3.0-or-later

package cohere

import "github.com/CommitBrief/commitbrief/internal/provider"

// apiKeyEnv is the environment variable internal/config reads this
// provider's credential from. It is spelled out here rather than imported
// because internal/config does not depend on the provider packages, and a
// reader of the generated inventory needs the name even when the variable
// is unset.
const apiKeyEnv = "COHERE_API_KEY"

// Metadata describes cohere without needing a credential, so documentation
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
		Name:           Name,
		Kind:           provider.KindAPI,
		DefaultModel:   DefaultModel,
		APIKeyEnv:      apiKeyEnv,
		DefaultBaseURL: defaultBaseURL,
		Models:         models,
	}
}
