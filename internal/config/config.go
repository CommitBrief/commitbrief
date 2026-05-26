package config

const CurrentSchemaVersion = 1

type Config struct {
	Version   int                       `yaml:"version"`
	Provider  string                    `yaml:"provider"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Output    OutputConfig              `yaml:"output"`
	Cache     CacheConfig               `yaml:"cache"`
	Guard     GuardConfig               `yaml:"guard"`
}

// GuardConfig toggles pre-send protections that don't quite fit
// elsewhere. SecretScan controls the credential-pattern scan added in
// v0.8.0 (ADR-0007 follow-up); leaving it true is the safe default,
// false disables it entirely for users who pipeline outputs through a
// secrets manager and don't want the prompt.
type GuardConfig struct {
	SecretScan bool `yaml:"secret_scan"`
}

type ProviderConfig struct {
	APIKey  string `yaml:"api_key,omitempty"`
	Model   string `yaml:"model,omitempty"`
	BaseURL string `yaml:"base_url,omitempty"`
}

type OutputConfig struct {
	Lang   string `yaml:"lang"`
	Stream bool   `yaml:"stream"`
	Color  string `yaml:"color"`
}

type CacheConfig struct {
	Enabled   bool `yaml:"enabled"`
	TTLDays   int  `yaml:"ttl_days"`
	MaxSizeMB int  `yaml:"max_size_mb"`
}
