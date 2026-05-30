// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

const defaultContextWindow = 1_000_000

// Gemini 3.x family baseline input window is 1M tokens. Exact published
// limits for these preview models were not yet broken out in the model
// docs at integration time; 1M is the documented floor and the safe value
// for the over-context guard. Bump if Google publishes larger windows.
var contextWindows = map[string]int{
	ModelPro31:       1_000_000,
	ModelFlash35:     1_000_000,
	ModelFlashLite31: 1_000_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
