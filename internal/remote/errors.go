// SPDX-License-Identifier: GPL-3.0-or-later

package remote

import "errors"

// Sentinel errors let the CLI layer map failures to i18n catalog keys
// without string-matching. Wrap with %w where extra context helps.
var (
	// ErrGHMissing — the `gh` binary is not on PATH.
	ErrGHMissing = errors.New("remote: gh binary not found on PATH")
	// ErrSelfPR — the authenticated user is the PR author (GitHub blocks
	// self-approval; we reject before fetching the diff).
	ErrSelfPR = errors.New("remote: cannot review your own pull request")
	// ErrTooVolatile — the PR head changed twice during review; aborted.
	ErrTooVolatile = errors.New("remote: pull request changed during review")
)
