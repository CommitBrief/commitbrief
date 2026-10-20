// SPDX-License-Identifier: GPL-3.0-or-later

package git

import "log/slog"

// DispatchRepo is the Repo the CLI layer holds. `exec git` (CLIRepo) is the
// only diff backend (ADR-0002 §Update 2026-10-20, second entry). When no `git`
// binary is on PATH the repo still opens — commands that never ask for a diff
// keep working — and every diff method reports ErrNoGitCLI.
type DispatchRepo struct {
	root   string
	cli    *CLIRepo // nil when no `git` binary is on PATH
	logger *slog.Logger
}

type DispatchOptions struct {
	Logger *slog.Logger
}

// Open resolves the repository root enclosing root (ErrNotARepo when there is
// none) and binds the `git` CLI backend when the binary is available.
func Open(root string, opts DispatchOptions) (*DispatchRepo, error) {
	detected, err := FindRepo(root)
	if err != nil {
		return nil, err
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// NewCLIRepo fails only with ErrNoGitCLI. Keep the field a nil
	// *CLIRepo in that case (never a typed-nil behind an interface).
	cli, cliErr := NewCLIRepo(detected)
	if cliErr != nil {
		logger.Debug("git: git binary unavailable; diff operations will fail", "err", cliErr)
	}

	return &DispatchRepo{root: detected, cli: cli, logger: logger}, nil
}

func (d *DispatchRepo) Root() string { return d.root }

func (d *DispatchRepo) StagedDiff() (Diff, error) {
	if d.cli == nil {
		return Diff{}, ErrNoGitCLI
	}
	return d.cli.StagedDiff()
}

func (d *DispatchRepo) UnstagedDiff() (Diff, error) {
	if d.cli == nil {
		return Diff{}, ErrNoGitCLI
	}
	return d.cli.UnstagedDiff()
}

func (d *DispatchRepo) Diff(args []string) (Diff, error) {
	if d.cli == nil {
		return Diff{}, ErrNoGitCLI
	}
	return d.cli.Diff(args)
}
