// SPDX-License-Identifier: GPL-3.0-or-later

package openai

const (
	Name = "openai"

	ModelGPT4o     = "gpt-4o"
	ModelGPT4oMini = "gpt-4o-mini"
	ModelGPT55     = "gpt-5.5"
	ModelGPT54Mini = "gpt-5.4-mini"
	ModelGPT55Pro  = "gpt-5.5-pro"

	DefaultModel = ModelGPT54Mini
)

var supportedModels = []string{
	ModelGPT54Mini,
	ModelGPT55,
	ModelGPT55Pro,
	ModelGPT4o,
	ModelGPT4oMini,
}

// responsesAPIModels are served only through the Responses API rather than
// Chat Completions (gpt-5.5-pro operates exclusively there per OpenAI's
// model docs). Review/TestConnection route these calls differently.
var responsesAPIModels = map[string]bool{
	ModelGPT55Pro: true,
}

// usesResponsesAPI reports whether the model must be driven through the
// Responses API instead of Chat Completions.
func usesResponsesAPI(model string) bool {
	return responsesAPIModels[model]
}

// reasoningModels are the GPT-5 family models that spend reasoning tokens
// out of the output-token budget. They need a larger default ceiling than
// the gpt-4o-era 4096 so a findings JSON isn't truncated by reasoning.
var reasoningModels = map[string]bool{
	ModelGPT55:     true,
	ModelGPT54Mini: true,
	ModelGPT55Pro:  true,
}

// defaultMaxTokensFor returns the output-token ceiling to use when the
// caller did not specify one. Reasoning models get a higher budget;
// everything else keeps the historical gpt-4o default.
func defaultMaxTokensFor(model string) int64 {
	if reasoningModels[model] {
		return defaultReasoningMaxTokens
	}
	return defaultMaxTokens
}

func Models() []string {
	out := make([]string, len(supportedModels))
	copy(out, supportedModels)
	return out
}

func IsModelSupported(model string) bool {
	for _, m := range supportedModels {
		if m == model {
			return true
		}
	}
	return false
}
