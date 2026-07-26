// SPDX-License-Identifier: GPL-3.0-or-later

// Package leaks scans a repository for credential-shaped content (ADR-0036).
//
// It is the standalone counterpart to the pre-send scanner in internal/guard.
// That one is a gate: it sees the added lines of the one diff about to be sent
// to a provider, and its job is to stop the send. This one is an audit: it
// reads whole files in the working tree and the added lines of arbitrary
// historical commits, and its job is to tell you what is there.
//
// The pattern set is not duplicated — guard owns it, including the ADR-0024
// user extensions, and this package drives it over two new inputs. guard stays
// a leaf (it must not import internal/git); the orchestration lives here.
//
// # The redaction invariant
//
// A Finding records a file, a line number and the pattern names that matched.
// It NEVER records the matched text. That is the ADR-0007 invariant, restated
// in engineering/standards/security.md, and it matters more here than in guard
// because this package reads whole files and writes a report the user is
// likely to paste somewhere. There is a test asserting the secret never
// reaches the output; keep it.
package leaks

import (
	"sort"
	"time"

	"github.com/CommitBrief/commitbrief/internal/guard"
	"github.com/CommitBrief/commitbrief/internal/ignore"
)

// Finding is one credential-shaped hit.
type Finding struct {
	// File is the repo-relative, slash-normalized path.
	File string
	// Line is 1-based: the line in the file for a worktree hit, or the line in
	// the commit's post-image for a history hit.
	Line int
	// Patterns are the alphabetised names of every pattern that matched.
	Patterns []string

	// Commit attribution, empty for a worktree hit. Knowing who introduced a
	// key and when is what decides whether it still needs rotating.
	Commit string
	Short  string
	Author string
	Date   time.Time
}

// FromHistory reports whether this hit came from a commit rather than the
// current working tree.
func (f Finding) FromHistory() bool { return f.Commit != "" }

// Options are the inputs shared by both scan halves.
type Options struct {
	// Patterns is the compiled user-pattern set from guard.CompileUserPatterns.
	// The built-ins always run regardless; this is additive only (ADR-0024).
	Patterns []guard.UserSecretPattern

	// Matcher is the composed ignore layer set (built-ins + .commitbriefignore).
	// nil means no ignore filtering.
	Matcher *ignore.Matcher

	// Files/Dirs narrow to these paths; ExcludeFiles/ExcludeDirs remove from
	// the result. Same semantics as the review's --file/--dir/--exclude-*.
	Files        []string
	Dirs         []string
	ExcludeFiles []string
	ExcludeDirs  []string

	// MaxFileBytes caps how large a file may be before it is skipped; 0 uses
	// DefaultMaxFileBytes.
	MaxFileBytes int64
}

// Result is one half's outcome.
type Result struct {
	Findings []Finding

	// FilesScanned counts files actually read (worktree) or file entries
	// examined (history).
	FilesScanned int
	// CommitsScanned is 0 for the worktree half.
	CommitsScanned int
	// Skipped counts files passed over as binary or oversized. Reported, never
	// silent — a scanner that quietly ignores half a repo is worse than none.
	Skipped int
	// Truncated is set when the commit walk hit its cap.
	Truncated bool
}

// Merge folds another Result into this one, concatenating findings and summing
// the counters. Used to combine the worktree and history halves into one report.
func (r Result) Merge(other Result) Result {
	return Result{
		Findings:       append(append([]Finding{}, r.Findings...), other.Findings...),
		FilesScanned:   r.FilesScanned + other.FilesScanned,
		CommitsScanned: r.CommitsScanned + other.CommitsScanned,
		Skipped:        r.Skipped + other.Skipped,
		Truncated:      r.Truncated || other.Truncated,
	}
}

// Sort orders findings deterministically: worktree hits before history hits,
// then by path, then by line, then by commit. Two runs over an unchanged repo
// must produce byte-identical reports, or diffing two scans is useless.
func (r *Result) Sort() {
	sort.SliceStable(r.Findings, func(i, j int) bool {
		a, b := r.Findings[i], r.Findings[j]
		if a.FromHistory() != b.FromHistory() {
			return !a.FromHistory()
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Commit < b.Commit
	})
}

// scanFunc scans arbitrary text and returns the matching lines.
//
// This exists because guard.CompileUserPatterns returns an *unexported* type:
// a caller outside guard can hold the value (type inference) but cannot name
// it, so it can never become a struct field or an explicit parameter. Wrapping
// the compiled set in a closure captures it without naming it — and keeps
// guard's API untouched, which is the point (its narrow surface is what keeps
// it a leaf package).
type scanFunc func(content string) []guard.SecretMatch

// newScanner compiles the user patterns once and returns a scanner over the
// effective set (built-ins ++ user, built-ins winning on a name collision —
// ADR-0024). An invalid user regex fails here, before any file is read.
func newScanner(opts Options) (scanFunc, error) {
	extra, err := guard.CompileUserPatterns(opts.Patterns)
	if err != nil {
		return nil, err
	}
	return func(content string) []guard.SecretMatch {
		return guard.ScanTextWith(content, extra)
	}, nil
}

// patternSeverityHigh names the built-in patterns that do NOT warrant
// `critical`. A JWT is the one built-in with a real false-positive rate —
// expired or sample tokens are common in fixtures and docs — so it is reported
// a notch lower rather than being dropped or crying wolf.
var patternSeverityHigh = map[string]struct{}{
	"JWT": {},
}

// IsHighNotCritical reports whether a pattern name should be surfaced as
// `high` rather than `critical`. User patterns (ADR-0024) are also `high`:
// they are somebody's house format and this package cannot assert how bad a
// hit really is.
func IsHighNotCritical(pattern string, userPatterns []guard.UserSecretPattern) bool {
	if _, ok := patternSeverityHigh[pattern]; ok {
		return true
	}
	for _, p := range userPatterns {
		if p.Name == pattern {
			return true
		}
	}
	return false
}
