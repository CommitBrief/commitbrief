// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"errors"
	"fmt"

	gogit "github.com/go-git/go-git/v5"
)

type GoGitRepo struct {
	repo *gogit.Repository
	root string
}

func NewGoGitRepo(root string) (*GoGitRepo, error) {
	r, err := gogit.PlainOpen(root)
	if err != nil {
		if errors.Is(err, gogit.ErrRepositoryNotExists) {
			return nil, ErrNotARepo
		}
		return nil, fmt.Errorf("git: open %s: %w", root, err)
	}
	return &GoGitRepo{repo: r, root: root}, nil
}

func (g *GoGitRepo) Root() string { return g.root }

// Working-tree operations are deferred to the CLI implementation: go-git's
// index-vs-worktree and HEAD-vs-index plumbing requires substantial glue code
// (status walking + per-file blob diffing + unified-diff formatting) and the
// behavior diverges from `git diff` on edge cases (rename detection, mode
// bits, LFS pointers). The dispatcher falls back to CLIRepo for these.

func (g *GoGitRepo) StagedDiff() (Diff, error)   { return Diff{}, ErrUnsupported }
func (g *GoGitRepo) UnstagedDiff() (Diff, error) { return Diff{}, ErrUnsupported }

// Diff (arbitrary `git diff <args>` passthrough) is intentionally
// unsupported on the go-git backend — re-implementing every git diff
// CLI shorthand (HEAD, A..B, A...B, --merge-base, etc.) by hand
// would balloon the surface area and drift. DispatchRepo falls back
// to the CLI here.
func (g *GoGitRepo) Diff([]string) (Diff, error) { return Diff{}, ErrUnsupported }
