package cli

import (
	"github.com/spf13/cobra"
)

// newDiffCmd is the `commitbrief diff <args...>` review entry point.
// It mirrors `git diff`'s positional-arg surface so the user can review
// arbitrary historic ranges with the same muscle memory:
//
//	commitbrief diff HEAD                 → git diff HEAD
//	commitbrief diff HEAD~3 HEAD          → git diff HEAD~3 HEAD
//	commitbrief diff main feature         → git diff main feature
//	commitbrief diff main...feature       → git diff main...feature
//
// Under the hood the args are forwarded verbatim to `git diff
// --no-color --no-ext-diff <args>` via the DispatchRepo's `Diff`
// method, then the resulting unified-diff feeds the same review
// pipeline as `--staged` / `--unstaged`.
//
// The `--file` / `--dir` global flags layer on top — they still
// narrow the post-parse file set, so e.g. `commitbrief diff HEAD~3
// HEAD --dir database/seeder` reviews only seeder changes in the
// last three commits.
//
// This replaces the v0.x scope flags `--commit`, `--branch`, and
// `--pull-request`; all three reduce to one or two positional args
// here. The merge-commit warning that previously fired on `--commit
// <merge-hash>` is no longer emitted — `git diff <hash>` semantics
// are well-known to anyone who already knows `git diff`.
func newDiffCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "diff <args>...",
		Short: "Run a review against an arbitrary git diff (passthrough)",
		Long: "Review the output of `git diff <args>`. Arguments are forwarded " +
			"verbatim to git, so any ref combination git understands works: " +
			"HEAD, HEAD~3 HEAD, main feature, main...feature, etc. " +
			"`--file` and `--dir` global filters apply on top.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runReview(cmd, reviewScopeFlags{}, args)
		},
	}
}
