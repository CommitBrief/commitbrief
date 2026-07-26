// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"errors"
	"strings"
	"time"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
)

// Commit-level filters, CLI side (ADR-0035).
//
// The global --author / --committer / --start-date / --end-date / --text flags
// select a SET OF COMMITS rather than a single diff. When any of them is set,
// fetchDiff stops asking git for one `git diff` and asks internal/git for the
// concatenated patches of the matching commits instead.
//
// --merges and --max-commits are modifiers: they shape a commit walk but never
// start one. Using either alone is a usage error rather than a silent no-op.

// dateLayout is the only accepted --start-date / --end-date form. Deliberately
// strict: git's approxidate would happily read "next tuesday" and quietly
// resolve a typo like "06-2026" to something unintended.
const dateLayout = "2006-01-02"

// commitFilterFlags are the flag names that switch on the commit walk, used in
// the error messages so the user sees exactly which surface they hit.
const commitFilterFlags = "--author/--committer/--start-date/--end-date/--text"

// buildCommitFilter assembles the filter from the global flags, validating as
// it goes. It returns the zero CommitFilter (Active() == false) when no
// selecting flag is present, which keeps the ordinary `git diff` path intact.
//
// diffArgs are the positional `git diff` arguments (the `diff` / `summary`
// subcommands); they define the revision range the walk covers. With no
// positional args the walk defaults to HEAD — the implicit history walk that
// makes `commitbrief --author alice` work on its own.
func buildCommitFilter(cat *i18n.Catalog, scope reviewScopeFlags, diffArgs []string) (git.CommitFilter, error) {
	f := git.CommitFilter{
		Authors:    trimAll(global.authors),
		Committers: trimAll(global.committers),
		Text:       strings.TrimSpace(global.text),
		Merges:     global.merges,
		MaxCommits: global.maxCommits,
	}

	var err error
	if f.Since, err = parseFilterDate(cat, "--start-date", global.startDate, false); err != nil {
		return git.CommitFilter{}, err
	}
	if f.Until, err = parseFilterDate(cat, "--end-date", global.endDate, true); err != nil {
		return git.CommitFilter{}, err
	}
	if !f.Since.IsZero() && !f.Until.IsZero() && f.Until.Before(f.Since) {
		return git.CommitFilter{}, errors.New(cat.T("filter.date.range_inverted",
			global.startDate, global.endDate))
	}

	if !f.Active() {
		// A modifier on its own can't do anything. Say so instead of running
		// a review that silently ignored a flag the user typed.
		if global.merges || global.maxCommits > 0 {
			return git.CommitFilter{}, errors.New(cat.T("filter.commit.modifier_only", commitFilterFlags))
		}
		return git.CommitFilter{}, nil
	}

	// A commit walk has no staged or unstaged changes to look at; the two
	// scopes are mutually exclusive with the filters by construction.
	if scope.staged || scope.unstaged {
		return git.CommitFilter{}, errors.New(cat.T("filter.commit.scope_conflict", commitFilterFlags))
	}

	rev, ok := commitFilterRev(diffArgs)
	if !ok {
		return git.CommitFilter{}, errors.New(cat.T("filter.commit.unsupported_range",
			strings.Join(diffArgs, " ")))
	}
	f.Rev = rev
	return f, nil
}

// commitFilterRev maps the positional `git diff` arguments onto the revision
// range the commit walk should cover.
//
//	(none)              → HEAD            (the implicit history walk)
//	main...feature      → main..feature   (PR-style: what the branch added)
//	main..feature       → as-is
//	HEAD~3 HEAD         → HEAD~3..HEAD
//	HEAD / <hash>       → as-is           (git log walks the ancestry — which
//	                                       is what a filtered review wants,
//	                                       unlike the summary manifest)
//	anything with a flag or a `--` pathspec → not ok
//
// The last case is refused rather than guessed: `git diff` pathspecs and
// options do not carry over to `git log` with the same meaning, and a wrong
// guess would silently review the wrong commits.
func commitFilterRev(diffArgs []string) ([]string, bool) {
	if len(diffArgs) == 0 {
		return []string{"HEAD"}, true
	}
	for _, a := range diffArgs {
		if a == "--" || strings.HasPrefix(a, "-") {
			return nil, false
		}
	}
	switch len(diffArgs) {
	case 1:
		a := diffArgs[0]
		if strings.Contains(a, "...") {
			return []string{strings.Replace(a, "...", "..", 1)}, true
		}
		return []string{a}, true
	case 2:
		if strings.Contains(diffArgs[0], "..") || strings.Contains(diffArgs[1], "..") {
			return nil, false
		}
		return []string{diffArgs[0] + ".." + diffArgs[1]}, true
	default:
		return nil, false
	}
}

