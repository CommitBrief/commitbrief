// SPDX-License-Identifier: GPL-3.0-or-later

// Package remote drives GitHub operations (currently PR review) through
// the user's `gh` CLI. It performs no HTTPS calls itself — `gh` handles
// auth, host resolution, and the REST round-trips. See ADR-0016.
package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Runner executes a `gh` invocation and returns its stdout. Production
// uses execRunner; tests inject a fake to avoid live network and to
// script sequential responses (race-retry, failure paths).
type Runner interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execRunner struct{}

// Run shells out to `gh` with the given args, surfacing stderr in the
// error so the caller can log a meaningful message.
func (execRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	out, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return out, fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(string(ee.Stderr)))
		}
		return out, fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}

// NewRunner returns the production Runner backed by the `gh` binary.
func NewRunner() Runner { return execRunner{} }

// EnsureGH reports ErrGHMissing when the `gh` binary is not on PATH.
func EnsureGH() error {
	if _, err := exec.LookPath("gh"); err != nil {
		return ErrGHMissing
	}
	return nil
}

// runJSON runs a gh command expected to emit JSON and decodes it into v.
func runJSON(ctx context.Context, r Runner, v any, args ...string) error {
	out, err := r.Run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("gh %s: decode json: %w", strings.Join(args, " "), err)
	}
	return nil
}
