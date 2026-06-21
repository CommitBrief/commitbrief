// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/render"
)

// scriptedExecutor returns an Executor that yields the scripted (passed, err)
// pairs in order, one per call, recording how many times it was invoked. It
// spawns no process and never sleeps, so the orchestration is exercised
// deterministically. Calls beyond the script panic, which surfaces an
// early-exit regression (we must not call exec more than the script allows).
func scriptedExecutor(t *testing.T, calls *int, script ...struct {
	passed bool
	err    error
}) Executor {
	t.Helper()
	return func(_ context.Context, _ string) (bool, error) {
		i := *calls
		*calls++
		if i >= len(script) {
			t.Fatalf("executor called %d times, script only has %d entries", i+1, len(script))
		}
		return script[i].passed, script[i].err
	}
}

type step struct {
	passed bool
	err    error
}

func TestRerun_MixedIsFlaky(t *testing.T) {
	// A pass then a fail across reruns confirms flakiness, and the early exit
	// stops the loop the moment both have been seen — only 2 of 5 calls.
	calls := 0
	exec := scriptedExecutor(t, &calls,
		step{passed: true},
		step{passed: false},
		step{passed: true}, // must NOT be reached (early exit)
	)
	res := Rerun(context.Background(), exec, "pkg.TestX", 5)
	if res.Verdict != VerdictFlaky {
		t.Fatalf("verdict = %q, want %q", res.Verdict, VerdictFlaky)
	}
	if !res.Confirmed() {
		t.Errorf("Confirmed() = false, want true for a flaky verdict")
	}
	if calls != 2 {
		t.Errorf("early exit: exec called %d times, want 2 (pass+fail is conclusive)", calls)
	}
	if res.Runs != 2 || res.Passes != 1 || res.Fails != 1 {
		t.Errorf("runs/passes/fails = %d/%d/%d, want 2/1/1", res.Runs, res.Passes, res.Fails)
	}
}

func TestRerun_AllFailIsRealFailure(t *testing.T) {
	// Every rerun fails deterministically: a genuine red test, NOT flaky.
	calls := 0
	exec := scriptedExecutor(t, &calls,
		step{passed: false}, step{passed: false}, step{passed: false},
	)
	res := Rerun(context.Background(), exec, "pkg.TestX", 3)
	if res.Verdict != VerdictRealFailure {
		t.Fatalf("verdict = %q, want %q", res.Verdict, VerdictRealFailure)
	}
	if res.Confirmed() {
		t.Errorf("Confirmed() = true, want false for a real failure (must not quarantine)")
	}
	if res.Runs != 3 || res.Fails != 3 || res.Passes != 0 {
		t.Errorf("runs/passes/fails = %d/%d/%d, want 3/0/3", res.Runs, res.Passes, res.Fails)
	}
}

func TestRerun_AllPassIsTransient(t *testing.T) {
	// Every rerun passes: the flake did not reproduce — transient / resolved.
	calls := 0
	exec := scriptedExecutor(t, &calls,
		step{passed: true}, step{passed: true}, step{passed: true}, step{passed: true},
	)
	res := Rerun(context.Background(), exec, "pkg.TestX", 4)
	if res.Verdict != VerdictTransient {
		t.Fatalf("verdict = %q, want %q", res.Verdict, VerdictTransient)
	}
	if res.Runs != 4 || res.Passes != 4 || res.Fails != 0 {
		t.Errorf("runs/passes/fails = %d/%d/%d, want 4/4/0", res.Runs, res.Passes, res.Fails)
	}
}

func TestRerun_NRespected(t *testing.T) {
	// With a stable (all-pass) test and no early exit, exec is called exactly N
	// times — the orchestrator honours the requested rerun count.
	for _, n := range []int{1, 3, 7} {
		calls := 0
		exec := func(_ context.Context, _ string) (bool, error) { calls++; return true, nil }
		res := Rerun(context.Background(), exec, "pkg.TestX", n)
		if calls != n {
			t.Errorf("N=%d: exec called %d times, want %d", n, calls, n)
		}
		if res.Runs != n {
			t.Errorf("N=%d: res.Runs = %d, want %d", n, res.Runs, n)
		}
		if res.Verdict != VerdictTransient {
			t.Errorf("N=%d: verdict = %q, want transient", n, res.Verdict)
		}
	}
}

func TestRerun_ZeroOrNegativeIsInconclusiveNoCalls(t *testing.T) {
	for _, n := range []int{0, -1} {
		calls := 0
		exec := func(_ context.Context, _ string) (bool, error) { calls++; return true, nil }
		res := Rerun(context.Background(), exec, "pkg.TestX", n)
		if calls != 0 {
			t.Errorf("N=%d: exec called %d times, want 0", n, calls)
		}
		if res.Verdict != VerdictInconclusive {
			t.Errorf("N=%d: verdict = %q, want inconclusive", n, res.Verdict)
		}
		if res.Confirmed() {
			t.Errorf("N=%d: Confirmed() = true, want false", n)
		}
	}
}

func TestRerun_NilExecutorIsInconclusive(t *testing.T) {
	// An unbound seam must be safe to call and yield no classification.
	res := Rerun(context.Background(), nil, "pkg.TestX", 5)
	if res.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %q, want inconclusive for a nil executor", res.Verdict)
	}
	if res.Runs != 0 {
		t.Errorf("res.Runs = %d, want 0", res.Runs)
	}
}

