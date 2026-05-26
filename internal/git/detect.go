package git

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

func FindRepo(start string) (string, error) {
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("git: cwd: %w", err)
		}
	}
	abs, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("git: abs(%q): %w", start, err)
	}
	dir := abs
	for {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		switch {
		case err == nil:
			if info.IsDir() {
				return dir, nil
			}
			// `.git` can be a file inside submodules / worktrees; treat as a repo marker.
			return dir, nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", fmt.Errorf("git: stat %s: %w", gitPath, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNotARepo
		}
		dir = parent
	}
}
