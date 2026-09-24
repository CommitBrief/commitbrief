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
