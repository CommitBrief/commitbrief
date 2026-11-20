// SPDX-License-Identifier: GPL-3.0-or-later

package mistral

const defaultContextWindow = 128_000

var contextWindows = map[string]int{
	ModelLarge:     1_000_000,
	ModelSmall:     256_000,
	ModelCodestral: 128_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
