// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"path/filepath"
	"sort"

	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// severityRank maps a severity to an ascending integer (info=0 …
// critical=4) so the "actual ≥ min_severity" floor is a plain comparison.
// Unknown severities sort below info; the findings parser rejects them
// upstream, so the -1 branch is defensive only.
func severityRank(s render.Severity) int {
	switch s {
	case render.SeverityCritical:
		return 4
	case render.SeverityHigh:
		return 3
	case render.SeverityMedium:
		return 2
	case render.SeverityLow:
		return 1
	case render.SeverityInfo:
		return 0
	}
	return -1
}

// FixtureScore is the per-fixture outcome of scoring produced findings
// against the answer key.
type FixtureScore struct {
	Fixture string
	HeldOut bool // mirrors Fixture.HeldOut so a Scorecard can be split

	TruePositives  int // expected findings that were matched
	FalseNegatives int // expected findings that were missed
	FalsePositives int // produced findings that matched no expected finding

	SilenceViolations int // produced findings landing on a silence anchor
	SilenceAnchors    int // total silence anchors in the fixture

	// CaughtByCategory / MissedByCategory attribute each expected finding to
	// its category, giving a per-category recall breakdown (ADR-0018 §2).
	CaughtByCategory map[string]int
	MissedByCategory map[string]int

	// Usage is the token usage the provider reported for this fixture's
	// Review call. Zero on the mock tier (the mock provider's usage is a
	// fixed stub, never billed) and on a fixture whose Review call itself
	// failed (no response was ever returned to report usage from). Still
	// populated on a fixture that errored only because its response body
	// couldn't be parsed as findings — the provider did report usage for
	// that call, and RunFixture/RunCorpus carry it through rather than
	// discarding it (review 2026-09-24, MINOR).
	Usage provider.Usage

	// CleanAlarm reports whether this run raised at least one produced
	// finding of severity low-or-higher on a clean-control fixture —
	// ADR-0043 §3's fpr numerator for a single fixture run. info findings
	// don't count (the severity rubric defines info as "no action
	// required"); they still show up in Precision. Always false for a
	// fixture that planted a defect.
	CleanAlarm bool

	// Errored marks a fixture whose review call failed after the harness's
	// own retries, or whose output could not be parsed (ADR-0043 §3). Such
	// a run is scored as a miss for every expected finding and, for a
	// clean fixture, as a CleanAlarm — see ErrorScore.
	Errored bool

	// ErrorMsg is the last attempt's error text when Errored is true (empty
	// otherwise). RunCorpus previously swallowed this after exhausting
	// retries — a run's log looked identical whether a model genuinely
	// missed every finding or every call to it failed. Carrying the message
	// through lets the caller log it instead (review 2026-09-24, MAJOR).
	ErrorMsg string
}

// IsBuggy reports whether this fixture had at least one planted defect —
// the same "expected == 0 means clean" rule Recall/Precision already use.
func (s FixtureScore) IsBuggy() bool {
	return s.TruePositives+s.FalseNegatives > 0
}

// Precision = TP / (TP + FP). A run that produced no findings is vacuously
// precise (returns 1) so it does not divide by zero or drag an aggregate.
func (s FixtureScore) Precision() float64 {
	produced := s.TruePositives + s.FalsePositives
	if produced == 0 {
		return 1
	}
	return float64(s.TruePositives) / float64(produced)
}

// Recall = TP / (TP + FN). A fixture that expects nothing (a clean diff) is
// fully recalled by definition (returns 1).
func (s FixtureScore) Recall() float64 {
	expected := s.TruePositives + s.FalseNegatives
	if expected == 0 {
		return 1
	}
	return float64(s.TruePositives) / float64(expected)
}

// SilenceViolationRate = silence violations ÷ silence anchors. Returns 0
// when the fixture defines no anchors.
func (s FixtureScore) SilenceViolationRate() float64 {
	if s.SilenceAnchors == 0 {
		return 0
	}
	return float64(s.SilenceViolations) / float64(s.SilenceAnchors)
}

// matchesExpected reports whether a produced finding satisfies an expected
// finding's file + line-tolerance + severity-floor criteria (ADR-0018 §2).
func matchesExpected(f render.Finding, e ExpectedFinding) bool {
	if filepath.ToSlash(f.File) != filepath.ToSlash(e.File) {
		return false
	}
	if !withinTolerance(f, e) {
		return false
	}
	if e.MinSeverity != "" && severityRank(f.Severity) < severityRank(e.MinSeverity) {
		return false
	}
	return true
}

// withinTolerance reports whether the finding's line — or its
// [Line, LineEnd] range — lands within ±tolerance of the expected line.
func withinTolerance(f render.Finding, e ExpectedFinding) bool {
	tol := e.tolerance()
	if abs(f.Line-e.Line) <= tol {
		return true
	}
	if f.LineEnd > f.Line && e.Line >= f.Line-tol && e.Line <= f.LineEnd+tol {
		return true
	}
	return false
}