// parseFilterDate accepts a strict YYYY-MM-DD value in the host's local time
// zone. endOfDay expands the value to 23:59:59 so --end-date includes the day
// the user named — git's bare `--until=<date>` stops at that day's midnight,
// which silently drops it.
func parseFilterDate(cat *i18n.Catalog, flag, value string, endOfDay bool) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	ts, err := time.ParseInLocation(dateLayout, value, time.Local)
	if err != nil {
		return time.Time{}, errors.New(cat.T("filter.date.invalid", flag, value))
	}
	if endOfDay {
		ts = ts.Add(24*time.Hour - time.Second)
	}
	return ts, nil
}

// keepAndDropPaths applies the path allowlist then the path denylist, in that
// order, so an exclusion always wins over an inclusion. Every pipeline that
// narrows by path goes through here rather than calling the two in sequence
// itself — the order is a contract, not an implementation detail.
//
// dry-run is the one exception: it calls the two separately because its report
// attributes a file count to each layer.
func keepAndDropPaths(d diff.Diff) (diff.Diff, error) {
	out, err := diff.KeepPaths(d, global.files, global.dirs)
	if err != nil {
		return diff.Diff{}, err
	}
	return diff.DropPaths(out, global.excludeFiles, global.excludeDirs)
}

// commitFilterLimit resolves the effective commit cap for reporting, so the
// dry-run report names the number that actually applied rather than the
// literal flag value (0 means "the default").
func commitFilterLimit(f git.CommitFilter) int {
	if f.MaxCommits > 0 {
		return f.MaxCommits
	}
	return git.DefaultMaxCommits
}

// trimAll drops blank entries so `--author ""` doesn't widen the match to
// everything (matchAny treats an empty needle list as unconstrained).
func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// commitFiltersRequested reports whether the user typed any commit-filter flag
// at all, including the modifiers. Commands that cannot honor them (`commit`,
// `remote pr`) use this to reject rather than silently ignore.
func commitFiltersRequested() bool {
	return len(trimAll(global.authors)) > 0 ||
		len(trimAll(global.committers)) > 0 ||
		strings.TrimSpace(global.startDate) != "" ||
		strings.TrimSpace(global.endDate) != "" ||
		strings.TrimSpace(global.text) != "" ||
		global.merges ||
		global.maxCommits > 0
}

// deriveLogRange turns the user's `git diff` arguments into a clean
// two-endpoint `git log` range for the `summary` commit manifest, returning
// ok=false when no such range can be derived (in which case the summary
// proceeds diff-only, with no commit attribution). It deliberately refuses
// anything ambiguous:
//
//   - "main...develop" → "main..develop"  (PR-style three-dot diff → the
//     commits unique to develop)
//   - "main..develop"  → "main..develop"  (already a range)
//   - "HEAD~3 HEAD"     → "HEAD~3..HEAD"   (two endpoints)
//   - "HEAD" / "<hash>" → ok=false         (a single ref would make git log
//     walk all of history, not "this change")
//   - anything with flags or a `--` pathspec → ok=false
//
// This is stricter than commitFilterRev on purpose: the manifest is an
// attribution aid for a cumulative range diff, so a bare ref would attribute
// the entire project history to the change. A commit-filtered review has the
// opposite need — it *wants* the ancestry walk, bounded by --max-commits.
func deriveLogRange(diffArgs []string) ([]string, bool) {
	if len(diffArgs) == 0 {
		return nil, false
	}
	for _, a := range diffArgs {
		if a == "--" || strings.HasPrefix(a, "-") {
			return nil, false
		}
	}
	switch len(diffArgs) {
	case 1:
		a := diffArgs[0]
		if strings.Contains(a, "...") {
			return []string{strings.Replace(a, "...", "..", 1)}, true
		}
		if strings.Contains(a, "..") {
			return []string{a}, true
		}
		return nil, false
	case 2:
		// Two bare refs ("main feature", "HEAD~3 HEAD") → a..b. Refs already
		// carrying range syntax here would be malformed git diff input, so a
		// plain join is the faithful mapping.
		if strings.Contains(diffArgs[0], "..") || strings.Contains(diffArgs[1], "..") {
			return nil, false
		}
		return []string{diffArgs[0] + ".." + diffArgs[1]}, true
	default:
		return nil, false
	}
}
