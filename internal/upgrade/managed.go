// SPDX-License-Identifier: GPL-3.0-or-later

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
)

// ModulePath is the `go install` target for CommitBrief.
const ModulePath = "github.com/CommitBrief/commitbrief/cmd/commitbrief"

// ErrToolMissing means the package manager that owns this install is
// not on PATH, so the upgrade cannot be delegated.
var ErrToolMissing = errors.New("package manager not found on PATH")

// Command returns the argv that upgrades a package-managed install.
// It returns nil for MethodManual, which is handled by InstallManual.
//
// Delegating rather than overwriting the file is the core decision of
// ADR-0034 §D1: replacing a brew- or scoop-owned binary desynchronizes
// the manager's metadata, and its next upgrade either conflicts or
// silently reverts the change.
func Command(m Method) []string {
	switch m {
	case MethodHomebrew:
		return []string{"brew", "upgrade", "commitbrief"}
	case MethodScoop:
		return []string{"scoop", "update", "commitbrief"}
	case MethodGoInstall:
		return []string{"go", "install", ModulePath + "@latest"}
	default:
		return nil
	}
}

// Run executes argv with its output streamed straight through to the
// user. The delegated tool's output is never parsed, so an upstream
// format change cannot break CommitBrief.
//
// argv is passed to exec directly — no shell is involved, per the
// argv-not-shell rule in the engineering standards.
func Run(ctx context.Context, argv []string, out, errOut io.Writer) error {
	if len(argv) == 0 {
		return errors.New("upgrade: empty command")
	}
	if _, err := exec.LookPath(argv[0]); err != nil {
		return fmt.Errorf("%w: %s", ErrToolMissing, argv[0])
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = out
	cmd.Stderr = errOut
	return cmd.Run()
}
