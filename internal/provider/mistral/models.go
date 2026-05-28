// SPDX-License-Identifier: GPL-3.0-or-later

package mistral

const (
	Name = "mistral"

	ModelLarge     = "mistral-large-latest"
	ModelSmall     = "mistral-small-latest"
	ModelCodestral = "codestral-latest"

	DefaultModel = ModelLarge
)

var supportedModels = []string{ModelLarge, ModelSmall, ModelCodestral}

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
