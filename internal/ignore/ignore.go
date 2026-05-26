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

func (m *Matcher) Len() int {
	if m == nil {
		return 0
	}
	return len(m.patterns)
}
