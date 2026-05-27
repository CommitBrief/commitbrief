// SPDX-License-Identifier: GPL-3.0-or-later

package rules

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	Filename       = "COMMITBRIEF.md"
	OutputFilename = "OUTPUT.md"
	LocalSubdir    = ".commitbrief"
)

//go:embed default.md
var defaultContent string

//go:embed output.md
var defaultOutputContent string

type Source int

const (
	SourceDefault Source = iota
	SourceFile
	SourceUserFile
)

func (s Source) String() string {
	switch s {
	case SourceFile:
		return "file"
	case SourceUserFile:
		return "user-file"
	case SourceDefault:
		return "default"
	default:
		return "unknown"
	}
}

type Loaded struct {
	Content string
	Source  Source
	Path    string
	Hash    string
}

func Load(repoRoot string) (Loaded, error) {
	if repoRoot != "" {
		path := filepath.Join(repoRoot, Filename)
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			return Loaded{
				Content: string(data),
				Source:  SourceFile,
				Path:    path,
				Hash:    hashOf(data),
			}, nil
		case errors.Is(err, fs.ErrNotExist):
			// fall through to embedded default
		default:
			return Loaded{}, fmt.Errorf("rules: read %s: %w", path, err)
		}
	}
	return Loaded{
		Content: defaultContent,
		Source:  SourceDefault,
		Hash:    hashOf([]byte(defaultContent)),
	}, nil
}

// LoadOutput resolves the output-format template through a three-tier
// fallback: repo-local (<repoRoot>/.commitbrief/OUTPUT.md) → user-level
// (<userHome>/.commitbrief/OUTPUT.md) → binary-embedded default. Both
// path segments are gitignored by `commitbrief setup --local`, so the
// override is per-user rather than team-shared — output convention is
// considered a personal preference; team-shared review content stays in
// COMMITBRIEF.md.
//
// Pass userHome == "" to skip the user-level layer (test injection); the
// CLI passes os.UserHomeDir() as resolved at startup.
func LoadOutput(repoRoot, userHome string) (Loaded, error) {
	if repoRoot != "" {
		path := filepath.Join(repoRoot, LocalSubdir, OutputFilename)
		if data, err := os.ReadFile(path); err == nil {
			return Loaded{
				Content: string(data),
				Source:  SourceFile,
				Path:    path,
				Hash:    hashOf(data),
			}, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Loaded{}, fmt.Errorf("rules: read %s: %w", path, err)
		}
	}
	if userHome != "" {
		path := filepath.Join(userHome, LocalSubdir, OutputFilename)
		if data, err := os.ReadFile(path); err == nil {
			return Loaded{
				Content: string(data),
				Source:  SourceUserFile,
				Path:    path,
				Hash:    hashOf(data),
			}, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return Loaded{}, fmt.Errorf("rules: read %s: %w", path, err)
		}
	}
	return DefaultOutput(), nil
}

func Default() Loaded {
	return Loaded{
		Content: defaultContent,
		Source:  SourceDefault,
		Hash:    hashOf([]byte(defaultContent)),
	}
}

func DefaultOutput() Loaded {
	return Loaded{
		Content: defaultOutputContent,
		Source:  SourceDefault,
		Hash:    hashOf([]byte(defaultOutputContent)),
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