func TestRerun_ExecutorErrorHandled(t *testing.T) {
	// An errored attempt is neither a pass nor a fail: it is recorded in Errors
	// and never short-circuits the loop. A campaign that errors on every
	// attempt has nothing to classify and is Inconclusive.
	calls := 0
	exec := scriptedExecutor(t, &calls,
		step{err: errors.New("could not launch runner")},
		step{err: errors.New("compile error")},
		step{err: errors.New("timeout")},
	)
	res := Rerun(context.Background(), exec, "pkg.TestX", 3)
	if res.Verdict != VerdictInconclusive {
		t.Fatalf("verdict = %q, want inconclusive when every attempt errors", res.Verdict)
	}
	if res.Errors != 3 || res.Passes != 0 || res.Fails != 0 {
		t.Errorf("errors/passes/fails = %d/%d/%d, want 3/0/0", res.Errors, res.Passes, res.Fails)
	}
	if res.Runs != 3 {
		t.Errorf("res.Runs = %d, want 3 (all attempts were made)", res.Runs)
	}
}

func TestRerun_ErrorsDoNotMaskAFlake(t *testing.T) {
	// A flickering runner (some errors) interleaved with a real pass and fail
	// still classifies as flaky once both outcomes are observed; the errors are
	// counted but never block the conclusion.
	calls := 0
	exec := scriptedExecutor(t, &calls,
		step{err: errors.New("transient launch error")},
		step{passed: true},
		step{passed: false},
		step{passed: true}, // must NOT be reached (flaky is conclusive at call 3)
	)
	res := Rerun(context.Background(), exec, "pkg.TestX", 6)
	if res.Verdict != VerdictFlaky {
		t.Fatalf("verdict = %q, want flaky", res.Verdict)
	}
	if calls != 3 {
		t.Errorf("exec called %d times, want 3 (early exit after pass+fail)", calls)
	}
	if res.Errors != 1 || res.Passes != 1 || res.Fails != 1 {
		t.Errorf("errors/passes/fails = %d/%d/%d, want 1/1/1", res.Errors, res.Passes, res.Fails)
	}
}

func TestRerun_CancelledContextStops(t *testing.T) {
	// A cancelled context aborts before any rerun; whatever was observed (none)
	// classifies as inconclusive.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	exec := func(_ context.Context, _ string) (bool, error) { calls++; return true, nil }
	res := Rerun(ctx, exec, "pkg.TestX", 5)
	if calls != 0 {
		t.Errorf("exec called %d times after cancel, want 0", calls)
	}
	if res.Verdict != VerdictInconclusive {
		t.Errorf("verdict = %q, want inconclusive", res.Verdict)
	}
}

func TestAnnotate_FlakyAppendsLineKeepsSeverity(t *testing.T) {
	cat := loadCatalog(t)
	f := render.Finding{
		Severity:   render.SeverityMedium,
		Title:      "Test uses a hard-coded sleep for synchronization",
		Suggestion: "Use a condition-based wait.",
	}
	got := Annotate(cat, f, RerunResult{Verdict: VerdictFlaky, Runs: 4})
	if got.Severity != render.SeverityMedium {
		t.Errorf("flaky should keep severity, got %q", got.Severity)
	}
	if !strings.Contains(got.Suggestion, "Use a condition-based wait.") {
		t.Errorf("original suggestion dropped: %q", got.Suggestion)
	}
	if !strings.Contains(strings.ToLower(got.Suggestion), "confirmed flaky") {
		t.Errorf("verdict line not appended: %q", got.Suggestion)
	}
	if strings.HasPrefix(got.Suggestion, "flaky.sandbox.") {
		t.Errorf("verdict line not localized (raw key): %q", got.Suggestion)
	}
}

func TestAnnotate_TransientDemotesToInfo(t *testing.T) {
	cat := loadCatalog(t)
	f := render.Finding{Severity: render.SeverityMedium, Suggestion: "Fix it."}
	got := Annotate(cat, f, RerunResult{Verdict: VerdictTransient, Runs: 5})
	if got.Severity != render.SeverityInfo {
		t.Errorf("transient should demote to info so it doesn't gate a commit, got %q", got.Severity)
	}
	if !strings.Contains(strings.ToLower(got.Suggestion), "did not reproduce") {
		t.Errorf("transient verdict line missing: %q", got.Suggestion)
	}
}

func TestAnnotate_RealFailureRelabelsKeepsSeverity(t *testing.T) {
	cat := loadCatalog(t)
	f := render.Finding{Severity: render.SeverityMedium, Suggestion: "Fix it."}
	got := Annotate(cat, f, RerunResult{Verdict: VerdictRealFailure, Runs: 3})
	if got.Severity != render.SeverityMedium {
		t.Errorf("real failure should keep severity, got %q", got.Severity)
	}
	if !strings.Contains(strings.ToLower(got.Suggestion), "real failure") {
		t.Errorf("real-failure verdict line missing: %q", got.Suggestion)
	}
}

func TestAnnotate_InconclusiveLeavesFindingUntouched(t *testing.T) {
	cat := loadCatalog(t)
	f := render.Finding{Severity: render.SeverityMedium, Suggestion: "Fix it."}
	got := Annotate(cat, f, RerunResult{Verdict: VerdictInconclusive})
	if got.Severity != f.Severity || got.Suggestion != f.Suggestion {
		t.Errorf("inconclusive should leave the finding unchanged, got %+v", got)
	}
}

func TestAnnotate_EmptySuggestionGetsVerdictAlone(t *testing.T) {
	cat := loadCatalog(t)
	f := render.Finding{Severity: render.SeverityLow}
	got := Annotate(cat, f, RerunResult{Verdict: VerdictFlaky, Runs: 2})
	if got.Suggestion == "" || strings.HasPrefix(got.Suggestion, " ") {
		t.Errorf("verdict should become the suggestion with no leading space, got %q", got.Suggestion)
	}
}
