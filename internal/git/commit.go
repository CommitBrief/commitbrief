// SPDX-License-Identifier: GPL-3.0-or-later

package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Commit creates a commit from the currently staged changes with the given
// message, returning git's summary line(s) (e.g. "[main abc1234] feat: …").
//
// This is the one place the tool writes to git (ADR-0019, superseding
// ADR-0015 §6's read-only stance for the authoring path). It is reached only
// after the `commit` command has the user's explicit confirmation (or --yes).
//
// The message is fed via stdin (`git commit -F -`) rather than `-m` so that
// multi-line bodies and arbitrary content commit verbatim without any
// shell-quoting or newline pitfalls, and so the behaviour is identical
// across platforms. Commit hooks run as usual (no --no-verify) — a failing
// pre-commit hook surfaces as an error here.
func Commit(ctx context.Context, repoRoot, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("git: refusing to commit an empty message")
	}
	bin, err := exec.LookPath("git")
	if err != nil {
		return "", ErrNoGitCLI
	}
	cmd := exec.CommandContext(ctx, bin, "commit", "-F", "-")
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(message)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git commit: %s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}
