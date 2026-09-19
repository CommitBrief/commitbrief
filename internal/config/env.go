// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "os"

// Per-provider environment variable names. These are the single source of
// truth for which env var each provider's credential (or, for ollama, base
// URL) comes from: ApplyEnv below reads them, and each provider package's
// Metadata() (internal/provider/*/metadata.go) imports these same constants
// for its APIKeyEnv field instead of re-spelling the literal. Before this,
// six provider packages carried their own `const apiKeyEnv = "..."` copy
// with nothing tying it back to the value actually read here — a rename
// here would have left the generated inventory (internal/meta/surface.json)
// and README pointing at a dead variable name while every test still passed.
const (
	AnthropicAPIKeyEnv = "ANTHROPIC_API_KEY"
	OpenAIAPIKeyEnv    = "OPENAI_API_KEY"
	GeminiAPIKeyEnv    = "GEMINI_API_KEY"
	DeepSeekAPIKeyEnv  = "DEEPSEEK_API_KEY"
	MistralAPIKeyEnv   = "MISTRAL_API_KEY"
	CohereAPIKeyEnv    = "COHERE_API_KEY"
	OllamaHostEnv      = "OLLAMA_HOST"
)

func ApplyEnv(c *Config) {
	if v := os.Getenv("COMMITBRIEF_PROVIDER"); v != "" {
		c.Provider = v
	}
	if v := os.Getenv("COMMITBRIEF_MODEL"); v != "" {
		if c.Provider != "" {
			setProviderField(c, c.Provider, func(p *ProviderConfig) { p.Model = v })
		}
	}
	if v := os.Getenv(AnthropicAPIKeyEnv); v != "" {
		setProviderField(c, "anthropic", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv(OpenAIAPIKeyEnv); v != "" {
		setProviderField(c, "openai", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv(GeminiAPIKeyEnv); v != "" {
		setProviderField(c, "gemini", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv(DeepSeekAPIKeyEnv); v != "" {
		setProviderField(c, "deepseek", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv(MistralAPIKeyEnv); v != "" {
		setProviderField(c, "mistral", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv(CohereAPIKeyEnv); v != "" {
		setProviderField(c, "cohere", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv(OllamaHostEnv); v != "" {
		setProviderField(c, "ollama", func(p *ProviderConfig) { p.BaseURL = v })
	}
}

func setProviderField(c *Config, name string, mutate func(*ProviderConfig)) {
	if c.Providers == nil {
		c.Providers = map[string]ProviderConfig{}
	}
	p := c.Providers[name]
	mutate(&p)
	c.Providers[name] = p
}
