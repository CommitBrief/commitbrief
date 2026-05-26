package config

const CurrentSchemaVersion = 1

type Config struct {
	Version   int                       `yaml:"version"`
	Provider  string                    `yaml:"provider"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Output    OutputConfig              `yaml:"output"`
	Cache     CacheConfig               `yaml:"cache"`
	Guard     GuardConfig               `yaml:"guard"`
	Cost      CostConfig                `yaml:"cost"`
}

// CostConfig controls the pre-send cost preflight added in v0.8.0.
// WarnThresholdUSD is the estimated-cost ceiling above which the CLI
// will prompt (TTY) or abort (non-TTY) before contacting the provider.
// Zero or negative disables the check entirely; the default of 0.50 is
// a "occasional dev review" budget — users running scheduled jobs
// should bump it via config or pass --no-cost-check per-invocation.
type CostConfig struct {
	WarnThresholdUSD float64 `yaml:"warn_threshold_usd"`
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
