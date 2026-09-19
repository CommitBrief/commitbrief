// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

import "github.com/CommitBrief/commitbrief/internal/provider"

// Metadata describes the local Ollama runtime without contacting it, so
// documentation can describe the provider on a machine where Ollama was
// never installed. See ADR-0039.
//
// The model list is Models() verbatim — a handful of setup-wizard
// suggestions, NOT an authoritative catalogue. Ollama serves whatever the
// user has pulled, and IsModelSupported accordingly accepts any non-empty
// tag; a reader must not treat an absent model as unsupported. Context
// windows come from contextWindowFor, which is itself best-effort (a
// Modelfile can raise num_ctx beyond what we advertise).
//
// Pricing is zero for every entry, and that is the answer rather than a
// gap: the tokens are spent on the user's own hardware.
func Metadata() provider.Metadata {
	suggested := Models()
	models := make([]provider.ModelInfo, 0, len(suggested))
	for _, id := range suggested {
		models = append(models, provider.ModelInfo{
			ID:            id,
			Default:       id == DefaultModel,
			ContextWindow: contextWindowFor(id),
			Pricing:       pricingFor(id),
		})
	}

	return provider.Metadata{
		Name:           Name,
		Kind:           provider.KindLocal,
		DefaultModel:   DefaultModel,
		DefaultBaseURL: DefaultBaseURL,
		Models:         models,
	}
}
