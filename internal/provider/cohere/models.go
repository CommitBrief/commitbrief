// SPDX-License-Identifier: GPL-3.0-or-later

package cohere

const (
	Name = "cohere"

	// The bare command-r-plus / command-r aliases point at the 2024-03/04
	// releases Cohere deprecated on 2025-09-15; the catalog pins the dated
	// 08-2024 releases Cohere names as their replacements.
	ModelCommandRPlus = "command-r-plus-08-2024"
	ModelCommandR     = "command-r-08-2024"
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
