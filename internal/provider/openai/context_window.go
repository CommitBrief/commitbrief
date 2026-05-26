package openai

const defaultContextWindow = 128_000

var contextWindows = map[string]int{
	ModelGPT4o:     128_000,
	ModelGPT4oMini: 128_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
