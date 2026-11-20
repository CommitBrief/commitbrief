// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

const (
	Name = "deepseek"

	// deepseek-chat and deepseek-reasoner were retired on 2026-07-24 and
	// are no longer reachable. deepseek-flash (DeepSeek-V4.1-Flash) and
	// deepseek-v4-pro replace them; both think by default.
	ModelFlash = "deepseek-flash"
	ModelV4Pro = "deepseek-v4-pro"

	DefaultModel = ModelFlash
)

var supportedModels = []string{ModelFlash, ModelV4Pro}

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
