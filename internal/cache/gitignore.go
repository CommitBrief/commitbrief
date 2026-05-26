package cache

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	gitignoreEntry  = ".commitbrief/"
	gitignoreHeader = "# CommitBrief local config and cache"
)

// EnsureGitignore appends `.commitbrief/` to the repo's .gitignore if not
// already present. Idempotent: a file that already contains the entry is
// left untouched. Returns true if the file was modified.
func EnsureGitignore(repoRoot string) (bool, error) {
	if repoRoot == "" {
		return false, nil
	}
	path := filepath.Join(repoRoot, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			content := gitignoreHeader + "\n" + gitignoreEntry + "\n"
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return false, fmt.Errorf("cache: write .gitignore: %w", err)
			}
			return true, nil
		}
		return false, fmt.Errorf("cache: read .gitignore: %w", err)
	}

	if hasEntry(data, gitignoreEntry) {
		return false, nil
	}

	var sb strings.Builder
	sb.Write(data)
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		sb.WriteString("\n")
	}
	if len(data) > 0 {
		sb.WriteString("\n")
	}
	sb.WriteString(gitignoreHeader)
	sb.WriteString("\n")
	sb.WriteString(gitignoreEntry)
	sb.WriteString("\n")

	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return false, fmt.Errorf("cache: append .gitignore: %w", err)
	}
	return true, nil
}

func hasEntry(data []byte, entry string) bool {
	s := bufio.NewScanner(strings.NewReader(string(data)))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if line == entry || line == strings.TrimSuffix(entry, "/") {
			return true
		}
	}
	return false
}
