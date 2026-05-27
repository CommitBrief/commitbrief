// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/guard"
)

func TestHandleSecretMatchesYesDoesNotBypass(t *testing.T) {
	// UC-01 regression guard. --yes used to silently approve any
	// flagged credential; that behaviour is gone — only --allow-secrets
	// bypasses the scanner. With --yes set and a non-TTY stdin the
	// handler must still abort, and the legacy "bypassed by --yes"
	// catalog line must no longer appear.
	resetGlobalFlags(t)
	global.yes = true
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)

	abort := handleSecretMatches(cmd, app, []guard.SecretMatch{
		{Line: 12, Patterns: []string{"AWS Access Key"}},
	})
	if !abort {
		t.Errorf("--yes must not bypass secret scanner; got abort=false")
	}
	out := errBuf.String()
	if strings.Contains(out, "bypassed by --yes") || strings.Contains(out, "--yes ile atlandı") {
		t.Errorf("legacy --yes bypass line should be gone; got stderr:\n%s", out)
	}
	if !strings.Contains(out, "AWS Access Key") {
		t.Errorf("detected pattern label should still surface; got:\n%s", out)
	}
}
