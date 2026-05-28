// SPDX-License-Identifier: GPL-3.0-or-later

package cohere

const defaultContextWindow = 128_000

var contextWindows = map[string]int{
	ModelCommandRPlus: 128_000,
	ModelCommandR:     128_000,
	ModelCommandA:     256_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