// hitsSilence reports whether a produced finding lands on a silence anchor
// (same file, line within the default tolerance). A multi-line finding
// [Line, LineEnd] that spans the anchor counts too — mirroring
// withinTolerance's range-overlap logic, so a finding whose start is far
// from the anchor but whose body covers it is still caught as a violation.
func hitsSilence(f render.Finding, a SilenceAnchor) bool {
	if filepath.ToSlash(f.File) != filepath.ToSlash(a.File) {
		return false
	}
	if abs(f.Line-a.Line) <= defaultLineTolerance {
		return true
	}
	return f.LineEnd > f.Line &&
		a.Line >= f.Line-defaultLineTolerance &&
		a.Line <= f.LineEnd+defaultLineTolerance
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Score matches produced findings against a fixture's answer key using
// one-to-one greedy assignment (ADR-0018 §2) and returns the tally. The
// corpus is assumed fully annotated, so any produced finding that matches
// no expected finding counts as a false positive.
func Score(produced []render.Finding, fx Fixture) FixtureScore {
	score := FixtureScore{
		Fixture:          fx.Name,
		HeldOut:          fx.HeldOut,
		SilenceAnchors:   len(fx.MustStaySilentOn),
		CaughtByCategory: map[string]int{},
		MissedByCategory: map[string]int{},
	}

	matched := make([]bool, len(produced))

	for _, exp := range fx.Expected {
		caught := false
		for i, f := range produced {
			if matched[i] {
				continue
			}
			if matchesExpected(f, exp) {
				matched[i] = true
				caught = true
				break
			}
		}
		if caught {
			score.TruePositives++
			score.CaughtByCategory[exp.Category]++
		} else {
			score.FalseNegatives++
			score.MissedByCategory[exp.Category]++
		}
	}

	for i, f := range produced {
		if !matched[i] {
			score.FalsePositives++
		}
		for _, anchor := range fx.MustStaySilentOn {
			if hitsSilence(f, anchor) {
				score.SilenceViolations++
				break
			}
		}
	}

	// ADR-0043 §3's fpr: a clean fixture (no planted defect) that draws at
	// least one low-or-higher finding is an alarm. info is excluded from
	// the numerator — it still counts toward Precision above.
	if len(fx.Expected) == 0 {
		for _, f := range produced {
			if severityRank(f.Severity) >= severityRank(render.SeverityLow) {
				score.CleanAlarm = true
				break
			}
		}
	}

	return score
}

// ErrorScore produces the FixtureScore for a fixture whose review call
// failed after the harness's own retries, or whose output could not be
// parsed (ADR-0043 §3). It counts as a miss for every expected finding of a
// buggy fixture, and as a CleanAlarm for a clean fixture — an errored model
// is not silently dropped from the corpus it failed on.
func ErrorScore(fx Fixture) FixtureScore {
	score := FixtureScore{
		Fixture:          fx.Name,
		HeldOut:          fx.HeldOut,
		SilenceAnchors:   len(fx.MustStaySilentOn),
		CaughtByCategory: map[string]int{},
		MissedByCategory: map[string]int{},
		Errored:          true,
	}
	for _, exp := range fx.Expected {
		score.FalseNegatives++
		score.MissedByCategory[exp.Category]++
	}
	if len(fx.Expected) == 0 {
		score.CleanAlarm = true
	}
	return score
}

// Scorecard aggregates fixture scores for one provider+model run.
type Scorecard struct {
	Provider string
	Model    string
	Fixtures []FixtureScore
}

// totals sums the raw counts across every fixture in the scorecard.
func (sc Scorecard) totals() (tp, fn, fp, sv, sa int) {
	for _, s := range sc.Fixtures {
		tp += s.TruePositives
		fn += s.FalseNegatives
		fp += s.FalsePositives
		sv += s.SilenceViolations
		sa += s.SilenceAnchors
	}
	return tp, fn, fp, sv, sa
}

// Precision is the corpus-wide TP / (TP + FP).
func (sc Scorecard) Precision() float64 {
	tp, _, fp, _, _ := sc.totals()
	if tp+fp == 0 {
		return 1
	}
	return float64(tp) / float64(tp+fp)
}

// Recall is the corpus-wide TP / (TP + FN).
func (sc Scorecard) Recall() float64 {
	tp, fn, _, _, _ := sc.totals()
	if tp+fn == 0 {
		return 1
	}
	return float64(tp) / float64(tp+fn)
}

// SilenceViolationRate is the corpus-wide silence violations ÷ silence
// anchors.
func (sc Scorecard) SilenceViolationRate() float64 {
	_, _, _, sv, sa := sc.totals()
	if sa == 0 {
		return 0
	}
	return float64(sv) / float64(sa)
}

// RecallN is Recall's denominator (TP + FN, i.e. every planted defect the
// run was scored against) — the n a reported recall figure should always
// be printed alongside, since "87% recall" is meaningless without it.
func (sc Scorecard) RecallN() int {
	tp, fn, _, _, _ := sc.totals()
	return tp + fn
}

// PrecisionN is Precision's denominator (TP + FP, every produced finding
// the run was scored against).
func (sc Scorecard) PrecisionN() int {
	tp, _, fp, _, _ := sc.totals()
	return tp + fp
}

// SilenceViolationRateN is SilenceViolationRate's denominator (total silence
// anchors in the scored fixtures).
func (sc Scorecard) SilenceViolationRateN() int {
	_, _, _, _, sa := sc.totals()
	return sa
}

// NBuggy is the number of fixtures in the scorecard that planted at least
// one defect (mirrors Recall's own "expected == 0 means clean" rule).
func (sc Scorecard) NBuggy() int {
	n := 0
	for _, f := range sc.Fixtures {
		if f.IsBuggy() {
			n++
		}
	}
	return n
}

// NClean is the number of clean-control fixtures (no planted defect) in
// the scorecard.
func (sc Scorecard) NClean() int {
	return len(sc.Fixtures) - sc.NBuggy()
}

// NExpected is the total number of planted defects (expected findings)
// across every fixture in the scorecard.
func (sc Scorecard) NExpected() int {
	n := 0
	for _, f := range sc.Fixtures {
		n += f.TruePositives + f.FalseNegatives
	}
	return n
}

// CleanAlarms is the count of clean-control fixtures on which this run
// raised at least one low-or-higher finding — ADR-0043 §3's fpr numerator
// for a single Scorecard. Pool across k runs by summing CleanAlarms() and
// dividing by (runs × NClean()), not by averaging each run's own rate.
func (sc Scorecard) CleanAlarms() int {
	n := 0
	for _, f := range sc.Fixtures {
		if f.CleanAlarm {
			n++
		}
	}
	return n
}

// Errors is the count of fixtures in the scorecard whose run errored after
// retries (ADR-0043 §3) — a provider failure or an unparseable response,
// recorded rather than aborting the corpus.
func (sc Scorecard) Errors() int {
	n := 0
	for _, f := range sc.Fixtures {
		if f.Errored {
			n++
		}
	}
	return n
}

// TotalUsage sums the provider usage reported across every fixture in the
// scorecard — the basis for a cost-per-review figure.
func (sc Scorecard) TotalUsage() provider.Usage {
	var u provider.Usage
	for _, f := range sc.Fixtures {
		u.InputTokens += f.Usage.InputTokens
		u.OutputTokens += f.Usage.OutputTokens
		u.CachedInputTokens += f.Usage.CachedInputTokens
	}
	return u
}

// Scorecard.USDPerReview was removed (review 2026-09-24, BLOCKER follow-up):
// it priced usage via provider.Pricing.Cost, which applies a cache-hit
// discount — the same bug fixed in results.go's BuildRow by
// uncachedInputOutputCost. It had no callers (BuildRow computes the
// ADR-0043 §4 usd_per_review field directly from TotalUsage), so rather
// than keep two cost formulas in this package that could drift apart
// again, it's gone; call uncachedInputOutputCost(pricing, sc.TotalUsage())
// directly if a scorecard-level figure is needed again.

// slice returns a Scorecard containing only the fixtures whose HeldOut flag
// equals heldOut, preserving provider/model.
func (sc Scorecard) slice(heldOut bool) Scorecard {
	out := Scorecard{Provider: sc.Provider, Model: sc.Model}
	for _, f := range sc.Fixtures {
		if f.HeldOut == heldOut {
			out.Fixtures = append(out.Fixtures, f)
		}
	}
	return out
}

// Dev returns the tunable slice (fixtures the prompt/corpus may be tuned
// against). HeldOut returns the generalization-only slice (ADR-0018
// §Goodhart). A change is overfitting when Dev recall rises but HeldOut
// recall does not.
func (sc Scorecard) Dev() Scorecard     { return sc.slice(false) }
func (sc Scorecard) HeldOut() Scorecard { return sc.slice(true) }

// CategoryRecall is per-category recall for one expected-finding category.
type CategoryRecall struct {
	Category string
	Caught   int
	Total    int
}

// CategoryRecall returns recall per expected-finding category, sorted by
// category name for deterministic output.
func (sc Scorecard) CategoryRecall() []CategoryRecall {
	caught := map[string]int{}
	total := map[string]int{}
	for _, s := range sc.Fixtures {
		for cat, n := range s.CaughtByCategory {
			caught[cat] += n
			total[cat] += n
		}
		for cat, n := range s.MissedByCategory {
			total[cat] += n
		}
	}
	out := make([]CategoryRecall, 0, len(total))
	for cat, t := range total {
		out = append(out, CategoryRecall{Category: cat, Caught: caught[cat], Total: t})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Category < out[j].Category })
	return out
}
