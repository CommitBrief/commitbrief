// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Commit-level filtering (ADR-0035).
//
// `git diff` has no --author/--since/--until/--grep — those are `git log`
// options — so a filter that selects *commits* cannot ride the normal
// diff-acquisition path. This file implements the alternative: pick the
// matching commits first, then assemble a diff from exactly those commits.
//
// Three phases, each a single `git` invocation:
//
//	A.  metadata walk   — `git log` over the rev range, no patches, so the
//	                      output stays small even on a long history.
//	A′. ref-name match  — only when Text is set: branches whose NAME matches
//	                      contribute the commits unique to them.
//	B.  patch fetch     — `git show` for exactly the selected hashes.
//
// Date and merge filtering push down to git (unambiguous). Author, committer
// and message matching happen HERE, in Go, deliberately: `git log --author=A
// --grep=B` ORs the two by default and --all-match would then also AND
// multiple --author values together. Doing it ourselves gives the semantics
// users expect — AND across filter kinds, OR within one kind — with no
// dependence on that surprising git behavior.

const (
	// DefaultMaxCommits bounds how many matching commits are folded into one
	// review. A broad filter (`--author me` on a year-old repo) would
	// otherwise assemble a diff far past any model's context window.
	DefaultMaxCommits = 200

	// walkLimit bounds phase A itself. Even without patches, an unbounded
	// `git log` on a 100k-commit repo is megabytes of metadata for nothing.
	// Hitting it sets Selection.WalkTruncated so the caller can say so out
	// loud rather than silently reviewing a subset.
	walkLimit = 10000

	// showBatch caps how many hashes go into one `git show` argv, keeping us
	// clear of the platform argument-length limit on a large selection.
	showBatch = 100

	// maxRefs bounds the ref list fed to phase A′'s rev-list exclusion set.
	maxRefs = 1000
)

// CommitFilter is the commit-level narrowing requested on the command line.
// The zero value selects nothing and reports Active() == false, which is how
// the pipeline decides to keep using the ordinary `git diff` path.
type CommitFilter struct {
	// Authors / Committers match case-insensitively against BOTH the name and
	// the email, so `--author ayse` and `--author ayse@example.com` both work.
	// Multiple values are OR'd; the two kinds are AND'd with each other.
	Authors    []string
	Committers []string

	// Since / Until bound the author date. Zero means unbounded. Until is
	// expected to already carry the end-of-day time (the CLI expands
	// --end-date so the named day is inclusive).
	Since time.Time
	Until time.Time

	// Text matches the commit subject+body, and additionally the NAME of a
	// branch/remote ref — a ref whose short name contains Text contributes
	// the commits unique to it (phase A′).
	Text string

	// Merges includes merge commits in the selection. Off by default: a
	// merge's changes are already carried by the commits it merges, and its
	// patch is noise.
	Merges bool

	// MaxCommits caps the selection; 0 means DefaultMaxCommits.
	MaxCommits int

	// Rev is the revision range to walk, e.g. ["main..HEAD"]. Empty means
	// ["HEAD"] — the implicit history walk.
	Rev []string
}

// Active reports whether any *selecting* filter is set. Merges and MaxCommits
// are deliberately excluded: they only modify a selection, they never create
// one, so `--merges` alone must not silently turn a staged review into a
// history walk. The CLI rejects a modifier used on its own.
func (f CommitFilter) Active() bool {
	return len(f.Authors) > 0 ||
		len(f.Committers) > 0 ||
		!f.Since.IsZero() ||
		!f.Until.IsZero() ||
		f.Text != ""
}

func (f CommitFilter) limit() int {
	if f.MaxCommits > 0 {
		return f.MaxCommits
	}
	return DefaultMaxCommits
}

func (f CommitFilter) revs() []string {
	if len(f.Rev) == 0 {
		return []string{"HEAD"}
	}
	return f.Rev
}

