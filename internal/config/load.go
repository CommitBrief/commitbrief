// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

func Load(globalPath, repoPath string) (*Config, error) {
	merged, err := marshalToMap(Default())
	if err != nil {
		return nil, fmt.Errorf("config: encode defaults: %w", err)
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
		layer, err := readLayer(p.path)
		if err != nil {
			return nil, fmt.Errorf("config: %s (%s): %w", p.label, p.path, err)
		}
		if layer != nil {
			deepMerge(merged, layer)
		}
	}

	out, err := unmarshalFromMap(merged)
	if err != nil {
		return nil, fmt.Errorf("config: decode merged: %w", err)
	}
	if err := Migrate(out); err != nil {
		return nil, err
	}
	return out, nil
}

func LoadFile(path string) (*Config, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return &c, nil
}

func readLayer(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read: %w", err)
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	return m, nil
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
