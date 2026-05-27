// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"errors"
	"log/slog"
)

type DispatchRepo struct {
	primary  Repo
	fallback Repo
	logger   *slog.Logger
}

type DispatchOptions struct {
	Logger *slog.Logger
}

func Open(root string, opts DispatchOptions) (*DispatchRepo, error) {
	detected, err := FindRepo(root)
	if err != nil {
		return nil, err
	}
	root = detected

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	primary, primErr := NewGoGitRepo(root)
	if primErr != nil && !errors.Is(primErr, ErrNotARepo) {
		logger.Debug("git: go-git open failed", "err", primErr)
	}
	fallback, fbErr := NewCLIRepo(root)

	switch {
	case primary == nil && fallback == nil:
		if errors.Is(primErr, ErrNotARepo) {
			return nil, ErrNotARepo
		}
		if errors.Is(fbErr, ErrNoGitCLI) && primErr != nil {
			return nil, ErrNoGitCLI
		}
		return nil, errors.Join(primErr, fbErr)
	case primary == nil:
		logger.Debug("git: go-git unavailable; using CLI only", "err", primErr)
	case fallback == nil:
		logger.Debug("git: git binary unavailable; using go-git only", "err", fbErr)
	}

	return &DispatchRepo{
		primary:  primary,
		fallback: fallback,
		logger:   logger,
	}, nil
}

func (d *DispatchRepo) Root() string {
	if d.primary != nil {
		return d.primary.Root()
	}
	return d.fallback.Root()
}

func (d *DispatchRepo) StagedDiff() (Diff, error) {
	return d.dispatch("StagedDiff", func(r Repo) (Diff, error) { return r.StagedDiff() })
}

func (d *DispatchRepo) UnstagedDiff() (Diff, error) {
	return d.dispatch("UnstagedDiff", func(r Repo) (Diff, error) { return r.UnstagedDiff() })
}

func (d *DispatchRepo) FileDiff(path string) (Diff, error) {
	return d.dispatch("FileDiff", func(r Repo) (Diff, error) { return r.FileDiff(path) })
}

func (d *DispatchRepo) CommitDiff(hash string) (Diff, error) {
	return d.dispatch("CommitDiff", func(r Repo) (Diff, error) { return r.CommitDiff(hash) })
}

func (d *DispatchRepo) RangeDiff(target, feature string) (Diff, error) {
	return d.dispatch("RangeDiff", func(r Repo) (Diff, error) { return r.RangeDiff(target, feature) })
}

func (d *DispatchRepo) BranchDiff(target string) (Diff, error) {
	return d.dispatch("BranchDiff", func(r Repo) (Diff, error) { return r.BranchDiff(target) })
}

func (d *DispatchRepo) Diff(args []string) (Diff, error) {
	return d.dispatch("Diff", func(r Repo) (Diff, error) { return r.Diff(args) })
}

func (d *DispatchRepo) dispatch(op string, call func(Repo) (Diff, error)) (Diff, error) {
	if d.primary != nil {
		diff, err := call(d.primary)
		if err == nil {
			return diff, nil
		}
		if !errors.Is(err, ErrUnsupported) {
			d.logger.Debug("git: primary failed; falling back to CLI", "op", op, "err", err)
		}
		if d.fallback == nil {
			return Diff{}, err
		}
	}
	if d.fallback == nil {
		return Diff{}, ErrNoGitCLI
	}
	return call(d.fallback)
}