// Selection is the outcome of a commit walk: the matching commits plus enough
// accounting for the caller to report honestly what was and wasn't covered.
type Selection struct {
	Commits []CommitMeta // newest first

	// Walked is how many commits phase A inspected.
	Walked int
	// Truncated is set when more commits matched than MaxCommits allowed.
	Truncated bool
	// WalkTruncated is set when phase A itself hit walkLimit, meaning older
	// history was never inspected at all.
	WalkTruncated bool
}

// Record/field separators as git format escapes. They expand to the same
// RS/US control characters log.go uses, but written as `%x1e`/`%x1f` rather
// than raw bytes: git only treats a --format value as a user format when it
// contains a `%`, so `--format=<raw RS>` is rejected outright as an unknown
// builtin format name.
const (
	fmtRecordSep = "%x1e"
	fmtFieldSep  = "%x1f"
)

// selectRecordFormat pins the phase-A per-commit layout. The trailing field
// separator closes the format so the --name-status block git appends lands in
// its own field.
const selectRecordFormat = "--format=" + fmtRecordSep +
	"%H" + fmtFieldSep +
	"%h" + fmtFieldSep +
	"%an" + fmtFieldSep +
	"%ae" + fmtFieldSep +
	"%cn" + fmtFieldSep +
	"%ce" + fmtFieldSep +
	"%aI" + fmtFieldSep +
	"%P" + fmtFieldSep +
	"%s" + fmtFieldSep +
	"%b" + fmtFieldSep

// showRecordFormat emits nothing but the record separator, so a `git show`
// block is the separator followed directly by the patch.
const showRecordFormat = "--format=" + fmtRecordSep

// selectRecordFields is how many US-separated fields selectRecordFormat plus
// the --name-status block produce.
const selectRecordFields = 11

// FilteredDiff selects the commits matching f and returns their concatenated
// patches together with the selection metadata. It is read-only: `git log`,
// `git for-each-ref`, `git rev-list`, `git show`.
func FilteredDiff(ctx context.Context, repoRoot string, f CommitFilter) (Diff, Selection, error) {
	sel, err := SelectCommits(ctx, repoRoot, f)
	if err != nil {
		return Diff{}, Selection{}, err
	}
	if len(sel.Commits) == 0 {
		return Diff{
			Origin: OriginFiltered,
			Args:   selectionArgs(f, sel),
		}, sel, nil
	}
	content, err := PatchesFor(ctx, repoRoot, sel.Commits)
	if err != nil {
		return Diff{}, Selection{}, err
	}
	return Diff{
		Content: content,
		Origin:  OriginFiltered,
		Args:    selectionArgs(f, sel),
	}, sel, nil
}

// selectionArgs surfaces what the filter resolved to, for renderers and
// cache-key debug output — mirroring what the Diff*() helpers do with their
// own inputs.
func selectionArgs(f CommitFilter, sel Selection) map[string]string {
	return map[string]string{
		"rev":     strings.Join(f.revs(), " "),
		"commits": strconv.Itoa(len(sel.Commits)),
	}
}

