// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"path/filepath"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/ignore"
)

func Filter(d Diff, m *ignore.Matcher) Diff {
	if m == nil || m.Len() == 0 {
		return d
	}
	out := Diff{Origin: d.Origin, Args: d.Args}
	for _, f := range d.Files {
		if shouldExclude(f, m) {
			continue
		}
		out.Files = append(out.Files, f)
	}
	return out
}

func shouldExclude(f FileDiff, m *ignore.Matcher) bool {
	if f.Path != "" && m.Match(f.Path) {
		return true
	}
	// For renames/deletes the post-change path may be empty; fall back to OldPath.
	if f.OldPath != "" && m.Match(f.OldPath) {
		return true
	}
	return false
}

// KeepPaths narrows a Diff to only files matching the supplied
// allowlists. files is an exact-path allowlist (post-change Path or
// OldPath equality, slash-normalized). dirs is a prefix allowlist —
// a file is kept when its path starts with `<dir>/` for any entry
// (trailing slash on the input is stripped, then added by the matcher).
//
// Both empty → no filtering (returns d unchanged). When at least one
// is non-empty, the union semantics apply: a file is kept if it
// matches ANY file OR sits under ANY dir.
//
// Paths are compared after `filepath.ToSlash` normalization on both
// sides so a user passing `app\Models` on Windows still matches
// `app/Models/User.go` in the parsed diff.
func KeepPaths(d Diff, files, dirs []string) Diff {
	if len(files) == 0 && len(dirs) == 0 {
		return d
	}
	wantFiles := make(map[string]struct{}, len(files))
	for _, f := range files {
		wantFiles[filepath.ToSlash(strings.TrimSpace(f))] = struct{}{}
	}
	wantDirs := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		clean := strings.TrimRight(filepath.ToSlash(strings.TrimSpace(dir)), "/")
		if clean == "" {
			continue
		}
		wantDirs = append(wantDirs, clean+"/")
	}

	out := Diff{Origin: d.Origin, Args: d.Args}
	for _, f := range d.Files {
		if matchesPathAllowlist(f, wantFiles, wantDirs) {
			out.Files = append(out.Files, f)
		}
	}
	return out
}

func matchesPathAllowlist(f FileDiff, files map[string]struct{}, dirPrefixes []string) bool {
	candidates := []string{filepath.ToSlash(f.Path)}
	if f.OldPath != "" {
		candidates = append(candidates, filepath.ToSlash(f.OldPath))
	}
	for _, p := range candidates {
		if p == "" {
			continue
		}
		if _, ok := files[p]; ok {
			return true
		}
		for _, prefix := range dirPrefixes {
			if strings.HasPrefix(p, prefix) {
				return true
			}
		}
	}
	return false
}
