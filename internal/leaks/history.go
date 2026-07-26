// SPDX-License-Identifier: GPL-3.0-or-later

package leaks

import (
	"context"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
)

// ScanHistory reports credential-shaped content in the ADDED lines of the
// selected commits.
//
// Added lines only, deliberately: a secret that was committed and later
// removed is still in the history — reachable by anyone who clones the repo —
// and finding exactly that is the point of scanning history at all. Scanning
// context lines instead would re-report the same key once per commit that
// touched the file near it.
//
// Each commit's patch is fetched and scanned separately rather than as one
// concatenated blob, because a finding is only actionable with attribution:
// which commit introduced it, by whom, and when decides whether the key still
// needs rotating.
func ScanHistory(ctx context.Context, repoRoot string, sel git.Selection, opts Options) (Result, error) {
	scan, err := newScanner(opts)
	if err != nil {
		return Result{}, err
	}

	res := Result{Truncated: sel.Truncated}
	matcher := opts.Matcher

	for _, commit := range sel.Commits {
		patch, pErr := git.PatchesFor(ctx, repoRoot, []git.CommitMeta{commit})
		if pErr != nil {
			return Result{}, pErr
		}
		res.CommitsScanned++
		if patch == "" {
			continue
		}

		parsed, parseErr := diff.Parse(git.Diff{Content: patch, Origin: git.OriginFiltered})
		if parseErr != nil {
			// A commit whose patch will not parse (an exotic mode change, a
			// malformed submodule entry) should not sink the whole scan.
			continue
		}
		if matcher != nil {
			parsed = diff.Filter(parsed, matcher)
		}
		parsed, parseErr = diff.KeepPaths(parsed, opts.Files, opts.Dirs)
		if parseErr != nil {
			return Result{}, parseErr
		}
		parsed, parseErr = diff.DropPaths(parsed, opts.ExcludeFiles, opts.ExcludeDirs)
		if parseErr != nil {
			return Result{}, parseErr
		}

		for _, f := range parsed.Files {
			if f.Binary {
				res.Skipped++
				continue
			}
			res.FilesScanned++
			for _, hit := range scanAddedLines(f, scan) {
				hit.Commit = commit.Hash
				hit.Short = commit.Short
				hit.Author = commit.Author
				hit.Date = commit.Date
				res.Findings = append(res.Findings, hit)
			}
		}
	}

	res.Sort()
	return res, nil
}

// scanAddedLines scans one file's added lines, translating each hunk offset
// into a real line number in that commit's post-image.
//
// The mapping matters: guard's SecretMatch.Line counts lines within the diff
// *string*, which is meaningless once the diff is gone. Walking the hunks and
// tracking NewStart gives a number that actually points at the file as that
// commit left it — the number a reader needs to go look.
func scanAddedLines(f diff.FileDiff, scan scanFunc) []Finding {
	var out []Finding
	for _, h := range f.Hunks {
		line := h.NewStart
		for _, l := range h.Lines {
			switch l.Kind {
			case diff.LineDel:
				// Deleted lines do not exist in the post-image, so they do not
				// advance the new-side counter.
				continue
			case diff.LineAdd:
				if matches := scan(l.Text); len(matches) > 0 {
					out = append(out, Finding{
						File:     f.Path,
						Line:     line,
						Patterns: matches[0].Patterns,
					})
				}
			}
			line++
		}
	}
	return out
}