// SelectCommits runs phases A and A′ and returns the matching commits, newest
// first, capped at f.MaxCommits.
func SelectCommits(ctx context.Context, repoRoot string, f CommitFilter) (Selection, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return Selection{}, ErrNoGitCLI
	}

	walked, err := walkCommits(ctx, bin, repoRoot, f, f.revs())
	if err != nil {
		return Selection{}, err
	}
	sel := Selection{Walked: len(walked), WalkTruncated: len(walked) >= walkLimit}

	matched := make([]CommitMeta, 0, len(walked))
	seen := make(map[string]struct{}, len(walked))
	for _, c := range walked {
		if !f.matches(c) {
			continue
		}
		if _, dup := seen[c.Hash]; dup {
			continue
		}
		seen[c.Hash] = struct{}{}
		matched = append(matched, c)
	}

	// Phase A′: branches whose NAME matches --text contribute their own
	// commits, which the rev-range walk above may never have visited.
	if f.Text != "" {
		fromRefs, refErr := commitsFromMatchingRefs(ctx, bin, repoRoot, f)
		if refErr != nil {
			return Selection{}, refErr
		}
		sel.Walked += len(fromRefs)
		for _, c := range fromRefs {
			if _, dup := seen[c.Hash]; dup {
				continue
			}
			// The ref name already satisfied the text predicate; the
			// identity/date filters still apply.
			if !f.matchesIdentity(c) || !f.matchesDate(c) {
				continue
			}
			seen[c.Hash] = struct{}{}
			matched = append(matched, c)
		}
	}

	// Newest first, hash as a deterministic tie-break so two commits sharing
	// a timestamp never reorder between runs (which would churn the cache key).
	sort.SliceStable(matched, func(i, j int) bool {
		if matched[i].Date.Equal(matched[j].Date) {
			return matched[i].Hash < matched[j].Hash
		}
		return matched[i].Date.After(matched[j].Date)
	})

	if limit := f.limit(); len(matched) > limit {
		matched = matched[:limit]
		sel.Truncated = true
	}
	sel.Commits = matched
	return sel, nil
}

// matches applies the full predicate: identity AND date AND text.
func (f CommitFilter) matches(c CommitMeta) bool {
	return f.matchesIdentity(c) && f.matchesDate(c) && f.matchesText(c)
}

func (f CommitFilter) matchesIdentity(c CommitMeta) bool {
	if !matchAny(f.Authors, c.Author, c.AuthorEmail) {
		return false
	}
	return matchAny(f.Committers, c.Committer, c.CommitterEmail)
}

// matchesDate re-checks the bounds in Go even though they were pushed down to
// git. `git log --since` compares against the COMMITTER date while our filter
// is documented against the AUTHOR date, so the pushdown is a cheap
// pre-narrowing and this is the authoritative check.
func (f CommitFilter) matchesDate(c CommitMeta) bool {
	if c.Date.IsZero() {
		return true
	}
	if !f.Since.IsZero() && c.Date.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && c.Date.After(f.Until) {
		return false
	}
	return true
}

func (f CommitFilter) matchesText(c CommitMeta) bool {
	if f.Text == "" {
		return true
	}
	needle := strings.ToLower(f.Text)
	return strings.Contains(strings.ToLower(c.Subject), needle) ||
		strings.Contains(strings.ToLower(c.Body), needle)
}

// matchAny reports whether any needle is a case-insensitive substring of any
// haystack. An empty needle list means "unconstrained" and matches everything.
func matchAny(needles []string, haystacks ...string) bool {
	if len(needles) == 0 {
		return true
	}
	for _, n := range needles {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" {
			continue
		}
		for _, h := range haystacks {
			if strings.Contains(strings.ToLower(h), n) {
				return true
			}
		}
	}
	return false
}

// walkCommits is phase A: metadata only, no patches.
func walkCommits(ctx context.Context, bin, repoRoot string, f CommitFilter, revs []string) ([]CommitMeta, error) {
	args := []string{"log", "--no-color", "--name-status", selectRecordFormat,
		fmt.Sprintf("-n%d", walkLimit)}
	args = append(args, dateArgs(f)...)
	if !f.Merges {
		args = append(args, "--no-merges")
	}
	args = append(args, revs...)

	out, err := runGit(ctx, bin, repoRoot, args)
	if err != nil {
		return nil, err
	}
	return parseSelectCommits(out), nil
}

