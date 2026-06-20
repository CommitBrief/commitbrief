// SPDX-License-Identifier: GPL-3.0-or-later

package config

func Default() *Config {
	return &Config{
		Version:  CurrentSchemaVersion,
		Provider: "anthropic",
		Providers: map[string]ProviderConfig{
			"anthropic": {Model: "claude-opus-4-8", BaseURL: "https://api.anthropic.com"},
			"openai":    {Model: "gpt-5.4-mini", BaseURL: "https://api.openai.com/v1"},
			"gemini":    {Model: "gemini-3.5-flash"},
			"ollama":    {Model: "qwen2.5-coder:14b", BaseURL: "http://localhost:11434"},
		},
		Output: OutputConfig{
			Lang:   "en",
			Stream: true,
			Color:  "auto",
		},
		Cache: CacheConfig{
			Enabled: true,
			TTLDays: 7,
		},
		Guard: GuardConfig{
			SecretScan:    true,
			InjectionScan: true,
		},
		Cost: CostConfig{
			WarnThresholdUSD: 0.50,
		},
		Commit: CommitConfig{
			Type:     "plain",
			Generate: 1,
		},
		Review: ReviewConfig{
			Flaky:    true,
			Baseline: true,
		},
	}
}
