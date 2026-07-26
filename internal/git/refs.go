// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Branch topology (ADR-0037) — the input for `commitbrief map --branches`.
//
// Read-only, `git for-each-ref` + `git rev-list --left-right --count`. Like the
// rest of internal/git's newer surface these are package-level functions beside
// the Repo interface rather than methods on it: go-git implements none of them,
// so widening the interface would only add ErrUnsupported stubs (ADR-0035 §E).

// maxBranches bounds how many refs a topology view will describe. A repo with
// thousands of stale remote branches would otherwise spend a `git rev-list` per
// ref to render a screen nobody can read.
const maxBranches = 200

// Branch is one ref plus its position relative to the base branch.
type Branch struct {
	Name    string    // short ref name, e.g. "main" or "origin/feature/x"
	Hash    string    // commit the ref points at
	Remote  bool      // true for refs/remotes/*
	Subject string    // the tip commit's subject
	Author  string    // the tip commit's author name
	Date    time.Time // the tip commit's author date
	Ahead   int       // commits on this ref that the base lacks
	Behind  int       // commits on the base that this ref lacks
	IsBase  bool      // this ref IS the base; Ahead/Behind are 0 by definition
}

// BranchTopology returns every local and remote-tracking branch, each measured
// against base. A base of "" is resolved with DefaultBranch.
//
// Ahead/behind are computed per ref with `git rev-list --left-right --count
// base...ref`, which is symmetric-difference counting — exactly what "3 ahead,
// 12 behind" means to a human. Refs are ordered base-first, then local, then
// remote, then by name, so the output is deterministic.
func BranchTopology(ctx context.Context, repoRoot, base string) ([]Branch, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNoGitCLI
	}
	if strings.TrimSpace(base) == "" {
		base = DefaultBranch(ctx, repoRoot)
	}

	out, err := runGit(ctx, bin, repoRoot, []string{
		"for-each-ref",
		"--format=%(refname:short)" + fieldSepLiteral +
			"%(objectname)" + fieldSepLiteral +
			"%(contents:subject)" + fieldSepLiteral +
			"%(authorname)" + fieldSepLiteral +
			"%(authordate:iso-strict)" + fieldSepLiteral +
			"%(refname)",
		"refs/heads", "refs/remotes",
	})
	if err != nil {
		return nil, err
	}

	branches := make([]Branch, 0, 16)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, fieldSepLiteral)
		if len(fields) < 6 {
			continue
		}
		name := strings.TrimSpace(fields[0])
		// origin/HEAD is a symbolic alias for another branch, not a line of
		// work of its own — listing it would double-count. Checked against the
		// FULL refname because the short form is bare "origin".
		if name == "" || isSymbolicHEAD(strings.TrimSpace(fields[5])) {
			continue
		}
		b := Branch{
			Name:    name,
			Hash:    strings.TrimSpace(fields[1]),
			Subject: strings.TrimSpace(fields[2]),
			Author:  strings.TrimSpace(fields[3]),
			Remote:  strings.HasPrefix(strings.TrimSpace(fields[5]), "refs/remotes/"),
			IsBase:  name == base,
		}
		if ts, perr := time.Parse(time.RFC3339, strings.TrimSpace(fields[4])); perr == nil {
			b.Date = ts
		}
		branches = append(branches, b)
		if len(branches) >= maxBranches {
			break
		}
	}

	for i := range branches {
		if branches[i].IsBase {
			continue
		}
		ahead, behind, cErr := aheadBehind(ctx, bin, repoRoot, base, branches[i].Name)
		if cErr != nil {
			// An unrelated history (no merge base) makes the comparison
			// meaningless rather than fatal — leave the counts at zero and
			// keep rendering the rest of the topology.
			continue
		}
		branches[i].Ahead, branches[i].Behind = ahead, behind
	}

	sortBranches(branches)
	return branches, nil
}