// hydrateCommits fetches metadata for an explicit hash list (phase A′'s
// output), using --no-walk so git prints exactly those commits.
func hydrateCommits(ctx context.Context, bin, repoRoot string, hashes []string) ([]CommitMeta, error) {
	if len(hashes) == 0 {
		return nil, nil
	}
	var all []CommitMeta
	for _, batch := range chunk(hashes, showBatch) {
		args := []string{"log", "--no-color", "--no-walk", "--name-status", selectRecordFormat}
		args = append(args, batch...)
		out, err := runGit(ctx, bin, repoRoot, args)
		if err != nil {
			return nil, err
		}
		all = append(all, parseSelectCommits(out)...)
	}
	return all, nil
}

// commitsFromMatchingRefs is phase A′. Refs whose SHORT NAME contains the text
// contribute the commits that exist on them and on no other ref — the closest
// git can get to "what happened on that branch".
//
// Best-effort by nature: a squash- or rebase-merged branch no longer owns its
// commits, so this finds nothing for it. That is a property of git history,
// not a bug here, and the docs say so.
func commitsFromMatchingRefs(ctx context.Context, bin, repoRoot string, f CommitFilter) ([]CommitMeta, error) {
	refs, err := listRefs(ctx, bin, repoRoot)
	if err != nil {
		// A repo with no refs at all (fresh init) is not an error condition
		// for the caller — it just means no branch matched.
		return nil, nil
	}
	needle := strings.ToLower(f.Text)
	var matching, others []string
	for _, r := range refs {
		if strings.Contains(strings.ToLower(r), needle) {
			matching = append(matching, r)
		} else {
			others = append(others, r)
		}
	}
	if len(matching) == 0 {
		return nil, nil
	}
	if len(others) > maxRefs {
		others = others[:maxRefs]
	}

	args := []string{"rev-list", fmt.Sprintf("-n%d", f.limit())}
	args = append(args, dateArgs(f)...)
	if !f.Merges {
		args = append(args, "--no-merges")
	}
	args = append(args, matching...)
	if len(others) > 0 {
		args = append(args, "--not")
		args = append(args, others...)
	}

	out, err := runGit(ctx, bin, repoRoot, args)
	if err != nil {
		return nil, err
	}
	var hashes []string
	for _, line := range strings.Split(out, "\n") {
		if h := strings.TrimSpace(line); h != "" {
			hashes = append(hashes, h)
		}
	}
	return hydrateCommits(ctx, bin, repoRoot, hashes)
}

// listRefs returns the short names of every local branch and remote-tracking
// branch. Tags are excluded on purpose: `--text` is about branch names, and a
// tag is not a line of work.
func listRefs(ctx context.Context, bin, repoRoot string) ([]string, error) {
	out, err := runGit(ctx, bin, repoRoot, []string{
		"for-each-ref", "--format=%(refname:short)", "refs/heads", "refs/remotes",
	})
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		// `origin/HEAD` is a symbolic alias, not a branch of its own.
		if name == "" || strings.HasSuffix(name, "/HEAD") {
			continue
		}
		refs = append(refs, name)
	}
	return refs, nil
}

// dateArgs pushes the date bounds down to git. `git log --since/--until`
// compares the committer date, which is a superset filter for our
// author-date semantics in every ordinary history and a cheap pre-narrowing
// in the rest; matchesDate does the authoritative check.
func dateArgs(f CommitFilter) []string {
	var args []string
	if !f.Since.IsZero() {
		args = append(args, "--since="+f.Since.Format(gitDateLayout))
	}
	if !f.Until.IsZero() {
		args = append(args, "--until="+f.Until.Format(gitDateLayout))
	}
	return args
}

// gitDateLayout is the unambiguous form git parses without approxidate
// guesswork, including the offset so the caller's local midnight is preserved.
const gitDateLayout = "2006-01-02T15:04:05-07:00"

