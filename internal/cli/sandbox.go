// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"text/template"
	"time"

	"github.com/CommitBrief/commitbrief/internal/flaky"
)

// sandboxRerunTimeout bounds ONE rerun attempt. A hung test must cost one
// attempt, not the whole review, and flaky.Rerun already treats a timed-out
// attempt as unobserved rather than as a failure. Deliberately a constant: a
// config knob for it is unjustified until someone asks (ADR-0033).
const sandboxRerunTimeout = 2 * time.Minute

// sandboxRunner is the bound rerun capability for one review.
type sandboxRunner struct {
	exec      flaky.Executor
	needsTest bool   // the command template references {{.Test}}
	display   string // the un-rendered argv, for the "we are running this" notice
}

// parseSandboxCommand compiles each argv element as a text/template and reports
// whether any of them needs a resolved test name. A malformed element is an
// error here — at config-read time, before any provider call — so a typo
// surfaces loudly instead of silently disabling the feature.
func parseSandboxCommand(spec []string) ([]*template.Template, bool, error) {
	if len(spec) == 0 {
		return nil, false, nil
	}
	tmpls := make([]*template.Template, 0, len(spec))
	needsTest := false
	for i, elem := range spec {
		t, err := template.New(fmt.Sprintf("sandbox_command[%d]", i)).Parse(elem)
		if err != nil {
			return nil, false, fmt.Errorf("sandbox_command[%d]: %w", i, err)
		}
		if strings.Contains(elem, ".Test") {
			needsTest = true
		}
		tmpls = append(tmpls, t)
	}
	return tmpls, needsTest, nil
}

// renderSandboxArgv renders the compiled templates against one target. An
// element that renders empty is an error: it would shift the argv and run a
// different command than the user declared.
func renderSandboxArgv(tmpls []*template.Template, t flaky.Target) ([]string, error) {
	argv := make([]string, 0, len(tmpls))
	for i, tmpl := range tmpls {
		var sb strings.Builder
		if err := tmpl.Execute(&sb, t); err != nil {
			return nil, fmt.Errorf("sandbox_command[%d]: %w", i, err)
		}
		s := sb.String()
		if strings.TrimSpace(s) == "" {
			return nil, fmt.Errorf("sandbox_command[%d]: rendered empty", i)
		}
		argv = append(argv, s)
	}
	return argv, nil
}

// newSandboxExecutor returns the Executor that actually spawns the configured
// command, once per attempt, from repoRoot.
//
// Arguments go to exec.CommandContext as argv — there is no shell, so nothing
// in a rendered element can be reinterpreted as a command separator
// (engineering/standards/security.md).
//
// Exit 0 is a pass; a non-zero exit is a FAIL, not an error — that is the
// signal the whole feature is built on. Only a process that could not run at
// all (missing binary, spawn failure, timeout) is an error, which flaky.Rerun
// tallies as unobserved.
func newSandboxExecutor(repoRoot string, tmpls []*template.Template) flaky.Executor {
	return func(ctx context.Context, t flaky.Target) (bool, error) {
		argv, err := renderSandboxArgv(tmpls, t)
		if err != nil {
			return false, err
		}
		runCtx, cancel := context.WithTimeout(ctx, sandboxRerunTimeout)
		defer cancel()

		cmd := exec.CommandContext(runCtx, argv[0], argv[1:]...) //nolint:gosec // G204: argv is the user's own declared command (ADR-0033)
		cmd.Dir = repoRoot
		if runErr := cmd.Run(); runErr != nil {
			var ee *exec.ExitError
			if errors.As(runErr, &ee) {
				return false, nil // ran and failed: a real observation
			}
			return false, runErr // could not be observed at all
		}
		return true, nil
	}
}

// buildSandboxRunner binds the runner for this review, or returns (nil, nil)
// when review.sandbox_command is empty — the default, which keeps sandbox
// rerun inert even with --sandbox-rerun set. A package var so tests can
// substitute a runner without spawning processes.
var buildSandboxRunner = func(app *appContext) (*sandboxRunner, error) {
	spec := app.Config.Review.SandboxCommand
	tmpls, needsTest, err := parseSandboxCommand(spec)
	if err != nil {
		return nil, err
	}
	if tmpls == nil {
		return nil, nil
	}
	return &sandboxRunner{
		exec:      newSandboxExecutor(app.RepoRoot, tmpls),
		needsTest: needsTest,
		display:   strings.Join(spec, " "),
	}, nil
}
