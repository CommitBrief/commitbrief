// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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
	// Timeout is the resolved --timeout / review.timeout value; zero means
	// "no deadline, keep every built-in provider cap" (the historical
	// behavior). Consumed by appContext.withTimeout and
	// newProviderWithTimeout.
	Timeout time.Duration
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

	globalPath, repoPath := configFilePaths(repoRoot)

	loadOpts := config.LoadOptions{IgnoreUnknownKeys: global.ignoreUnknownConfig}

	cfg, unknownKeys, err := config.LoadWith(globalPath, repoPath, loadOpts)
	if err != nil {
		// An unknown-key error already opens with "config:" and names the
		// offending file, so re-wrapping it here printed "config: config:".
		var uk config.UnknownKey
		if errors.As(err, &uk) {
			return nil, err
		}
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

	// Timeout resolution is deliberately here, not at the call site: every
	// command that can spend real time reads app.Timeout, and resolving it
	// once means a malformed value fails the run before the diff is read,
	// let alone sent to a provider.
	timeout, err := resolveTimeout(global.timeout, cfg)
	if err != nil {
		return nil, err
	}

	// Language resolution (ADR-0021) is independent of the merged config: it
	// reads the raw per-file configs so each level (--lang flag → repo → user
	// → English) is judged on its own value, with invalid/empty values falling
	// through. langRes.Code is the AI *output* language (any recognized
	// language, e.g. "fr"); the CLI's own interface strings load from
	// langRes.UICatalog(), which degrades to English for any language we don't
	// ship a catalog for. The flag is NOT folded into cfg.Output.Lang, so
	// `config get output.lang` still reports the stored file value.
	// Same options as the merged load above: without them a file with an
	// unknown key would come back nil here even under
	// --ignore-unknown-config, silently dropping that file's output.lang.
	rawGlobal, _, _ := config.LoadFileWith(globalPath, loadOpts)
	rawRepo, _, _ := config.LoadFileWith(repoPath, loadOpts)
	langRes := lang.Resolve(global.lang, rawRepo, rawGlobal)

	cat, err := i18n.Load(langRes.UICatalog())
	if err != nil {
		cat, _ = i18n.Load(i18n.DefaultLang)
	}

	// Warn only once the catalog exists, and to stderr — stdout carries the
	// --json payload CI parses. Deliberately NOT gated on --quiet: --quiet
	// suppresses *info* (progress) messages, while this reports that part of
	// the user's configuration is not in force. Muting it for the scripted
	// runs that pass --quiet is precisely how the silent failure got shipped
	// in the first place.
	warnUnknownConfigKeys(cat, unknownKeys)

	return &appContext{
		RepoRoot:   repoRoot,
		Repo:       repo,
		Config:     cfg,
		RawRepoCfg: rawRepo,
		RawGlobal:  rawGlobal,
		Lang:       langRes,
		Catalog:    cat,
		Timeout:    timeout,
	}, nil
}

// warnUnknownConfigKeys names every key --ignore-unknown-config let through.
// Each entry carries its own source file because the two config layers
// (~/.commitbrief/config.yml and <repo>/.commitbrief/config.yml) merge into
// one result — "which file do I edit?" is the user's next question.
//
// ValidateKeys sorts each layer's findings, so the printed order is stable
// across runs; an error message that reshuffles itself is not a usable one.
func warnUnknownConfigKeys(cat *i18n.Catalog, keys []config.UnknownKey) {
	if len(keys) == 0 || cat == nil {
		return
	}
	named := make([]string, 0, len(keys))
	for _, k := range keys {
		named = append(named, fmt.Sprintf("%q (%s)", k.Path, k.Source))
	}
	fmt.Fprintln(os.Stderr, cat.T("config.unknown_key_ignored", strings.Join(named, ", ")))
}

// refuseUnknownKeysWrite builds the error a write path (config set,
// providers use) returns instead of rewriting a file that was loaded with
// --ignore-unknown-config and still carries unknown keys.
//
// Why refuse rather than warn-and-write (Wave 0 review turu 2, item 6): both
// commands decode the file into a typed config.Config and then serialize the
// WHOLE struct back out. A key the schema doesn't know has nowhere to land
// in that struct, so it is silently dropped by the decode — writing the
// struct back out would permanently destroy the user's original value (a
// mistyped guard.secret_patterns[0].pattern came back as regex: ""), which
// is strictly worse than the pre-hatch behavior of erroring with the file
// untouched. Read paths (review, list, guard, diff, ...) never rewrite the
// file, so they keep using the lenient load without this check.
//
// setup is the one write path that does NOT call this: it rewrites the file
// from scratch by design (see newSetupCmd's RunE) and is itself the
// recovery route for a config broken enough to need the escape hatch, so
// refusing it would relock the exact user it exists to rescue.
func refuseUnknownKeysWrite(cat *i18n.Catalog, path string, found []config.UnknownKey) error {
	names := make([]string, 0, len(found))
	for _, k := range found {
		names = append(names, fmt.Sprintf("%q", k.Path))
	}
	return errors.New(cat.T("config.write_refused_unknown_keys", path, strings.Join(names, ", ")))
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

// configFilePaths resolves the global and repo config.yml paths used by
// config.Load. globalPath honors $COMMITBRIEF_CONFIG, else
// ~/.commitbrief/config.yml; repoPath is <repoRoot>/.commitbrief/config.yml
// when repoRoot is non-empty (else ""). Shared by resolveContext and the
// pre-parse default-command expansion in Execute so the two never drift.
func configFilePaths(repoRoot string) (globalPath, repoPath string) {
	globalPath = os.Getenv("COMMITBRIEF_CONFIG")
	if globalPath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			globalPath = home + "/.commitbrief/config.yml"
		}
	}
	if repoRoot != "" {
		repoPath = repoRoot + "/.commitbrief/config.yml"
	}
	return globalPath, repoPath
}
