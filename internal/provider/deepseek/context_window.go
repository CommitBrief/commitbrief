// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

const defaultContextWindow = 64_000

var contextWindows = map[string]int{
	ModelChat:     64_000,
	ModelReasoner: 64_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
