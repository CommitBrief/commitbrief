// SPDX-License-Identifier: GPL-3.0-or-later

package ignore

import (
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type Matcher struct {
	patterns []gitignore.Pattern
	backend  gitignore.Matcher
}

func newMatcher(patterns []gitignore.Pattern) *Matcher {
	return &Matcher{
		patterns: patterns,
		backend:  gitignore.NewMatcher(patterns),
	}
}

func Empty() *Matcher {
	return newMatcher(nil)
}

func (m *Matcher) Match(path string) bool {
	if m == nil || len(m.patterns) == 0 || path == "" {
		return false
	}
	p := filepath.ToSlash(path)
	p = strings.TrimPrefix(p, "./")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return false
	}
	return m.backend.Match(strings.Split(p, "/"), false)
}

// MatchParts is the same logical predicate as Match but accepts an
// already-split `[]string` path. Use this when the caller already
// has the parts (e.g. diff.Filter has FileDiff.PathParts populated
// at parse time) to skip the per-call strings.Split allocation.
func (m *Matcher) MatchParts(parts []string) bool {
	if m == nil || len(m.patterns) == 0 || len(parts) == 0 {
		return false
	}
	return m.backend.Match(parts, false)
}

func (m *Matcher) Len() int {
	if m == nil {
		return 0
	}
	return len(m.patterns)
}
