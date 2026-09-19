// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// LoadOptions tunes a load. The zero value is the strict default, so every
// existing caller keeps the behavior it had.
type LoadOptions struct {
	// IgnoreUnknownKeys downgrades an unknown key from a hard failure to a
	// reported finding: the load proceeds and the offenders come back as
	// []UnknownKey for the caller to warn about. It exists for `commitbrief
	// --ignore-unknown-config` — a config that loaded yesterday must not
	// become unusable today — and deliberately does NOT restore the old
	// silence: a caller that drops the returned findings on the floor is
	// reintroducing the exact bug strict validation removed.
	//
	// Note what "ignoring" means: the key stays in the merged map and is
	// dropped by the final typed decode, so a typo'd key simply has no
	// effect. That is precisely why the warning is mandatory — the user
	// believes the setting is in force and it is not.
	IgnoreUnknownKeys bool
}

func Load(globalPath, repoPath string) (*Config, error) {
	cfg, _, err := LoadWith(globalPath, repoPath, LoadOptions{})
	return cfg, err
}

// LoadWith is Load with explicit options. It additionally returns every
// unknown key it walked past; with the strict (zero) options that slice is
// always empty, because the first offender is an error instead.
func LoadWith(globalPath, repoPath string, opts LoadOptions) (*Config, []UnknownKey, error) {
	var unknown []UnknownKey

	merged, err := marshalToMap(Default())
	if err != nil {
		return nil, nil, fmt.Errorf("config: encode defaults: %w", err)
	}

	for _, p := range []struct {
		label string
		path  string
	}{
		{"global", globalPath},
		{"repo", repoPath},
	} {
		if p.path == "" {
			continue
		}
		layer, found, err := readLayer(p.path, opts)
		unknown = append(unknown, found...)
		if err != nil {
			// An unknown-key error already names the exact file and key;
			// re-wrapping it would print "config:" twice and bury the path.
			var uk UnknownKey
			if errors.As(err, &uk) {
				return nil, unknown, err
			}
			return nil, unknown, fmt.Errorf("config: %s (%s): %w", p.label, p.path, err)
		}
		if layer != nil {
			deepMerge(merged, layer)
		}
	}

	out, err := unmarshalFromMap(merged)
	if err != nil {
		return nil, unknown, fmt.Errorf("config: decode merged: %w", err)
	}
	if err := Migrate(out); err != nil {
		return nil, unknown, err
	}
	return out, unknown, nil
}

func LoadFile(path string) (*Config, error) {
	cfg, _, err := LoadFileWith(path, LoadOptions{})
	return cfg, err
}

// LoadFileWith is LoadFile with explicit options; see LoadOptions.
func LoadFileWith(path string, opts LoadOptions) (*Config, []UnknownKey, error) {
	if path == "" {
		return nil, nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	// LoadFile backs `config set`, `providers use` and lang.Resolve. It has
	// to be as strict as Load, or `config set` would happily write into a
	// file the next Load refuses.
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	found := ValidateKeys(raw, path)
	if !opts.IgnoreUnknownKeys {
		if err := unknownKeyError(found); err != nil {
			return nil, nil, err
		}
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, found, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, found, nil
}

func readLayer(path string, opts LoadOptions) (map[string]any, []UnknownKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("read: %w", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, nil, fmt.Errorf("parse: %w", err)
	}
	// Validate per layer, never on the merged map: after the merge the data
	// has been re-marshalled, so nothing can say which file was wrong.
	found := ValidateKeys(m, path)
	if !opts.IgnoreUnknownKeys {
		if err := unknownKeyError(found); err != nil {
			return nil, nil, err
		}
	}
	return m, found, nil
}

func marshalToMap(c *Config) (map[string]any, error) {
	b, err := yaml.Marshal(c)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func unmarshalFromMap(m map[string]any) (*Config, error) {
	b, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	out := &Config{}
	if err := yaml.Unmarshal(b, out); err != nil {
		return nil, err
	}
	return out, nil
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if existing, ok := dst[k]; ok {
			if dstMap, dstIsMap := existing.(map[string]any); dstIsMap {
				if srcMap, srcIsMap := v.(map[string]any); srcIsMap {
					deepMerge(dstMap, srcMap)
					continue
				}
			}
		}
		dst[k] = v
	}
}
