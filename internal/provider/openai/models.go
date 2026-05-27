// SPDX-License-Identifier: GPL-3.0-or-later

package openai

const (
	Name = "openai"

	ModelGPT4o     = "gpt-4o"
	ModelGPT4oMini = "gpt-4o-mini"

	DefaultModel = ModelGPT4o
)

var supportedModels = []string{
	ModelGPT4o,
	ModelGPT4oMini,
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
