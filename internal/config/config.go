// SPDX-License-Identifier: GPL-3.0-or-later

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
	Command   CommandConfig             `yaml:"command"`
	Commit    CommitConfig              `yaml:"commit"`
}

// CommitConfig sets defaults for the `commit` command (ADR-0019) so a repo
// or user can pin a preferred message format / suggestion count without
// retyping flags. Precedence is flag > config > built-in default. Type is
// one of plain|conventional|conventional+body|gitmoji|subject+body; an
// empty value means "use the built-in default" (plain). Generate is the
// number of suggestions to offer; zero/negative means the built-in default
// (1). The values are validated at the CLI layer, not here, so a stale
// config never blocks loading.
type CommitConfig struct {
	Type     string `yaml:"type"`
	Generate int    `yaml:"generate"`
}

// CommandConfig customizes the bare `commitbrief` invocation. Default is
// the argument string applied when `commitbrief` is run with NO arguments
// at all — e.g. "--unstaged --cli gemini" makes a bare `commitbrief`
// behave like `commitbrief --unstaged --cli gemini`. Empty (the default)
// preserves the built-in behavior, `commitbrief` == `commitbrief --staged`.
// It only fires for the truly bare invocation; passing any flag or
// subcommand bypasses it entirely (the user is being explicit). Tokens are
// whitespace-split; shell quoting is not interpreted.
type CommandConfig struct {
	Default string `yaml:"default"`
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
//
// TokenPreflight is an opt-in (default false) guard (ADR-0003): when on,
// a review whose estimated prompt tokens exceed the provider's context
// window prompts for confirmation (TTY) or aborts (non-TTY) before the
// paid round-trip, instead of letting the provider reject it with a raw
// 400. Off by default because the estimate is a chars/4 heuristic and a
// false positive shouldn't block a review nobody asked to guard.
type GuardConfig struct {
	SecretScan     bool `yaml:"secret_scan"`
	TokenPreflight bool `yaml:"token_preflight"`
}

type ProviderConfig struct {
	APIKey  string `yaml:"api_key,omitempty"`
	Model   string `yaml:"model,omitempty"`
	BaseURL string `yaml:"base_url,omitempty"`

	// Pricing overrides the built-in per-model rate table (OQ-09), keyed
	// by model name. Useful when the hard-coded snapshot drifts or for a
	// negotiated rate. Zero fields fall back to the built-in value, so a
	// partial override (e.g. only output_per_1m) is allowed. Consumed by
	// the cost preflight, verbose footer, and cached-cost figures via
	// resolvePricing (internal/cli).
	Pricing map[string]ModelPricing `yaml:"pricing,omitempty"`
}

// ModelPricing is a per-1M-token rate override for one model.
type ModelPricing struct {
	InputPer1M       float64 `yaml:"input_per_1m,omitempty"`
	OutputPer1M      float64 `yaml:"output_per_1m,omitempty"`
	CachedInputPer1M float64 `yaml:"cached_input_per_1m,omitempty"`
}

type OutputConfig struct {
	Lang   string `yaml:"lang"`
	Stream bool   `yaml:"stream"`
	Color  string `yaml:"color"`
}

// CacheConfig controls the local response cache (ADR-0008). MaxSizeMB
// bounds the on-disk cache: after each write, if the cache directory
// exceeds this many mebibytes the oldest entries are evicted oldest-first
// until it fits (the just-written entry is never evicted). Zero — the
// default — disables eviction; `cache prune` stays the manual stand-in.
// A new key rather than the v0.9.1-removed `max_size_mb` revival: this
// one is actually read on the Put path.
type CacheConfig struct {
	Enabled   bool `yaml:"enabled"`
	TTLDays   int  `yaml:"ttl_days"`
	MaxSizeMB int  `yaml:"max_size_mb"`
}
