// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !windows

package alias

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// rcPerm is the mode for a shell startup file. These are not secret (unlike
// config.yml with its API keys), so 0644 matches what a user-created
// ~/.bashrc carries.
const rcPerm = os.FileMode(0o644)

// All returns the shells we can install an alias for on this Unix-like OS:
// bash, zsh and fish.
func All() []Installer {
	return []Installer{
		newPosixInstaller("bash", "Bash", bashRC()),
		newPosixInstaller("zsh", "Zsh", zshRC()),
		newFishInstaller(fishConfig()),
	}
}

// Detect picks the installer matching the login shell ($SHELL). Returns
// false when $SHELL is unset or names a shell we do not support, so the
// caller can fall back to an interactive picker.
func Detect() (Installer, bool) {
	switch filepath.Base(os.Getenv("SHELL")) {
	case "bash":
		return ByName("bash")
	case "zsh":
		return ByName("zsh")
	case "fish":
		return ByName("fish")
	}
	return nil, false
}

// newPosixInstaller builds the bash/zsh installer. Both use POSIX
// `alias <name>='commitbrief'` syntax and reload via `source <rc>`.
func newPosixInstaller(name, label, rc string) Installer {
	reload := ""
	if rc != "" {
		reload = "source " + rc
	}
	return fileInstaller{
		name:  name,
		label: fmt.Sprintf("%s (%s)", label, displayPath(rc)),
		path:  rc,
		perm:  rcPerm,
		render: func(alias string) string {
			return fmt.Sprintf("alias %s='%s'", alias, Command)
		},
		detectors: func(alias string) []*regexp.Regexp {
			q := regexp.QuoteMeta(alias)
			return []*regexp.Regexp{
				regexp.MustCompile(`(?m)^[ \t]*alias[ \t]+` + q + `[= \t]`),
			}
		},
		reload: reload,
	}
}

// newFishInstaller builds the fish installer. fish accepts the same
// `alias name='cmd'` form; we also flag a pre-existing `abbr` of the name.
func newFishInstaller(cfg string) Installer {
	reload := ""
	if cfg != "" {
		reload = "source " + cfg
	}
	return fileInstaller{
		name:  "fish",
		label: fmt.Sprintf("fish (%s)", displayPath(cfg)),
		path:  cfg,
		perm:  rcPerm,
		render: func(alias string) string {
			return fmt.Sprintf("alias %s='%s'", alias, Command)
		},
		detectors: func(alias string) []*regexp.Regexp {
			q := regexp.QuoteMeta(alias)
			return []*regexp.Regexp{
				regexp.MustCompile(`(?m)^[ \t]*alias[ \t]+(?:--save[ \t]+)?` + q + `[= \t]`),
				// Anchor the name to the abbreviation KEY (the first non-flag
				// token after `abbr` + optional flags), not anywhere on the
				// line — otherwise `abbr gco 'git checkout cbr'` would falsely
				// report `cbr` as taken.
				regexp.MustCompile(`(?m)^[ \t]*abbr\b(?:[ \t]+-{1,2}[A-Za-z][\w-]*)*[ \t]+` + q + `\b`),
			}
		},
		reload: reload,
	}
}

func bashRC() string {
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".bashrc")
	}
	return ""
}

func zshRC() string {
	if zdot := os.Getenv("ZDOTDIR"); zdot != "" {
		return filepath.Join(zdot, ".zshrc")
	}
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".zshrc")
	}
	return ""
}

func fishConfig() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "fish", "config.fish")
	}
	if h := homeDir(); h != "" {
		return filepath.Join(h, ".config", "fish", "config.fish")
	}
	return ""
}
