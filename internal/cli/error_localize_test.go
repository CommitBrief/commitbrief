// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
)

func TestLocalizeGitError(t *testing.T) {
	// pickErrorCatalog reads the repo and user config for the language, so a
	// developer's real ~/.commitbrief/config.yml (or this checkout's own
	// .commitbrief/) must not leak into the "en" cases.
	isolateHomeEnv(t)
	chdirIsolated(t)
	prev := global.lang
	t.Cleanup(func() { global.lang = prev })

	cases := []struct {
		name string
		lang string
		err  error
		want string
	}{
		{"tr git missing", "tr", git.ErrNoGitCLI, "git PATH'te bulunamadı"},
		{"tr git missing wrapped", "tr", fmt.Errorf("diff: %w", git.ErrNoGitCLI), "git PATH'te bulunamadı"},
		{"tr not a repo", "tr", git.ErrNotARepo, "git deposunun içinde değilsiniz"},
		{"en git missing", "en", git.ErrNoGitCLI, "git not found on PATH"},
		{"en not a repo", "", git.ErrNotARepo, "Not inside a git repository"},
		{"other error unchanged", "tr", errors.New("boom"), "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			global.lang = tc.lang
			got := localizeGitError(pickErrorCatalog(), tc.err)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("localizeGitError = %q, want substring %q", got, tc.want)
			}
		})
	}
}

// pickErrorCatalog follows the same language chain as resolveContext:
// --lang > repo config > user config > English, a bad source falling through
// to the next, and it never fails on its own.
func TestErrorLocalizeCatalogPriority(t *testing.T) {
	const (
		trPrefix = "Hata:"
		enPrefix = "Error:"
	)

	type setup struct {
		name     string
		flag     string
		repoCfg  *string // nil: no repo config file
		userCfg  *string // nil: no user config file
		wantPref string
	}
	str := func(s string) *string { return &s }
	cases := []setup{
		{"default is English", "", nil, nil, enPrefix},
		{"user config", "", nil, str("output:\n  lang: tr\n"), trPrefix},
		{"repo config", "", str("output:\n  lang: tr\n"), nil, trPrefix},
		{"repo beats user", "", str("output:\n  lang: en\n"), str("output:\n  lang: tr\n"), enPrefix},
		{"flag beats repo", "en", str("output:\n  lang: tr\n"), nil, enPrefix},
		{"flag beats user", "tr", nil, str("output:\n  lang: en\n"), trPrefix},
		{"malformed repo falls to user", "", str("output: [unclosed\n"), str("output:\n  lang: tr\n"), trPrefix},
		{"unrecognized repo value falls to user", "", str("output:\n  lang: zz-nope\n"), str("output:\n  lang: tr\n"), trPrefix},
		{"unrecognized flag falls to repo", "zz-nope", str("output:\n  lang: tr\n"), nil, trPrefix},
		{"unknown key does not lose the language", "", str("output:\n  lang: tr\nnot_a_key: 1\n"), nil, trPrefix},
		{"language without a UI catalog is English", "", str("output:\n  lang: fr\n"), nil, enPrefix},
	}

	prev := global.lang
	t.Cleanup(func() { global.lang = prev })

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A git repo marker in a fresh dir so FindRepo resolves it as the
			// repo root, plus a user config path nowhere near the real one.
			repo := t.TempDir()
			if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(repo)
			userPath := filepath.Join(t.TempDir(), "config.yml")
			t.Setenv("COMMITBRIEF_CONFIG", userPath)

			if tc.repoCfg != nil {
				if err := os.MkdirAll(filepath.Join(repo, ".commitbrief"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, ".commitbrief", "config.yml"), []byte(*tc.repoCfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if tc.userCfg != nil {
				if err := os.WriteFile(userPath, []byte(*tc.userCfg), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			global.lang = tc.flag

			if got := pickErrorCatalog().T("common.error_prefix"); got != tc.wantPref {
				t.Fatalf("common.error_prefix = %q, want %q", got, tc.wantPref)
			}
		})
	}
}

// An unreadable config location must not turn into a second error.
func TestErrorLocalizeCatalogSurvivesUnreadableConfig(t *testing.T) {
	prev := global.lang
	t.Cleanup(func() { global.lang = prev })
	global.lang = ""

	t.Chdir(t.TempDir()) // not a repo
	// A directory where the config file should be: ReadFile fails with a
	// non-NotExist error.
	t.Setenv("COMMITBRIEF_CONFIG", t.TempDir())

	cat := pickErrorCatalog()
	if cat == nil {
		t.Fatal("pickErrorCatalog must always return a catalog")
	}
	if got := cat.T("common.error_prefix"); got != "Error:" {
		t.Fatalf("common.error_prefix = %q, want the English fallback", got)
	}
}
