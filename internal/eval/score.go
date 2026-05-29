// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"path/filepath"
	"sort"

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

// FalsePositiveRate = silence violations ÷ silence anchors. Returns 0 when
// the fixture defines no anchors.
func (s FixtureScore) FalsePositiveRate() float64 {
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
// (same file, line within the default tolerance).
func hitsSilence(f render.Finding, a SilenceAnchor) bool {
	if filepath.ToSlash(f.File) != filepath.ToSlash(a.File) {
		return false
	}
	return abs(f.Line-a.Line) <= defaultLineTolerance
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

// FalsePositiveRate is the corpus-wide silence violations ÷ silence anchors.
func (sc Scorecard) FalsePositiveRate() float64 {
	_, _, _, sv, sa := sc.totals()
	if sa == 0 {
		return 0
	}
	return float64(sv) / float64(sa)
}

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