// PatchesFor is phase B: the concatenated patches of the given commits, in the
// order given.
//
// `git show` is used rather than `git log -p` because we need per-commit
// framing we control. A raw `git log -p` interleaves the commit message
// indented by four spaces, and internal/diff's parser reads a leading-space
// line as hunk CONTEXT — a message body would be silently absorbed into the
// previous hunk. Emitting a bare record separator as the format and slicing
// each block from its first `diff --git` removes that whole class of problem.
//
// `-m --first-parent` is what makes a merge commit produce a patch at all
// (git shows nothing for a merge otherwise); it is a no-op on ordinary
// commits.
func PatchesFor(ctx context.Context, repoRoot string, commits []CommitMeta) (string, error) {
	if len(commits) == 0 {
		return "", nil
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", ErrNoGitCLI
	}
	hashes := make([]string, 0, len(commits))
	for _, c := range commits {
		if c.Hash != "" {
			hashes = append(hashes, c.Hash)
		}
	}

	var sb strings.Builder
	for _, batch := range chunk(hashes, showBatch) {
		args := []string{"show", "--no-color", "--no-ext-diff", "-m", "--first-parent",
			showRecordFormat}
		args = append(args, batch...)
		out, err := runGit(ctx, bin, repoRoot, args)
		if err != nil {
			return "", err
		}
		for _, block := range strings.Split(out, logRecordSep) {
			patch := patchBody(block)
			if patch == "" {
				continue
			}
			sb.WriteString(patch)
			if !strings.HasSuffix(patch, "\n") {
				sb.WriteString("\n")
			}
		}
	}
	return sb.String(), nil
}

// patchBody slices a `git show` block from its first `diff --git` line to the
// end, dropping the format/record preamble. A commit with no textual change
// (an empty commit, or a merge that resolved to nothing) yields "".
func patchBody(block string) string {
	const marker = "diff --git "
	if strings.HasPrefix(block, marker) {
		return block
	}
	if i := strings.Index(block, "\n"+marker); i >= 0 {
		return block[i+1:]
	}
	return ""
}

// parseSelectCommits turns phase A / A′ output into CommitMeta records. Split
// on RS yields one block per commit (the leading element, before the first RS,
// is empty); each block splits on US into the fixed field list plus the
// trailing --name-status block. Malformed blocks are skipped rather than
// aborting — a single unparseable record must not fail the whole run.
func parseSelectCommits(out string) []CommitMeta {
	blocks := strings.Split(out, logRecordSep)
	commits := make([]CommitMeta, 0, len(blocks))
	for _, block := range blocks {
		if strings.TrimSpace(block) == "" {
			continue
		}
		fields := strings.SplitN(block, logFieldSep, selectRecordFields)
		if len(fields) < selectRecordFields-1 {
			continue
		}
		hash := strings.TrimSpace(fields[0])
		if hash == "" {
			continue
		}
		c := CommitMeta{
			Hash:           hash,
			Short:          strings.TrimSpace(fields[1]),
			Author:         strings.TrimSpace(fields[2]),
			AuthorEmail:    strings.TrimSpace(fields[3]),
			Committer:      strings.TrimSpace(fields[4]),
			CommitterEmail: strings.TrimSpace(fields[5]),
			Parents:        strings.Fields(fields[7]),
			Subject:        strings.TrimSpace(fields[8]),
			Body:           strings.TrimSpace(fields[9]),
		}
		if ts, err := time.Parse(time.RFC3339, strings.TrimSpace(fields[6])); err == nil {
			c.Date = ts
		}
		if len(fields) == selectRecordFields {
			c.Files = parseNameStatus(fields[10])
		}
		commits = append(commits, c)
	}
	return commits
}

func chunk(items []string, size int) [][]string {
	if len(items) == 0 {
		return nil
	}
	var out [][]string
	for i := 0; i < len(items); i += size {
		end := i + size
		if end > len(items) {
			end = len(items)
		}
		out = append(out, items[i:end])
	}
	return out
}

// runGit is the single exec seam for this file, mirroring CLIRepo.run's error
// shape so a git failure reads the same wherever it surfaces.
func runGit(ctx context.Context, bin, repoRoot string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = repoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}
