// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"fmt"
	"os"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/lang"
)

// appContext bundles the resolved environment the CLI commands operate
// against. Built once per invocation by resolveContext.
type appContext struct {
	RepoRoot   string
	Repo       *git.DispatchRepo
	Config     *config.Config
	RawRepoCfg *config.Config // pre-merge, for lang source attribution
	RawGlobal  *config.Config
	Lang       lang.Resolution
	Catalog    *i18n.Catalog
}

func resolveContext(requireRepo bool) (*appContext, error) {
	repoRoot := ""
	var repo *git.DispatchRepo
	if requireRepo {
		root, err := git.FindRepo("")
		if err != nil {
			return nil, fmt.Errorf("%w", err)
		}
		repoRoot = root
		repo, err = git.Open(repoRoot, git.DispatchOptions{})
		if err != nil {
			return nil, err
		}
	} else {
		// Best-effort detection so non-review commands (setup, list) still
		// know the repo root when present.
		if root, err := git.FindRepo(""); err == nil {
			repoRoot = root
			repo, _ = git.Open(repoRoot, git.DispatchOptions{})
		}
	}

	globalPath := os.Getenv("COMMITBRIEF_CONFIG")
	if globalPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			globalPath = home + "/.commitbrief/config.yml"
		}
	}
	repoPath := ""
	if repoRoot != "" {
		repoPath = repoRoot + "/.commitbrief/config.yml"
	}

	cfg, err := config.Load(globalPath, repoPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	config.ApplyEnv(cfg)

	// Apply CLI overrides. --cli <name> is a shorthand that resolves
	// to the "<name>-cli" provider (claude → claude-cli, gemini →
	// gemini-cli). cobra has already enforced mutual exclusion with
	// --provider so at most one of the two is set.
	if global.cli != "" {
		cfg.Provider = global.cli + "-cli"
	}
	if global.provider != "" {
		cfg.Provider = global.provider
	}
	if global.model != "" {
		pc := cfg.Providers[cfg.Provider]
		pc.Model = global.model
		cfg.Providers[cfg.Provider] = pc
	}
	if global.lang != "" {
		cfg.Output.Lang = global.lang
	}

	rawGlobal, _ := config.LoadFile(globalPath)
	rawRepo, _ := config.LoadFile(repoPath)
	langRes := lang.Resolve(rawRepo, rawGlobal, lang.Env{LANG: os.Getenv("LANG")})
	if global.lang != "" {
		// UC-09: --lang goes through the same supported() coercion the
		// config-file path uses, so passing --lang=de on the CLI lands
		// at "en" rather than blowing up i18n.Load below.
		langRes = lang.CoerceCLIFlag(global.lang)
	}

	cat, err := i18n.Load(langRes.Code)
	if err != nil {
		cat, _ = i18n.Load(i18n.DefaultLang)
	}

	return &appContext{
		RepoRoot:   repoRoot,
		Repo:       repo,
		Config:     cfg,
		RawRepoCfg: rawRepo,
		RawGlobal:  rawGlobal,
		Lang:       langRes,
		Catalog:    cat,
	}, nil
}

func infof(format string, args ...any) {
	if global.quiet {
		return
	}
	fmt.Fprintf(os.Stderr, format+"\n", args...)
}

// userHome returns the current user's home dir or "" if it cannot be
// resolved. Used by callers that want to honor ~/.commitbrief/... layers
// without erroring when the lookup fails (e.g. detached environments).
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}
