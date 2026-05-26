package git

import "errors"

type Origin string

const (
	OriginStaged   Origin = "staged"
	OriginUnstaged Origin = "unstaged"
	OriginFile     Origin = "file"
	OriginCommit   Origin = "commit"
	OriginRange    Origin = "range"
	OriginBranch   Origin = "branch"
)

type Diff struct {
	Content string
	Origin  Origin
	Args    map[string]string
	// IsMerge is set by CommitDiff when the requested commit has 2+ parents.
	// The diff content itself is always against the first parent (OQ-03 (b));
	// callers use this flag to emit a user-visible warning about combined-diff
	// limitations and suggest --pull-request for full branch comparison.
	IsMerge bool
}

func (d Diff) Empty() bool { return d.Content == "" }

type Repo interface {
	StagedDiff() (Diff, error)
	UnstagedDiff() (Diff, error)
	FileDiff(path string) (Diff, error)
	CommitDiff(hash string) (Diff, error)
	RangeDiff(target, feature string) (Diff, error)
	BranchDiff(target string) (Diff, error)
	Root() string
}

var (
	ErrUnsupported = errors.New("git: operation not supported on this backend")
	ErrNotARepo    = errors.New("git: not inside a git repository")
	ErrNoGitCLI    = errors.New("git: `git` binary not on PATH and go-git fallback unavailable")
)
