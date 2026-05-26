package ignore

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

const Filename = ".commitbriefignore"

func Parse(r io.Reader) (*Matcher, error) {
	var lines []string
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 64*1024), 1024*1024)
	for s.Scan() {
		lines = append(lines, s.Text())
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("ignore: read: %w", err)
	}
	return parsePatterns(lines), nil
}

func ParseFile(path string) (*Matcher, error) {
	if path == "" {
		return Empty(), nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Empty(), nil
		}
		return nil, fmt.Errorf("ignore: open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	return Parse(f)
}

func parsePatterns(lines []string) *Matcher {
	patterns := make([]gitignore.Pattern, 0, len(lines))
	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(trimmed, nil))
	}
	return newMatcher(patterns)
}
