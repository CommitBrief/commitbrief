// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

const (
	Name = "anthropic"

	ModelOpus48   = "claude-opus-4-8"
	ModelSonnet46 = "claude-sonnet-4-6"
	ModelHaiku45  = "claude-haiku-4-5-20251001"

	DefaultModel = ModelOpus48
)

var supportedModels = []string{
	ModelOpus48,
	ModelSonnet46,
	ModelHaiku45,
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
