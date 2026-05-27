// SPDX-License-Identifier: GPL-3.0-or-later

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
	out, err := r.run("show", "--format=", "--no-color", "--no-ext-diff", hash)
	if err != nil {
		return Diff{}, err
	}
	return Diff{
		Content: out,
		Origin:  OriginCommit,
		Args:    map[string]string{"hash": hash},
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

// Diff is the generic `git diff <args>` passthrough used by the
// `commitbrief diff` subcommand. We always inject `--no-color` and
// `--no-ext-diff` so the parser/renderer pipeline sees the same
// stable unified-diff shape the other Diff*() helpers produce.
// The caller is responsible for validating args (e.g. requiring at
// least one positional ref) — empty args yields `git diff` (i.e.
// unstaged), which is identical to UnstagedDiff() and discouraged.
func (r *CLIRepo) Diff(args []string) (Diff, error) {
	full := append([]string{"diff", "--no-color", "--no-ext-diff"}, args...)
	out, err := r.run(full...)
	if err != nil {
		return Diff{}, err
	}
	// Surface the user's args verbatim for renderers / cache-key debug
	// output. We don't try to detect merge semantics here — `git diff
	// HEAD~3 HEAD` and similar ranges don't have a single "the commit"
	// to inspect.
	return Diff{
		Content: out,
		Origin:  OriginDiff,
		Args:    map[string]string{"args": strings.Join(args, " ")},
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
