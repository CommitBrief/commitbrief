// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

const defaultContextWindow = 1_000_000

var contextWindows = map[string]int{
	ModelPro2_5:   2_000_000,
	ModelFlash2_5: 1_000_000,
	ModelFlash1_5: 1_000_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
