package config

const CurrentSchemaVersion = 1

type Config struct {
	Version   int                       `yaml:"version"`
	Provider  string                    `yaml:"provider"`
	Providers map[string]ProviderConfig `yaml:"providers"`
	Output    OutputConfig              `yaml:"output"`
	Cache     CacheConfig               `yaml:"cache"`
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
