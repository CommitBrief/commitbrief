// SPDX-License-Identifier: GPL-3.0-or-later

package openai

const (
	Name = "openai"

	ModelGPT4o      = "gpt-4o"
	ModelGPT4oMini  = "gpt-4o-mini"
	ModelGPT55      = "gpt-5.5"
	ModelGPT54Mini  = "gpt-5.4-mini"
	ModelGPT55Pro   = "gpt-5.5-pro"
	ModelGPT6Astra  = "gpt-6-astra"
	ModelGPT6Sol    = "gpt-6-sol"
	ModelGPT6Luna   = "gpt-6-luna"
	ModelGPT62Astra = "gpt-6.2-astra"
	ModelGPT62Sol   = "gpt-6.2-sol"
	ModelGPT61Terra = "gpt-6.1-terra"

	DefaultModel = ModelGPT54Mini
)

var supportedModels = []string{
	ModelGPT54Mini,
	ModelGPT55,
	ModelGPT55Pro,
	ModelGPT6Astra,
	ModelGPT6Sol,
	ModelGPT6Luna,
	ModelGPT4o,
	ModelGPT4oMini,
	ModelGPT62Astra,
	ModelGPT62Sol,
	ModelGPT61Terra,
}

// responsesAPIModels are served only through the Responses API rather than
// Chat Completions (gpt-5.5-pro and the gpt-6 family operate exclusively
// there per OpenAI's model docs). Review/TestConnection route these calls
// differently. gpt-6.2-astra, gpt-6.2-sol and gpt-6.1-terra are served on
// Chat Completions as well; Chat's json_schema response_format needs no tool
// calling (the one feature gpt-6.1-terra reserves for the Responses API), so
// they stay on the Chat path.
var responsesAPIModels = map[string]bool{
	ModelGPT55Pro:  true,
	ModelGPT6Astra: true,
	ModelGPT6Sol:   true,
	ModelGPT6Luna:  true,
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
	ModelGPT55:      true,
	ModelGPT54Mini:  true,
	ModelGPT55Pro:   true,
	ModelGPT6Astra:  true,
	ModelGPT6Sol:    true,
	ModelGPT6Luna:   true,
	ModelGPT62Astra: true,
	ModelGPT62Sol:   true,
	ModelGPT61Terra: true,
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
