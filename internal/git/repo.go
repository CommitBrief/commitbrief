// SPDX-License-Identifier: GPL-3.0-or-later

package git

import "errors"

type Origin string

const (
	OriginStaged   Origin = "staged"
	OriginUnstaged Origin = "unstaged"
	OriginCommit   Origin = "commit"
	OriginDiff     Origin = "diff" // `commitbrief diff <args...>` passthrough
	// OriginFiltered marks a diff assembled from an explicitly selected set
	// of commits (the --author / --start-date / --end-date / --text filters).
	// Unlike every other origin its content is a CONCATENATION of per-commit
	// patches, so the same path may appear as several file entries.
	OriginFiltered Origin = "filtered"
)

type Diff struct {
	Content string
	Origin  Origin
	Args    map[string]string
}

func (d Diff) Empty() bool { return d.Content == "" }

type Repo interface {
	StagedDiff() (Diff, error)
	UnstagedDiff() (Diff, error)
	// Diff is the generic `git diff <args>` passthrough used by the
	// `commitbrief diff` subcommand. args are forwarded verbatim
	// after `--no-color --no-ext-diff` so the renderer/parser see
	// stable unified-diff output. Without a `git` binary on PATH the
	// implementation returns ErrNoGitCLI.
	Diff(args []string) (Diff, error)
	Root() string
}

var (
	ErrNotARepo = errors.New("git: not inside a git repository")
	ErrNoGitCLI = errors.New("git: `git` binary not found on PATH")
)
