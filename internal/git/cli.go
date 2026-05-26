package git

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

type CLIRepo struct {
	root   string
	gitBin string
}

func NewCLIRepo(root string) (*CLIRepo, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, ErrNoGitCLI
	}
	return &CLIRepo{root: root, gitBin: bin}, nil
}

func (r *CLIRepo) Root() string { return r.root }

func (r *CLIRepo) StagedDiff() (Diff, error) {
	out, err := r.run("diff", "--staged", "--no-color", "--no-ext-diff")
	if err != nil {
		return Diff{}, err
	}
	return Diff{Content: out, Origin: OriginStaged}, nil
}

func (r *CLIRepo) UnstagedDiff() (Diff, error) {
	out, err := r.run("diff", "--no-color", "--no-ext-diff")
	if err != nil {
		return Diff{}, err
	}
	return Diff{Content: out, Origin: OriginUnstaged}, nil
}

func (r *CLIRepo) FileDiff(path string) (Diff, error) {
	if path == "" {
		return Diff{}, errors.New("git: FileDiff requires a path")
	}
	out, err := r.run("diff", "HEAD", "--no-color", "--no-ext-diff", "--", path)
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Content: out,
		Origin:  OriginFile,
		Args:    map[string]string{"path": path},
	}, nil
}

func (r *CLIRepo) CommitDiff(hash string) (Diff, error) {
	if hash == "" {
		return Diff{}, errors.New("git: CommitDiff requires a commit hash")
	}
	// `rev-list --parents -n 1 <hash>` emits "<hash> <p1> [<p2> ...]"; 2+ parents
	// means a merge commit. The dispatcher routes merge commits through go-git
	// in practice (CLI fallback only fires on ErrUnsupported, which CommitDiff
	// returns only for initial commits), but we set IsMerge here too so direct
	// CLIRepo usage stays consistent with the GoGitRepo behavior.
	parents, err := r.run("rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return Diff{}, err
	}
	isMerge := len(strings.Fields(parents)) > 2
	out, err := r.run("show", "--format=", "--no-color", "--no-ext-diff", hash)
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Content: out,
		Origin:  OriginCommit,
		Args:    map[string]string{"hash": hash},
		IsMerge: isMerge,
	}, nil
}

func (r *CLIRepo) RangeDiff(target, feature string) (Diff, error) {
	if target == "" || feature == "" {
		return Diff{}, errors.New("git: RangeDiff requires target and feature refs")
	}
	out, err := r.run("diff", "--no-color", "--no-ext-diff", fmt.Sprintf("%s...%s", target, feature))
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Content: out,
		Origin:  OriginRange,
		Args:    map[string]string{"target": target, "feature": feature},
	}, nil
}

func (r *CLIRepo) BranchDiff(target string) (Diff, error) {
	if target == "" {
		return Diff{}, errors.New("git: BranchDiff requires a target ref")
	}
	out, err := r.run("diff", "--no-color", "--no-ext-diff", target)
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Content: out,
		Origin:  OriginBranch,
		Args:    map[string]string{"target": target},
	}, nil
}

func (r *CLIRepo) run(args ...string) (string, error) {
	cmd := exec.Command(r.gitBin, args...)
	cmd.Dir = r.root
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
