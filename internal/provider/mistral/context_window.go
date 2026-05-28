// SPDX-License-Identifier: GPL-3.0-or-later

package mistral

const defaultContextWindow = 128_000

var contextWindows = map[string]int{
	ModelLarge:     128_000,
	ModelSmall:     32_000,
	ModelCodestral: 256_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
