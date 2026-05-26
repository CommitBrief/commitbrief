package rules

import (
	_ "embed"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const Filename = "COMMITBRIEF.md"

//go:embed default.md
var defaultContent string

type Source int

const (
	SourceDefault Source = iota
	SourceFile
)

func (s Source) String() string {
	switch s {
	case SourceFile:
		return "file"
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

func Default() Loaded {
	return Loaded{
		Content: defaultContent,
		Source:  SourceDefault,
		Hash:    hashOf([]byte(defaultContent)),
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
