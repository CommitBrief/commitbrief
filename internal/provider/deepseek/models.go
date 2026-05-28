// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

const (
	Name = "deepseek"

	ModelChat     = "deepseek-chat"
	ModelReasoner = "deepseek-reasoner"

	DefaultModel = ModelChat
)

var supportedModels = []string{ModelChat, ModelReasoner}

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
