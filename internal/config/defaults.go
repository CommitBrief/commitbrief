package config

func Default() *Config {
	return &Config{
		Version:  CurrentSchemaVersion,
		Provider: "anthropic",
		Providers: map[string]ProviderConfig{
			"anthropic": {Model: "claude-opus-4-7", BaseURL: "https://api.anthropic.com"},
			"openai":    {Model: "gpt-4o", BaseURL: "https://api.openai.com/v1"},
			"gemini":    {Model: "gemini-2.5-pro"},
			"ollama":    {Model: "qwen2.5-coder:14b", BaseURL: "http://localhost:11434"},
		},
		Output: OutputConfig{
			Lang:   "en",
			Stream: true,
			Color:  "auto",
		},
		Cache: CacheConfig{
			Enabled:   true,
			TTLDays:   7,
			MaxSizeMB: 100,
		},
		Guard: GuardConfig{
			SecretScan: true,
		},
		Cost: CostConfig{
			WarnThresholdUSD: 0.50,
		},
	}
}
