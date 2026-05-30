// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

const defaultContextWindow = 200_000

var contextWindows = map[string]int{
	ModelOpus48:   1_000_000,
	ModelSonnet46: 1_000_000,
	ModelHaiku45:  200_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
