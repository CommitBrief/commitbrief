// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

const (
	Name = "ollama"

	// DefaultModel is the placeholder model name used when the caller has
	// not configured one. Ollama installs are user-specific (models must
	// be pulled locally with `ollama pull`); the setup wizard discovers
	// the actual list via /api/tags. We keep a common default name so
	// out-of-the-box TestConnection gives a recognizable error if absent.
	DefaultModel = "qwen2.5-coder:14b"
)

// Models returns a small list of known-popular Ollama models for setup-
// wizard suggestions when /api/tags discovery fails (offline / first run).
// The authoritative list is whatever `ollama list` reports on the user's
// machine; the wizard falls back to free-text entry when needed.
func Models() []string {
	return []string{
		"qwen2.5-coder:14b",
		"qwen2.5-coder:7b",
		"llama3.3:latest",
		"llama3.2:latest",
		"deepseek-coder-v2:latest",
	}
}

// IsModelSupported is intentionally permissive: Ollama accepts any model
// tag the user has pulled, so the only invalid input is the empty string.
func IsModelSupported(model string) bool {
	return model != ""
}
