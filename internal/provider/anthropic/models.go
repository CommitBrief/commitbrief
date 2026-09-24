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

	DefaultModel = ModelOpus48
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
}

// noForcedToolChoiceModels are the Claude models that reject a forced
// tool_choice ("any" or "tool") with a 400 invalid_request_error on every
// request — their adaptive thinking is incompatible with forcing tool use.
// Source: https://platform.claude.com/docs/en/build-with-claude/thinking
// ("Response prefill and forced tool use" — accessed 2026-09-24): "Adaptive
// thinking … supports forced tool use, except on Claude Opus 5.5, Claude
// Fable 5.1, and Claude Mythos 5.1 … reject forced tool use on every
// request with a 400 error. On those models, use tool_choice:
// {"type": "auto"} … instead." buildParams reads this flag; keep the
// per-model capability here rather than duplicating the model list.
var noForcedToolChoiceModels = map[string]bool{
	ModelOpus55:  true,
	ModelFable51: true,
}

// supportsForcedToolChoice reports whether buildParams may force the
// report tool via tool_choice. Opus 5.5 and Fable 5.1 do not — see
// noForcedToolChoiceModels.
func supportsForcedToolChoice(model string) bool {
	return !noForcedToolChoiceModels[model]
}

// extendedThinkingDefaultMaxTokensModels get a higher default max_tokens
// than the historical 4096 when the caller specifies none. Thinking tokens
// count toward max_tokens on these models (adaptive thinking is on by
// default and cannot be fully disabled on Opus 5.5 above effort "high" —
// same source as noForcedToolChoiceModels), so a low ceiling can truncate
// or starve the visible response. 16000 mirrors the max_tokens Anthropic's
// own adaptive-thinking examples use.
var extendedThinkingDefaultMaxTokensModels = map[string]bool{
	ModelOpus55:  true,
	ModelFable51: true,
}

// defaultMaxTokensFor returns the output-token ceiling to use when the
// caller did not specify one.
func defaultMaxTokensFor(model string) int64 {
	if extendedThinkingDefaultMaxTokensModels[model] {
		return defaultExtendedThinkingMaxTokens
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
