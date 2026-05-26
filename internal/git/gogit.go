package git

import (
	"errors"
	"fmt"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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
func (g *GoGitRepo) FileDiff(string) (Diff, error) {
	return Diff{}, ErrUnsupported
}

func (g *GoGitRepo) CommitDiff(hash string) (Diff, error) {
	if hash == "" {
		return Diff{}, errors.New("git: CommitDiff requires a commit hash")
	}
	resolved, err := g.repo.ResolveRevision(plumbing.Revision(hash))
	if err != nil {
		return Diff{}, fmt.Errorf("git: resolve %s: %w", hash, err)
	}
	commit, err := g.repo.CommitObject(*resolved)
	if err != nil {
		return Diff{}, fmt.Errorf("git: commit %s: %w", hash, err)
	}
	content, err := patchAgainstParent(commit)
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Content: content,
		Origin:  OriginCommit,
		Args:    map[string]string{"hash": hash},
	}, nil
}

func (g *GoGitRepo) RangeDiff(target, feature string) (Diff, error) {
	if target == "" || feature == "" {
		return Diff{}, errors.New("git: RangeDiff requires target and feature refs")
	}
	targetCommit, err := resolveCommit(g.repo, target)
	if err != nil {
		return Diff{}, err
	}
	featureCommit, err := resolveCommit(g.repo, feature)
	if err != nil {
		return Diff{}, err
	}
	base, err := mergeBase(targetCommit, featureCommit)
	if err != nil {
		// Fallback to direct diff; matches `git diff target feature` rather than
		// the three-dot form, but is the best we can do without a full graph walk.
		return Diff{}, ErrUnsupported
	}
	patch, err := base.Patch(featureCommit)
	if err != nil {
		return Diff{}, fmt.Errorf("git: patch range: %w", err)
	}
	return Diff{
		Content: patch.String(),
		Origin:  OriginRange,
		Args:    map[string]string{"target": target, "feature": feature},
	}, nil
}

func (g *GoGitRepo) BranchDiff(target string) (Diff, error) {
	if target == "" {
		return Diff{}, errors.New("git: BranchDiff requires a target ref")
	}
	targetCommit, err := resolveCommit(g.repo, target)
	if err != nil {
		return Diff{}, err
	}
	head, err := g.repo.Head()
	if err != nil {
		return Diff{}, fmt.Errorf("git: HEAD: %w", err)
	}
	headCommit, err := g.repo.CommitObject(head.Hash())
	if err != nil {
		return Diff{}, fmt.Errorf("git: head commit: %w", err)
	}
	patch, err := targetCommit.Patch(headCommit)
	if err != nil {
		return Diff{}, fmt.Errorf("git: patch branch: %w", err)
	}
	return Diff{
		Content: patch.String(),
		Origin:  OriginBranch,
		Args:    map[string]string{"target": target},
	}, nil
}

func resolveCommit(r *gogit.Repository, ref string) (*object.Commit, error) {
	hash, err := r.ResolveRevision(plumbing.Revision(ref))
	if err != nil {
		return nil, fmt.Errorf("git: resolve %s: %w", ref, err)
	}
	commit, err := r.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("git: commit %s: %w", ref, err)
	}
	return commit, nil
}

func patchAgainstParent(commit *object.Commit) (string, error) {
	if commit.NumParents() == 0 {
		// Initial commit needs a diff against an empty tree. go-git's Patch
		// API panics when the receiving Commit has no Storer, so we hand this
		// case to the CLI dispatcher.
		return "", ErrUnsupported
	}
	parent, err := commit.Parent(0)
	if err != nil {
		return "", fmt.Errorf("git: parent: %w", err)
	}
	patch, err := parent.Patch(commit)
	if err != nil {
		return "", fmt.Errorf("git: patch: %w", err)
	}
	return patch.String(), nil
}

func mergeBase(a, b *object.Commit) (*object.Commit, error) {
	bases, err := a.MergeBase(b)
	if err != nil {
		return nil, err
	}
	if len(bases) == 0 {
		return nil, errors.New("no merge base")
	}
	return bases[0], nil
}
