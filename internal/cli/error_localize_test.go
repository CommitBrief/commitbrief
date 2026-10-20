// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/git"
)

func TestLocalizeGitError(t *testing.T) {
	prev := global.lang
	t.Cleanup(func() { global.lang = prev })

	cases := []struct {
		name string
		lang string
		err  error
		want string
	}{
		{"tr git missing", "tr", git.ErrNoGitCLI, "git bulunamadı"},
		{"tr git missing wrapped", "tr", fmt.Errorf("diff: %w", git.ErrNoGitCLI), "git bulunamadı"},
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
