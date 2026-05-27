// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"bufio"
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/i18n"
)

// emptyStdin returns a *bufio.Reader over an empty buffer — used to
// satisfy the UC-21 shared-stdin parameter when the test exercises a
// code path that never reaches a Read.
func emptyStdin() *bufio.Reader {
	return bufio.NewReader(&bytes.Buffer{})
}

// stubCmd builds a minimal *cobra.Command with stdout/stderr buffers so
// handleCostPreflight can be exercised without spinning up the full CLI.
// Returns the command and the stderr buffer (the only sink the preflight
// writes to in practice).
func stubCmd(t *testing.T) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	cmd := &cobra.Command{}
	var errBuf bytes.Buffer
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&errBuf)
	return cmd, &errBuf
}

// stubApp wires an appContext with the supplied cost threshold and a
// real i18n catalog so message keys resolve to their EN templates.
func stubApp(t *testing.T, threshold float64) *appContext {
	t.Helper()
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Cost.WarnThresholdUSD = threshold
	return &appContext{Config: cfg, Catalog: cat}
}

// resetGlobalFlags is the same trick newCLIEnv uses — global is a
// package-level var so per-test state must be reset to avoid bleed.
func resetGlobalFlags(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { global = globalFlags{color: "never"} })
	global = globalFlags{color: "never"}
}

func TestHandleCostPreflightBelowThresholdSilent(t *testing.T) {
	resetGlobalFlags(t)
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0.50)

	abort := handleCostPreflight(cmd, app, 0.10, emptyStdin()) // well below 0.50
	if abort {
		t.Errorf("below-threshold cost should not abort")
	}
	if got := errBuf.String(); got != "" {
		t.Errorf("preflight under threshold should be silent; got stderr:\n%s", got)
	}
}

func TestHandleCostPreflightDisabledThresholdSilent(t *testing.T) {
	// Threshold == 0 disables the check entirely — even a hypothetical
	// $100 cost shouldn't trigger anything.
	resetGlobalFlags(t)
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0)

	if handleCostPreflight(cmd, app, 100, emptyStdin()) {
		t.Errorf("threshold=0 should disable preflight; got abort=true")
	}
	if errBuf.Len() > 0 {
		t.Errorf("disabled preflight must not write to stderr; got:\n%s", errBuf.String())
	}
}

func TestHandleCostPreflightYesDoesNotBypass(t *testing.T) {
	// UC-06 regression guard. Setting --yes used to silently approve
	// any above-threshold cost; that behaviour is gone — --yes only
	// auto-answers the guard prompt now. In a non-TTY context with
	// --yes, the preflight must still abort and must NOT emit any
	// "bypassed by --yes" line (the catalog key was removed).
	resetGlobalFlags(t)
	global.yes = true
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0.10)

	abort := handleCostPreflight(cmd, app, 0.50, emptyStdin())
	if !abort {
		t.Errorf("--yes must not bypass cost preflight; got abort=false")
	}
	out := errBuf.String()
	if strings.Contains(out, "bypassed by --yes") {
		t.Errorf("legacy --yes bypass line should be gone; got stderr:\n%s", out)
	}
	if !strings.Contains(out, "non-interactive") {
		t.Errorf("non-TTY abort message should still surface; got:\n%s", out)
	}
}

func TestHandleCostPreflightNonTTYAborts(t *testing.T) {
	// stdin in tests is a *bytes.Buffer (not a TTY), so
	// ui.IsStdinTTY(os.Stdin) returns false — the non-interactive
	// abort path triggers.
	resetGlobalFlags(t)
	cmd, errBuf := stubCmd(t)
	app := stubApp(t, 0.10)

	abort := handleCostPreflight(cmd, app, 0.50, emptyStdin())
	if !abort {
		t.Errorf("above-threshold + non-TTY should abort; got abort=false")
	}
	out := errBuf.String()
	if !strings.Contains(out, "Estimated cost") {
		t.Errorf("abort message should include the cost estimate line; got:\n%s", out)
	}
	if !strings.Contains(out, "non-interactive") {
		t.Errorf("abort message should reference non-interactive mode; got:\n%s", out)
	}
}

func TestEstimateOutputTokensFloorsAndCaps(t *testing.T) {
	cases := []struct {
		in   int
		want int
	}{
		{0, 200},      // floor
		{100, 200},    // floor (100/4 = 25, below floor)
		{800, 200},    // boundary (800/4 = 200)
		{1000, 250},   // mid range
		{6000, 1500},  // boundary (6000/4 = 1500)
		{10000, 1500}, // cap
	}
	for _, tc := range cases {
		if got := estimateOutputTokens(tc.in); got != tc.want {
			t.Errorf("estimateOutputTokens(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestEstimateOutputTokensMonotonic(t *testing.T) {
	// The heuristic should be non-decreasing across the input range
	// (or at minimum: not jump downward). Catches a regression where
	// the floor/cap clamping accidentally inverts the curve.
	prev := -1
	for _, in := range []int{0, 100, 500, 1000, 2000, 5000, 10000, 100000} {
		got := estimateOutputTokens(in)
		if got < prev {
			t.Errorf("non-monotonic: estimateOutputTokens(%d) = %d < prev %d", in, got, prev)
		}
		prev = got
	}
}
