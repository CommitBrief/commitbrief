// SPDX-License-Identifier: GPL-3.0-or-later

package config

import "os"

func ApplyEnv(c *Config) {
	if v := os.Getenv("COMMITBRIEF_PROVIDER"); v != "" {
		c.Provider = v
	}
	if v := os.Getenv("COMMITBRIEF_MODEL"); v != "" {
		if c.Provider != "" {
			setProviderField(c, c.Provider, func(p *ProviderConfig) { p.Model = v })
		}
	}
	if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
		setProviderField(c, "anthropic", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv("OPENAI_API_KEY"); v != "" {
		setProviderField(c, "openai", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv("GEMINI_API_KEY"); v != "" {
		setProviderField(c, "gemini", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv("DEEPSEEK_API_KEY"); v != "" {
		setProviderField(c, "deepseek", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv("MISTRAL_API_KEY"); v != "" {
		setProviderField(c, "mistral", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv("COHERE_API_KEY"); v != "" {
		setProviderField(c, "cohere", func(p *ProviderConfig) { p.APIKey = v })
	}
	if v := os.Getenv("OLLAMA_HOST"); v != "" {
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