// aheadBehind counts the symmetric difference between base and ref.
// `--left-right --count` prints "<behind>\t<ahead>": the left side is what
// base has and ref doesn't, the right side the reverse.
func aheadBehind(ctx context.Context, bin, repoRoot, base, ref string) (ahead, behind int, err error) {
	out, err := runGit(ctx, bin, repoRoot, []string{
		"rev-list", "--left-right", "--count", base + "..." + ref,
	})
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Fields(out)
	if len(fields) < 2 {
		return 0, 0, fmt.Errorf("git rev-list --count %s...%s: unexpected output %q", base, ref, out)
	}
	behind, err = strconv.Atoi(fields[0])
	if err != nil {
		return 0, 0, err
	}
	ahead, err = strconv.Atoi(fields[1])
	if err != nil {
		return 0, 0, err
	}
	return ahead, behind, nil
}

// sortBranches orders base first, then locals, then remotes, then by name —
// so the base branch anchors the top of the view and a run is reproducible.
func sortBranches(branches []Branch) {
	rank := func(b Branch) int {
		switch {
		case b.IsBase:
			return 0
		case !b.Remote:
			return 1
		default:
			return 2
		}
	}
	for i := 1; i < len(branches); i++ {
		for j := i; j > 0; j-- {
			a, b := branches[j-1], branches[j]
			if rank(a) < rank(b) || (rank(a) == rank(b) && a.Name <= b.Name) {
				break
			}
			branches[j-1], branches[j] = b, a
		}
	}
}

// RefsByCommit maps a commit hash to the short names of every branch and tag
// pointing at it, for the graph's `(main, v1.2.0)` labels.
//
// Best-effort: a repo with no refs, or a git that refuses the query, yields an
// empty map rather than an error — labels are decoration, and a graph without
// them is still a graph.
func RefsByCommit(ctx context.Context, repoRoot string) (map[string][]string, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNoGitCLI
	}
	out, err := runGit(ctx, bin, repoRoot, []string{
		"for-each-ref",
		"--format=%(objectname)" + fieldSepLiteral +
			"%(refname:short)" + fieldSepLiteral +
			"%(refname)",
		"refs/heads", "refs/remotes", "refs/tags",
	})
	if err != nil {
		return nil, err
	}
	refs := make(map[string][]string)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, fieldSepLiteral)
		if len(fields) < 3 {
			continue
		}
		hash, name, full := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1]), strings.TrimSpace(fields[2])
		if hash == "" || name == "" || isSymbolicHEAD(full) {
			continue
		}
		refs[hash] = append(refs[hash], name)
	}
	return refs, nil
}

// isSymbolicHEAD reports whether a full refname is a symbolic HEAD alias such
// as refs/remotes/origin/HEAD. The check MUST use the full refname: the short
// form of refs/remotes/origin/HEAD is bare "origin", so filtering on the short
// name lets it through and the graph grows a phantom "origin" label.
func isSymbolicHEAD(fullRef string) bool {
	return strings.HasSuffix(fullRef, "/HEAD")
}

// DefaultBranch resolves the repo's base branch: the target of
// refs/remotes/origin/HEAD when it exists, else the first of main/master/trunk
// that does, else the current HEAD. It never fails — a topology view with an
// imperfect base is far more useful than an error.
func DefaultBranch(ctx context.Context, repoRoot string) string {
	bin, err := exec.LookPath("git")
	if err != nil {
		return "main"
	}
	if out, sErr := runGit(ctx, bin, repoRoot, []string{
		"symbolic-ref", "--short", "refs/remotes/origin/HEAD",
	}); sErr == nil {
		// "origin/main" → "main": the local branch is the useful comparison
		// point, and it is what a user means by "the base".
		if name := strings.TrimSpace(out); name != "" {
			return strings.TrimPrefix(name, "origin/")
		}
	}
	for _, candidate := range []string{"main", "master", "trunk"} {
		if _, vErr := runGit(ctx, bin, repoRoot, []string{
			"rev-parse", "--verify", "--quiet", candidate,
		}); vErr == nil {
			return candidate
		}
	}
	if out, hErr := runGit(ctx, bin, repoRoot, []string{
		"rev-parse", "--abbrev-ref", "HEAD",
	}); hErr == nil {
		if name := strings.TrimSpace(out); name != "" && name != "HEAD" {
			return name
		}
	}
	return "HEAD"
}

// fieldSepLiteral is the raw US byte. for-each-ref has no %x1f escape (that is
// a `git log` pretty-format feature), so the separator goes in literally —
// which is fine here because, unlike --format on log, for-each-ref does not
// reject a format that lacks a `%`.
const fieldSepLiteral = "\x1f"
