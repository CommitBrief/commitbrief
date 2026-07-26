// SPDX-License-Identifier: GPL-3.0-or-later

package leaks

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/diff"
)

// ScanWorktree reads every tracked file in the working tree and reports the
// credential-shaped lines in it.
//
// Enumeration is `git ls-files -z`, not filepath.WalkDir, for three reasons:
// it yields exactly the tracked set (an untracked, gitignored .env is where a
// secret is *supposed* to live, so flagging it would be noise); it respects
// .gitignore for free; and it sidesteps ignore.Matcher having no isDir=true
// entry point, which would make directory pruning during a walk subtly wrong.
//
// Unlike guard's diff scan this reads whole files — every line, not just added
// ones — because the question here is "what is in my tree right now", not
// "what am I about to send".
func ScanWorktree(ctx context.Context, repoRoot string, opts Options) (Result, error) {
	scan, err := newScanner(opts)
	if err != nil {
		return Result{}, err
	}
	paths, err := trackedFiles(ctx, repoRoot)
	if err != nil {
		return Result{}, err
	}

	limit := maxFileBytes(opts)
	var res Result

	for _, rel := range paths {
		if !keepPath(rel, opts) {
			continue
		}
		abs := filepath.Join(repoRoot, filepath.FromSlash(rel))

		info, statErr := os.Lstat(abs)
		if statErr != nil {
			// The index can list a file the tree no longer has (a deletion
			// staged elsewhere, a race with another process). Skip it rather
			// than failing the whole scan.
			continue
		}
		// A symlink's target may sit outside the repo entirely; following it
		// would scan files the user never asked about.
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > limit {
			res.Skipped++
			continue
		}

		content, readErr := os.ReadFile(abs)
		if readErr != nil {
			continue
		}
		if isBinary(content) {
			res.Skipped++
			continue
		}

		res.FilesScanned++
		for _, m := range scan(string(content)) {
			res.Findings = append(res.Findings, Finding{
				File:     rel,
				Line:     m.Line,
				Patterns: m.Patterns,
			})
		}
	}

	res.Sort()
	return res, nil
}

// trackedFiles lists the repo's tracked paths, NUL-separated so a path
// containing a newline (legal on POSIX) cannot split a record.
func trackedFiles(ctx context.Context, repoRoot string) ([]string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("leaks: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, "ls-files", "-z")
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, fmt.Errorf("git ls-files: %s", msg)
	}

	raw := strings.Split(stdout.String(), "\x00")
	paths := make([]string, 0, len(raw))
	for _, p := range raw {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	return paths, nil
}

// keepPath applies the ignore layers and the path allow/denylists to one
// repo-relative path.
//
// It reuses diff.KeepPaths / diff.DropPaths by wrapping the path in a
// single-entry diff, so the glob semantics are byte-identical to what
// --file/--dir/--exclude-* do on a review (ADR-0026). Reimplementing the
// matcher here is exactly how the two would drift apart.
func keepPath(rel string, opts Options) bool {
	parts := strings.Split(rel, "/")
	if opts.Matcher != nil && opts.Matcher.MatchParts(parts) {
		return false
	}
	probe := diff.Diff{Files: []diff.FileDiff{{Path: rel, PathParts: parts}}}

	kept, err := diff.KeepPaths(probe, opts.Files, opts.Dirs)
	if err != nil || len(kept.Files) == 0 {
		return false
	}
	kept, err = diff.DropPaths(kept, opts.ExcludeFiles, opts.ExcludeDirs)
	if err != nil || len(kept.Files) == 0 {
		return false
	}
	return true
}
