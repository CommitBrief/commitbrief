// SPDX-License-Identifier: GPL-3.0-or-later

package setup

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/config"
)

const (
	repoConfigSubdir = ".commitbrief"
	configFilename   = "config.yml"
	globalConfigDir  = ".commitbrief"
)

// GlobalConfigPath returns the canonical user-level config path.
func GlobalConfigPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("setup: home dir: %w", err)
	}
	return filepath.Join(home, globalConfigDir, configFilename), nil
}

// RepoConfigPath returns the canonical repo-level config path.
func RepoConfigPath(repoRoot string) string {
	return filepath.Join(repoRoot, repoConfigSubdir, configFilename)
}

// WriteConfig serializes cfg to YAML at path. Parent directory is created
// with 0700 (config may contain API keys). Write is atomic: temp + rename.
func WriteConfig(path string, cfg *config.Config) error {
	if path == "" {
		return fmt.Errorf("setup: WriteConfig: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("setup: mkdir %s: %w", filepath.Dir(path), err)
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("setup: marshal: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("setup: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("setup: rename: %w", err)
	}
	return nil
}

// WriteRepoConfig saves cfg under <repoRoot>/.commitbrief/config.yml and
// makes sure the repo's .gitignore excludes the .commitbrief/ directory.
// Returns whether .gitignore was modified so callers can surface a notice.
func WriteRepoConfig(repoRoot string, cfg *config.Config) (gitignoreUpdated bool, err error) {
	if repoRoot == "" {
		return false, fmt.Errorf("setup: WriteRepoConfig: empty repoRoot")
	}
	if err := WriteConfig(RepoConfigPath(repoRoot), cfg); err != nil {
		return false, err
	}
	updated, err := cache.EnsureGitignore(repoRoot)
	if err != nil {
		return false, fmt.Errorf("setup: %w", err)
	}
	return updated, nil
}
