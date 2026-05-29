// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/setup"
)

// newConfigCmd exposes the typed configuration surface so users don't have
// to hand-edit YAML for one-line changes. `show` dumps the merged config
// (API keys masked), `get <key>` reads a single field by dotted path,
// `set <key> <value>` writes one with type coercion and validation.
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show, get, or set individual configuration values",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigGetCmd())
	cmd.AddCommand(newConfigSetCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration (API keys masked)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(false)
			if err != nil {
				return err
			}
			data, err := yaml.Marshal(maskConfig(app.Config))
			if err != nil {
				return fmt.Errorf("config show: %w", err)
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
}

func newConfigGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print a single configuration value by dotted path",
		Long: `Print a single value from the merged configuration.

Examples:
  commitbrief config get provider
  commitbrief config get providers.anthropic.model
  commitbrief config get output.lang
  commitbrief config get cache.ttl_days`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(false)
			if err != nil {
				return err
			}
			val, err := configFieldGet(app.Config, args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), val)
			return err
		},
	}
}

func newConfigSetCmd() *cobra.Command {
	var local bool
	cmd := &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a single configuration value by dotted path",
		Long: `Write a single configuration value with type coercion and validation.

Examples:
  commitbrief config set provider openai
  commitbrief config set providers.anthropic.api_key sk-ant-xxxx
  commitbrief config set output.lang tr
  commitbrief config set cache.ttl_days 14

Booleans accept true/false, yes/no, 1/0, on/off.
By default writes to ~/.commitbrief/config.yml; --local writes to the repo.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			app, err := resolveContext(local)
			if err != nil {
				return err
			}
			path, err := targetPath(app.RepoRoot, local)
			if err != nil {
				return err
			}
			// Operate on the on-disk file directly so we don't accidentally
			// promote merged-in state from one scope into another. First-time
			// writes fall back to a Default skeleton.
			cfg, err := config.LoadFile(path)
			if err != nil {
				return err
			}
			if cfg == nil {
				cfg = config.Default()
			}
			if err := configFieldSet(cfg, args[0], args[1]); err != nil {
				return err
			}
			if err := setup.WriteConfig(path, cfg); err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), app.Catalog.T("config.set.success", args[0], path))
			return err
		},
	}
	cmd.Flags().BoolVar(&local, "local", false, "write to repo .commitbrief/config.yml instead of user-level")
	return cmd
}

// configFieldGet reads a value from cfg using dotted-path notation. The
// supported paths mirror the YAML structure exactly so users can paste a
// path from their config file and have it just work.
func configFieldGet(cfg *config.Config, path string) (string, error) {
	parts := strings.Split(path, ".")
	switch parts[0] {
	case "provider":
		if len(parts) != 1 {
			return "", fmt.Errorf("config: %q is not a sub-path", path)
		}
		return cfg.Provider, nil

	case "providers":
		if len(parts) != 3 {
			return "", fmt.Errorf("config: %q must be providers.<name>.<field>", path)
		}
		pc, ok := cfg.Providers[parts[1]]
		if !ok {
			return "", fmt.Errorf("config: unknown provider %q", parts[1])
		}
		switch parts[2] {
		case "api_key":
			return pc.APIKey, nil
		case "model":
			return pc.Model, nil
		case "base_url":
			return pc.BaseURL, nil
		default:
			return "", fmt.Errorf("config: unknown field %q in providers.%s (allowed: api_key, model, base_url)", parts[2], parts[1])
		}

	case "output":
		if len(parts) != 2 {
			return "", fmt.Errorf("config: %q must be output.<field>", path)
		}
		switch parts[1] {
		case "lang":
			return cfg.Output.Lang, nil
		case "stream":
			return strconv.FormatBool(cfg.Output.Stream), nil
		case "color":
			return cfg.Output.Color, nil
		default:
			return "", fmt.Errorf("config: unknown field %q in output (allowed: lang, stream, color)", parts[1])
		}

	case "cache":
		if len(parts) != 2 {
			return "", fmt.Errorf("config: %q must be cache.<field>", path)
		}
		switch parts[1] {
		case "enabled":
			return strconv.FormatBool(cfg.Cache.Enabled), nil
		case "ttl_days":
			return strconv.Itoa(cfg.Cache.TTLDays), nil
		case "max_size_mb":
			return strconv.Itoa(cfg.Cache.MaxSizeMB), nil
		default:
			return "", fmt.Errorf("config: unknown field %q in cache (allowed: enabled, ttl_days, max_size_mb)", parts[1])
		}

	case "guard":
		if len(parts) != 2 {
			return "", fmt.Errorf("config: %q must be guard.<field>", path)
		}
		switch parts[1] {
		case "secret_scan":
			return strconv.FormatBool(cfg.Guard.SecretScan), nil
		default:
			return "", fmt.Errorf("config: unknown field %q in guard (allowed: secret_scan)", parts[1])
		}

	case "cost":
		if len(parts) != 2 {
			return "", fmt.Errorf("config: %q must be cost.<field>", path)
		}
		switch parts[1] {
		case "warn_threshold_usd":
			return strconv.FormatFloat(cfg.Cost.WarnThresholdUSD, 'f', -1, 64), nil
		default:
			return "", fmt.Errorf("config: unknown field %q in cost (allowed: warn_threshold_usd)", parts[1])
		}

	case "command":
		if len(parts) != 2 {
			return "", fmt.Errorf("config: %q must be command.<field>", path)
		}
		switch parts[1] {
		case "default":
			return cfg.Command.Default, nil
		default:
			return "", fmt.Errorf("config: unknown field %q in command (allowed: default)", parts[1])
		}

	case "version":
		// Read-only via get; explicitly rejected by configFieldSet.
		return strconv.Itoa(cfg.Version), nil

	default:
		return "", fmt.Errorf("config: unknown top-level field %q (allowed: provider, providers.*, output.*, cache.*, guard.*, cost.*, command.*, version)", parts[0])
	}
}

// configFieldSet writes a value into cfg using dotted-path notation. The
// supported paths match configFieldGet; type coercion happens here and
// invalid values surface clear errors (so the YAML file never gets a
// half-typed mess).
func configFieldSet(cfg *config.Config, path, value string) error {
	parts := strings.Split(path, ".")
	switch parts[0] {
	case "provider":
		if len(parts) != 1 {
			return fmt.Errorf("config: %q is not a sub-path", path)
		}
		if !isRegistered(value) {
			return fmt.Errorf("config: unknown provider %q; known: %v", value, provider.Names())
		}
		cfg.Provider = value
		return nil

	case "providers":
		if len(parts) != 3 {
			return fmt.Errorf("config: %q must be providers.<name>.<field>", path)
		}
		if cfg.Providers == nil {
			cfg.Providers = map[string]config.ProviderConfig{}
		}
		pc := cfg.Providers[parts[1]]
		switch parts[2] {
		case "api_key":
			pc.APIKey = value
		case "model":
			pc.Model = value
		case "base_url":
			pc.BaseURL = value
		default:
			return fmt.Errorf("config: unknown field %q in providers.%s (allowed: api_key, model, base_url)", parts[2], parts[1])
		}
		cfg.Providers[parts[1]] = pc
		return nil

	case "output":
		if len(parts) != 2 {
			return fmt.Errorf("config: %q must be output.<field>", path)
		}
		switch parts[1] {
		case "lang":
			cfg.Output.Lang = value
		case "stream":
			b, err := parseConfigBool(value)
			if err != nil {
				return fmt.Errorf("config: output.stream: %w", err)
			}
			cfg.Output.Stream = b
		case "color":
			switch value {
			case "auto", "always", "never":
				cfg.Output.Color = value
			default:
				return fmt.Errorf("config: output.color must be auto/always/never; got %q", value)
			}
		default:
			return fmt.Errorf("config: unknown field %q in output (allowed: lang, stream, color)", parts[1])
		}
		return nil

	case "cache":
		if len(parts) != 2 {
			return fmt.Errorf("config: %q must be cache.<field>", path)
		}
		switch parts[1] {
		case "enabled":
			b, err := parseConfigBool(value)
			if err != nil {
				return fmt.Errorf("config: cache.enabled: %w", err)
			}
			cfg.Cache.Enabled = b
		case "ttl_days":
			i, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("config: cache.ttl_days must be an integer; got %q", value)
			}
			if i < 0 {
				return errors.New("config: cache.ttl_days cannot be negative")
			}
			cfg.Cache.TTLDays = i
		case "max_size_mb":
			i, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("config: cache.max_size_mb must be an integer; got %q", value)
			}
			if i < 0 {
				return errors.New("config: cache.max_size_mb cannot be negative")
			}
			cfg.Cache.MaxSizeMB = i
		default:
			return fmt.Errorf("config: unknown field %q in cache (allowed: enabled, ttl_days, max_size_mb)", parts[1])
		}
		return nil

	case "guard":
		if len(parts) != 2 {
			return fmt.Errorf("config: %q must be guard.<field>", path)
		}
		switch parts[1] {
		case "secret_scan":
			b, err := parseConfigBool(value)
			if err != nil {
				return fmt.Errorf("config: guard.secret_scan: %w", err)
			}
			cfg.Guard.SecretScan = b
		default:
			return fmt.Errorf("config: unknown field %q in guard (allowed: secret_scan)", parts[1])
		}
		return nil

	case "cost":
		if len(parts) != 2 {
			return fmt.Errorf("config: %q must be cost.<field>", path)
		}
		switch parts[1] {
		case "warn_threshold_usd":
			v, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return fmt.Errorf("config: cost.warn_threshold_usd must be a number; got %q", value)
			}
			if v < 0 {
				return errors.New("config: cost.warn_threshold_usd cannot be negative; use 0 to disable")
			}
			cfg.Cost.WarnThresholdUSD = v
		default:
			return fmt.Errorf("config: unknown field %q in cost (allowed: warn_threshold_usd)", parts[1])
		}
		return nil

	case "command":
		if len(parts) != 2 {
			return fmt.Errorf("config: %q must be command.<field>", path)
		}
		switch parts[1] {
		case "default":
			// Free-form argument string applied to a bare `commitbrief`.
			// Stored verbatim; tokenization happens at invocation time.
			cfg.Command.Default = value
		default:
			return fmt.Errorf("config: unknown field %q in command (allowed: default)", parts[1])
		}
		return nil

	case "version":
		return errors.New("config: version is managed by migrations and cannot be set manually")

	default:
		return fmt.Errorf("config: unknown top-level field %q (allowed: provider, providers.*, output.*, cache.*, guard.*, cost.*, command.*)", parts[0])
	}
}

func parseConfigBool(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1", "on":
		return true, nil
	case "false", "no", "0", "off":
		return false, nil
	default:
		return false, fmt.Errorf("expected true/false, got %q", s)
	}
}

// maskConfig clones cfg and replaces every non-empty APIKey with its
// masked form so `config show` never spills secrets to stdout or a
// redirected file. The original cfg is not mutated.
func maskConfig(cfg *config.Config) *config.Config {
	out := *cfg
	out.Providers = make(map[string]config.ProviderConfig, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		if pc.APIKey != "" {
			pc.APIKey = maskAPIKey(pc.APIKey)
		}
		out.Providers[name] = pc
	}
	return &out
}
