// SPDX-License-Identifier: GPL-3.0-or-later

package cohere

const (
	Name = "cohere"

	ModelCommandRPlus = "command-r-plus"
	ModelCommandR     = "command-r"
	ModelCommandA     = "command-a-03-2025"

	DefaultModel = ModelCommandRPlus
)

var supportedModels = []string{ModelCommandRPlus, ModelCommandR, ModelCommandA}

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
