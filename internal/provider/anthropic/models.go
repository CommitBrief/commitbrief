// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

const (
	Name = "anthropic"

	ModelOpus48   = "claude-opus-4-8"
	ModelSonnet46 = "claude-sonnet-4-6"
	ModelHaiku45  = "claude-haiku-4-5-20251001"
	ModelOpus55   = "claude-opus-5-5"
	ModelSonnet5  = "claude-sonnet-5"
	ModelFable51  = "claude-fable-5-1"
	ModelFable52  = "claude-fable-5-2"
	ModelSonnet55 = "claude-sonnet-5-5"
	ModelHaiku55  = "claude-haiku-5-5"

	DefaultModel = ModelOpus55
)

// supportedModels' order is the order the setup wizard's model picker
// shows and, since the wizard reads spec.Models rather than DefaultModel,
// the model an Enter keypress selects (internal/setup/wizard.go). New
// models are appended at the end so the first element — and the wizard's
// implicit default — never moves out from under an existing config.
var supportedModels = []string{
	ModelOpus48,
	ModelSonnet46,
	ModelHaiku45,
	ModelOpus55,
	ModelSonnet5,
	ModelFable51,
	ModelFable52,
	ModelSonnet55,
	ModelHaiku55,
}

// noForcedToolChoiceModels are the Claude models that reject a forced
// tool_choice ("any" or "tool") with a 400 invalid_request_error on every
// request — their adaptive thinking is incompatible with forcing tool use.
// Source: https://platform.claude.com/docs/en/build-with-claude/thinking
// ("Response prefill and forced tool use" — accessed 2026-09-24): "Adaptive
// thinking … supports forced tool use, except on Claude Opus 5.5, Claude
// Fable 5.1, and Claude Mythos 5.1 … reject forced tool use on every
// request with a 400 error. On those models, use tool_choice:
// {"type": "auto"} … instead." Claude Fable 5.2 and Claude Sonnet 5.5
// (both released after that snapshot) reject it the same way; Claude Haiku
// 5.5 still accepts it (Anthropic model docs, accessed 2026-11-20).
// buildParams reads this flag; keep the per-model capability here rather
// than duplicating the model list.
var noForcedToolChoiceModels = map[string]bool{
	ModelOpus55:   true,
	ModelFable51:  true,
	ModelFable52:  true,
	ModelSonnet55: true,
}

// supportsForcedToolChoice reports whether buildParams may force the
// report tool via tool_choice. Opus 5.5, Fable 5.1/5.2 and Sonnet 5.5 do not — see
// noForcedToolChoiceModels.
func supportsForcedToolChoice(model string) bool {
	return !noForcedToolChoiceModels[model]
}

// adaptiveThinkingDefaultMaxTokensModels get a higher default max_tokens
// than the historical 4096 when the caller specifies none. Adaptive thinking
// is on by default on these models and cannot be turned off at all on Opus
// 5.5 and Fable 5.1 (the effort-"high" disable threshold in the same source
// applies to Opus 5, not Opus 5.5). Thinking tokens count toward max_tokens,
// so a low ceiling can truncate or starve the visible response. 16000
// mirrors the max_tokens Anthropic's own adaptive-thinking examples use.
// Same source as noForcedToolChoiceModels; deliberately a separate map since
// it tracks a different capability — Sonnet 5 and Haiku 5.5 also have
// thinking on by default (need the raised ceiling here) but, unlike Opus
// 5.5/Fable 5.1/Fable 5.2/Sonnet 5.5, still accept a forced tool_choice
// (stay out of noForcedToolChoiceModels).
var adaptiveThinkingDefaultMaxTokensModels = map[string]bool{
	ModelOpus55:   true,
	ModelFable51:  true,
	ModelSonnet5:  true,
	ModelFable52:  true,
	ModelSonnet55: true,
	ModelHaiku55:  true,
}

// defaultMaxTokensFor returns the output-token ceiling to use when the
// caller did not specify one.
func defaultMaxTokensFor(model string) int64 {
	if adaptiveThinkingDefaultMaxTokensModels[model] {
		return defaultAdaptiveThinkingMaxTokens
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
