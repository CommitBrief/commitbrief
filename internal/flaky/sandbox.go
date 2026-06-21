// SPDX-License-Identifier: GPL-3.0-or-later

package flaky

import (
	"context"

	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// Sandbox-rerun (ADR-0022 §Update 2026-06-21) raises confidence in a
// statically flagged flaky candidate by actually RE-RUNNING the test in
// isolation N times and classifying it by the observed pass/fail mix. The
// static rules infer flakiness from anti-patterns; a rerun confirms it
// empirically.
//
// This file ships only the orchestration: the N-rerun loop, the
// classification, and the optional early exit once a mixed result is proven.
// It deliberately does NOT embed a language-specific test runner. The caller
// binds an Executor (e.g. `go test -run`, `pytest -k`, `jest -t`) so the core
// stays pure, deterministic, and unit-testable with a fake — no process spawns
// or real sleeps in tests.

// Executor runs a single, already-isolated test once and reports whether it
// passed. The error channel is reserved for harness failures that are NOT a
// test result — the runner could not be launched, the context was cancelled,
// the test binary did not compile. A test that runs and fails its assertions
// is `passed == false, err == nil`; only an inability to OBSERVE a result is
// an error. testID is opaque to the orchestrator: it is whatever handle the
// caller's runner understands (a fully-qualified test name, a node id, …).
//
// Implementations must be safe to call repeatedly for the same testID — each
// call is one independent rerun.
type Executor func(ctx context.Context, testID string) (passed bool, err error)

// Verdict is the empirical classification produced by re-running a flagged
// test in isolation. It is intentionally a small closed vocabulary so the
// rendered/​localized text and any future machine surface stay stable.
type Verdict string

const (
	// VerdictFlaky means the reruns produced BOTH a pass and a fail: the test
	// is non-deterministic under identical conditions — confirmed flaky.
	VerdictFlaky Verdict = "flaky"
	// VerdictRealFailure means every rerun failed: the test fails
	// deterministically, so it is a genuine failure, not flakiness. Such a
	// test must NOT be quarantined as flaky — it is correctly red.
	VerdictRealFailure Verdict = "real_failure"
	// VerdictTransient means every rerun passed: the original signal did not
	// reproduce. The flake (if any) was a one-off / already resolved.
	VerdictTransient Verdict = "transient"
	// VerdictInconclusive means no rerun was observed (runs <= 0, or every
	// attempt errored before yielding a result), so no classification can be
	// made. The caller should fall back to the static finding alone.
	VerdictInconclusive Verdict = "inconclusive"
)

// RerunResult is the outcome of a sandbox rerun campaign for one test.
type RerunResult struct {
	// Verdict is the empirical classification (see the Verdict constants).
	Verdict Verdict
	// Passes and Fails are the observed counts across the reruns that
	// produced a result. Errored attempts (Executor returned a non-nil error)
	// are counted in neither — they are tallied in Errors instead.
	Passes int
	Fails  int
	// Errors is the number of attempts that could not be observed (Executor
	// returned an error). A campaign that errors on every attempt is
	// Inconclusive.
	Errors int
	// Runs is the number of attempts actually made. With early exit it can be
	// fewer than the requested N (we stop as soon as a mixed pass+fail proves
	// flakiness), so callers can report "confirmed after k of N runs".
	Runs int
}

// Rerun re-runs a single flagged test up to runs times via exec and classifies
// the result. It is a pure function of exec's behaviour: given the same exec
// it returns the same verdict, so it is trivially testable with a fake.
//
// Contract:
//   - mixed pass+fail  → VerdictFlaky (confirmed)
//   - all observed fail → VerdictRealFailure (genuine red — do not quarantine)
//   - all observed pass → VerdictTransient (did not reproduce)
//   - nothing observed  → VerdictInconclusive
//
// Early exit: as soon as BOTH a pass and a fail have been seen the verdict can
// no longer change (flaky is terminal), so Rerun stops and returns without
// burning the remaining attempts. Errored attempts never short-circuit the
// loop — a flickering runner should not be mistaken for a flaky test — but
// they are recorded so a wholly-unobservable campaign is reported honestly.
//
// runs <= 0 yields an Inconclusive result with no calls to exec. A nil exec is
// treated the same way (defensive: the seam is unbound), so the orchestrator
// is safe to call even when the caller never bound a runner.
func Rerun(ctx context.Context, exec Executor, testID string, runs int) RerunResult {
	res := RerunResult{Verdict: VerdictInconclusive}
	if exec == nil || runs <= 0 {
		return res
	}

	for i := 0; i < runs; i++ {
		// Honour cancellation between attempts so a slow campaign can be
		// aborted; whatever was observed so far still classifies.
		if ctx != nil && ctx.Err() != nil {
			break
		}
		passed, err := exec(ctx, testID)
		res.Runs++
		switch {
		case err != nil:
			res.Errors++
		case passed:
			res.Passes++
		default:
			res.Fails++
		}
		// Early exit: a single pass AND a single fail is conclusive flakiness;
		// no further rerun can overturn it.
		if res.Passes > 0 && res.Fails > 0 {
			res.Verdict = VerdictFlaky
			return res
		}
	}

	res.Verdict = classify(res.Passes, res.Fails)
	return res
}

// classify maps observed pass/fail counts to a Verdict. It is the single
// source of the pass/fail-mix rule so Rerun and any future caller agree.
func classify(passes, fails int) Verdict {
	switch {
	case passes > 0 && fails > 0:
		return VerdictFlaky
	case fails > 0: // only fails observed
		return VerdictRealFailure
	case passes > 0: // only passes observed
		return VerdictTransient
	default: // nothing observed
		return VerdictInconclusive
	}
}

// Confirmed reports whether the rerun empirically confirmed flakiness. It is
// the predicate a caller uses to decide whether to keep treating the candidate
// as flaky after the rerun (true) or to step back from the static finding
// (a real failure or a non-reproducing transient).
func (r RerunResult) Confirmed() bool { return r.Verdict == VerdictFlaky }

// verdictKey maps a Verdict to its i18n catalog key suffix. Inconclusive has
// no annotation — there is nothing to tell the user beyond the static finding.
func verdictKey(v Verdict) string {
	switch v {
	case VerdictFlaky:
		return "flaky.sandbox.flaky"
	case VerdictRealFailure:
		return "flaky.sandbox.real_failure"
	case VerdictTransient:
		return "flaky.sandbox.transient"
	default:
		return ""
	}
}

// Annotate folds a rerun verdict into an existing static flaky finding without
// touching the JSON schema (locked at v1, ADR-0014): the localized verdict line
// is appended to the finding's Suggestion, and the finding's Severity is
// adjusted to reflect empirical truth:
//
//   - flaky        → keep the finding; the rerun upgrades it from "suspected"
//     to "confirmed" (text only; severity unchanged).
//   - real_failure → this is a genuinely red test, not flakiness; the static
//     anti-pattern note still applies but must not masquerade as a flake, so
//     the verdict line says so plainly.
//   - transient    → the flake did not reproduce; downgraded to info so a
//     one-off does not block a commit-stage gate, with a line saying it
//     passed every rerun.
//   - inconclusive → returned unchanged (no rerun was observed).
//
// Annotate never drops a finding — the static signal is preserved either way;
// the rerun only refines how it is presented. cat localizes the verdict line
// to the resolved output language (ADR-0021).
func Annotate(cat *i18n.Catalog, f render.Finding, r RerunResult) render.Finding {
	key := verdictKey(r.Verdict)
	if key == "" || cat == nil {
		return f
	}
	line := cat.T(key, r.Runs)
	if f.Suggestion == "" {
		f.Suggestion = line
	} else {
		f.Suggestion = f.Suggestion + " " + line
	}
	if r.Verdict == VerdictTransient {
		// A non-reproducing one-off should not gate a commit; demote it so
		// --fail-on doesn't trip on a flake that didn't reproduce.
		f.Severity = render.SeverityInfo
	}
	return f
}
