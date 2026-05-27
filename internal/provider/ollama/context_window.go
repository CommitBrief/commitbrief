// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

// defaultContextWindow is a conservative fallback for unknown local
// models. Ollama doesn't expose context window over the /api/chat
// endpoint; users running large-context models can configure their
// modelfile and we honor whatever the server enforces.
const defaultContextWindow = 8_192

// contextWindowFor returns a per-model hint. The map is best-effort:
// Ollama lets users override num_ctx via Modelfile, so the actual ceiling
// can be larger than what we advertise here.
var contextWindows = map[string]int{
	"qwen2.5-coder:14b": 32_768,
	"qwen2.5-coder:7b":  32_768,
	"llama3.3:latest":   128_000,
	"llama3.2:latest":   128_000,
}

func contextWindowFor(model string) int {
	if w, ok := contextWindows[model]; ok {
		return w
	}
	return defaultContextWindow
}
