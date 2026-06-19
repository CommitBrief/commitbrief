// SPDX-License-Identifier: GPL-3.0-or-later

package diff

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"

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
	out.addedLines, out.deletedLines = countLineKinds(out.Files)
	return out
}

func shouldExclude(f FileDiff, m *ignore.Matcher) bool {
	// PathParts / OldPathParts are pre-split at parse time so this
	// hot path (every file × every filter layer) avoids re-splitting.
	if len(f.PathParts) > 0 && m.MatchParts(f.PathParts) {
		return true
	}
	if len(f.OldPathParts) > 0 && m.MatchParts(f.OldPathParts) {
		return true
	}
	return false
}

// KeepPaths narrows a Diff to only files matching the supplied
// allowlists. The inputs are split into a fast literal path and a glob
// path so existing literal usage keeps its O(1) / prefix behavior
// byte-for-byte (the backward-compatibility guarantee, ADR-0026).
//
//   - Literal files (no glob metacharacter) → exact-path allowlist
//     (post-change Path or OldPath equality, slash-normalized).
//   - Literal dirs (no glob metacharacter) → prefix allowlist; a file
//     is kept when its path starts with `<dir>/` for any entry
//     (trailing slash on the input is stripped, then re-added by the
//     matcher so prefixes stay strict on path-segment boundaries).
//   - Glob entries (file ∪ dir containing `*`, `?`, or `[`) → compiled
//     gitignore patterns. A slash-less pattern matches the basename at
//     any depth (`*.go` → `internal/x.go`); a slash-bearing pattern is
//     root-anchored (`internal/**/*.ts`). Reuses go-git's gitignore
//     matcher (already a dependency via internal/ignore) → zero new deps.
//
// Both empty → no filtering (returns d unchanged). When at least one
// input is non-empty, union semantics apply: a file is kept if it
// matches ANY literal file OR sits under ANY literal dir OR matches ANY
// glob.
//
// An invalid glob pattern returns a non-nil error rather than silently
// mis-filtering (e.g. dropping every file). Paths are compared after
// `filepath.ToSlash` normalization on both sides so a user passing
// `app\Models` on Windows still matches `app/Models/User.go`.
func KeepPaths(d Diff, files, dirs []string) (Diff, error) {
	if len(files) == 0 && len(dirs) == 0 {
		return d, nil
	}

	var (
		literalFiles = make(map[string]struct{}, len(files))
		literalDirs  = make([]string, 0, len(dirs))
		globSources  = make([]string, 0, len(files)+len(dirs))
	)

	for _, f := range files {
		trimmed := strings.TrimSpace(f)
		if trimmed == "" {
			continue
		}
		norm := toSlashPattern(trimmed)
		if hasGlobMeta(norm) {
			globSources = append(globSources, norm)
			continue
		}
		literalFiles[norm] = struct{}{}
	}
	for _, dir := range dirs {
		trimmed := strings.TrimSpace(dir)
		if trimmed == "" {
			continue
		}
		norm := toSlashPattern(trimmed)
		if hasGlobMeta(norm) {
			globSources = append(globSources, norm)
			continue
		}
		clean := strings.TrimRight(norm, "/")
		if clean == "" {
			continue
		}
		literalDirs = append(literalDirs, clean+"/")
	}

	globs, err := compileGlobs(globSources)
	if err != nil {
		return Diff{}, err
	}

	out := Diff{Origin: d.Origin, Args: d.Args}
	for _, f := range d.Files {
		if matchesPathAllowlist(f, literalFiles, literalDirs) || matchesAnyGlob(f, globs) {
			out.Files = append(out.Files, f)
		}
	}
	out.addedLines, out.deletedLines = countLineKinds(out.Files)
	return out, nil
}

// toSlashPattern normalizes a user-supplied --file/--dir pattern to
// POSIX `/` separators. filepath.ToSlash is a no-op on non-Windows
// hosts, so we also replace `\` explicitly: a Windows user's
// `app\Models\*` must filter the same slash-normalized diff paths
// regardless of which OS the binary runs on (diff paths are always
// `/`-based internally). This is safe because CommitBrief never treats
// `\` as a gitignore escape in path-allowlist patterns — paths are
// POSIX and `\` only ever appears as a Windows separator here.
func toSlashPattern(s string) string {
	return strings.ReplaceAll(filepath.ToSlash(s), `\`, "/")
}

// hasGlobMeta reports whether s contains a gitignore glob metacharacter.
// Inputs without one stay on the literal fast path (backward compat).
func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// compileGlobs parses each (slash-normalized, trimmed) pattern into a
// gitignore.Pattern. gitignore.ParsePattern itself does not surface a
// parse error, so invalid input is validated minimally here (empty
// after trim, or an unbalanced `[` character class) and returned as an
// error — we never silently compile a pattern that would mis-filter.
func compileGlobs(patterns []string) ([]gitignore.Pattern, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	out := make([]gitignore.Pattern, 0, len(patterns))
	for _, pat := range patterns {
		if err := validateGlob(pat); err != nil {
			return nil, fmt.Errorf("%q: %w", pat, err)
		}
		out = append(out, gitignore.ParsePattern(pat, nil))
	}
	return out, nil
}

// validateGlob rejects patterns gitignore.ParsePattern would accept but
// that cannot meaningfully match — primarily an unterminated `[` class.
// gitignore patterns rarely fail to parse, so this is a minimal guard
// against the cases that would otherwise mis-filter silently.
func validateGlob(pat string) error {
	if strings.TrimSpace(pat) == "" {
		return fmt.Errorf("empty pattern")
	}
	depth := 0
	for i := 0; i < len(pat); i++ {
		switch pat[i] {
		case '\\':
			i++ // skip the escaped character
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		}
	}
	if depth != 0 {
		return fmt.Errorf("unterminated character class '['")
	}
	return nil
}

func matchesPathAllowlist(f FileDiff, files map[string]struct{}, dirPrefixes []string) bool {
	if len(files) == 0 && len(dirPrefixes) == 0 {
		return false
	}
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

// matchesAnyGlob reports whether the file's path (or pre-rename OldPath)
// matches any compiled glob. It feeds gitignore.Pattern.Match the
// pre-split path parts (PathParts/OldPathParts, populated at parse time;
// derived on the fly for hand-built FileDiff values), with isDir=false
// since a diff entry is always a file. A gitignore Exclude result means
// the pattern matched → the file is kept (allowlist inversion).
func matchesAnyGlob(f FileDiff, globs []gitignore.Pattern) bool {
	if len(globs) == 0 {
		return false
	}
	for _, parts := range [][]string{pathPartsOf(f, false), pathPartsOf(f, true)} {
		if len(parts) == 0 {
			continue
		}
		for _, g := range globs {
			if g.Match(parts, false) == gitignore.Exclude {
				return true
			}
		}
	}
	return false
}

// pathPartsOf returns the slash-split parts of the file's path. It
// prefers the parse-time cache (PathParts/OldPathParts) and falls back
// to splitting Path/OldPath so hand-constructed FileDiff values (tests,
// and any caller that doesn't go through diff.Parse) still match.
func pathPartsOf(f FileDiff, old bool) []string {
	if old {
		if len(f.OldPathParts) > 0 {
			return f.OldPathParts
		}
		if f.OldPath == "" {
			return nil
		}
		return strings.Split(filepath.ToSlash(f.OldPath), "/")
	}
	if len(f.PathParts) > 0 {
		return f.PathParts
	}
	if f.Path == "" {
		return nil
	}
	return strings.Split(filepath.ToSlash(f.Path), "/")
}
