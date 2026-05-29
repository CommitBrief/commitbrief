// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/provider/mock"
	"github.com/CommitBrief/commitbrief/internal/render"
)

func corpusDir() string { return filepath.Join("testdata", "corpus") }

// TestEvalMockCorpus is the deterministic tier (ADR-0018 §3): it runs every
// corpus fixture through the mock provider with that fixture's scripted
// "ideal" response and asserts the scoring invariant below. It runs under
// plain `go test ./...`, so a regression in the harness, the matcher, or a
// fixture's internal consistency fails CI. It does NOT measure model
// quality — the mock's answers are authored, not earned.
//
// Invariant: each fixture ships a mock_response.json that is the *ideal*
// answer to its own expected.json, so scoring it must yield perfect recall,
// no false positives, and no silence violations. That single property
// validates, for every fixture and at any corpus size:
//   - the diff, answer key, and mock response all load and parse;
//   - the matcher pairs the ideal answer to each expected finding (so the
//     fixture's file / line / severity-floor are internally consistent);
//   - the scripted answer trips none of the fixture's silence anchors.
//
// It needs no hand-maintained per-fixture tally, so the corpus can grow
// without touching this test.
func TestEvalMockCorpus(t *testing.T) {
	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	sc := Scorecard{Provider: "mock", Model: "mock-model"}
	for _, fx := range fixtures {
		if fx.MockResponse == "" {
			t.Fatalf("fixture %q: missing mock_response.json (required by the deterministic tier)", fx.Name)
		}
		m := mock.New()
		m.ResponseContent = fx.MockResponse

		score, runErr := RunFixture(context.Background(), m, fx, "")
		if runErr != nil {
			t.Fatalf("fixture %q: RunFixture: %v", fx.Name, runErr)
		}
		sc.Fixtures = append(sc.Fixtures, score)

		if score.TruePositives != len(fx.Expected) || score.FalseNegatives != 0 {
			t.Errorf("fixture %q: ideal mock recall imperfect — TP=%d FN=%d, want TP=%d FN=0 (check expected.json vs mock_response.json file/line/severity alignment)",
				fx.Name, score.TruePositives, score.FalseNegatives, len(fx.Expected))
		}
		if score.FalsePositives != 0 {
			t.Errorf("fixture %q: ideal mock produced %d false positive(s) — every mock finding must match an expected finding", fx.Name, score.FalsePositives)
		}
		if score.SilenceViolations != 0 {
			t.Errorf("fixture %q: ideal mock tripped %d silence anchor(s) — no mock finding may land on a must_stay_silent_on line", fx.Name, score.SilenceViolations)
		}
	}

	// With ideal scripted responses the aggregate is perfect; this guards
	// the aggregation math, not the model.
	if got := sc.Recall(); got != 1 {
		t.Errorf("aggregate recall = %v, want 1", got)
	}
	if got := sc.Precision(); got != 1 {
		t.Errorf("aggregate precision = %v, want 1", got)
	}
	if got := sc.FalsePositiveRate(); got != 0 {
		t.Errorf("aggregate false-positive rate = %v, want 0", got)
	}
	t.Logf("deterministic corpus: %d fixtures, all ideal-mock-perfect", len(fixtures))
}

func TestLoadCorpusSorted(t *testing.T) {
	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(fixtures) < 3 {
		t.Fatalf("expected at least 3 fixtures, got %d", len(fixtures))
	}
	for i := 1; i < len(fixtures); i++ {
		if fixtures[i-1].Name > fixtures[i].Name {
			t.Errorf("fixtures not sorted: %q before %q", fixtures[i-1].Name, fixtures[i].Name)
		}
	}
}

// TestHeldOutSlice guards the Goodhart protection (ADR-0018 §Goodhart): the
// corpus must keep a non-trivial, representative held-out slice that prompt
// and corpus tuning never inspect. If someone moves every fixture into the
// tunable dev slice (so "the eval always passes"), this fails.
func TestHeldOutSlice(t *testing.T) {
	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	var held, dev []Fixture
	for _, fx := range fixtures {
		if fx.HeldOut {
			held = append(held, fx)
		} else {
			dev = append(dev, fx)
		}
	}

	if len(held) == 0 {
		t.Fatal("held-out slice is empty — Goodhart protection is disabled")
	}
	if len(dev) == 0 {
		t.Fatal("dev slice is empty — nothing left to tune against")
	}
	if len(dev) < len(held) {
		t.Errorf("dev slice (%d) should be larger than the held-out slice (%d)", len(dev), len(held))
	}

	frac := float64(len(held)) / float64(len(fixtures))
	if frac < 0.15 || frac > 0.40 {
		t.Errorf("held-out fraction %.2f is outside [0.15, 0.40] (%d/%d) — re-balance the slice", frac, len(held), len(fixtures))
	}

	// Representativeness: the held-out slice must span several categories and
	// include a clean control, or its generalization estimate is biased.
	catSet := map[string]struct{}{}
	clean := false
	for _, fx := range held {
		for _, c := range fx.Categories() {
			catSet[c] = struct{}{}
			if c == "clean" {
				clean = true
			}
		}
	}
	cats := make([]string, 0, len(catSet))
	for c := range catSet {
		cats = append(cats, c)
	}
	sort.Strings(cats)

	if len(cats) < 3 {
		t.Errorf("held-out slice spans only %d categories %v; needs >=3 for a fair generalization estimate", len(cats), cats)
	}
	if !clean {
		t.Error("held-out slice has no clean control; cannot measure held-out false-positive behavior")
	}
	t.Logf("held-out slice: %d/%d fixtures (%.0f%%), categories=%v", len(held), len(fixtures), frac*100, cats)
}

func TestScoreMatching(t *testing.T) {
	fx := Fixture{
		Name: "unit",
		Expected: []ExpectedFinding{
			{ID: "a", File: "pkg/a.go", Line: 100, LineTol: 3, Category: "security", MinSeverity: render.SeverityHigh},
		},
		MustStaySilentOn: []SilenceAnchor{
			{File: "pkg/b.go", Line: 50, Reason: "rename"},
		},
	}

	t.Run("exact match within tolerance", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityCritical, File: "pkg/a.go", Line: 102},
		}, fx)
		if got.TruePositives != 1 || got.FalsePositives != 0 || got.FalseNegatives != 0 {
			t.Errorf("got %+v, want TP=1 FP=0 FN=0", got)
		}
	})

	t.Run("severity below floor does not match", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityLow, File: "pkg/a.go", Line: 100},
		}, fx)
		if got.TruePositives != 0 || got.FalseNegatives != 1 || got.FalsePositives != 1 {
			t.Errorf("got %+v, want TP=0 FN=1 FP=1", got)
		}
	})

	t.Run("line outside tolerance does not match", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityHigh, File: "pkg/a.go", Line: 110},
		}, fx)
		if got.TruePositives != 0 || got.FalseNegatives != 1 || got.FalsePositives != 1 {
			t.Errorf("got %+v, want TP=0 FN=1 FP=1", got)
		}
	})

	t.Run("wrong file does not match", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityHigh, File: "pkg/z.go", Line: 100},
		}, fx)
		if got.TruePositives != 0 || got.FalseNegatives != 1 || got.FalsePositives != 1 {
			t.Errorf("got %+v, want TP=0 FN=1 FP=1", got)
		}
	})

	t.Run("range overlap matches", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityHigh, File: "pkg/a.go", Line: 96, LineEnd: 105},
		}, fx)
		if got.TruePositives != 1 {
			t.Errorf("got TP=%d, want 1 (expected line 100 inside [96,105])", got.TruePositives)
		}
	})

	t.Run("silence anchor violation is counted", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityMedium, File: "pkg/b.go", Line: 51},
		}, fx)
		if got.SilenceViolations != 1 {
			t.Errorf("got silenceViolations=%d, want 1", got.SilenceViolations)
		}
		if got.FalsePositives != 1 {
			t.Errorf("got FP=%d, want 1 (no expected finding matches)", got.FalsePositives)
		}
		if got.FalseNegatives != 1 {
			t.Errorf("got FN=%d, want 1 (the security finding was missed)", got.FalseNegatives)
		}
	})

	t.Run("clean diff with silent finding is fully recalled", func(t *testing.T) {
		clean := Fixture{Name: "clean"}
		got := Score(nil, clean)
		if got.Recall() != 1 || got.Precision() != 1 {
			t.Errorf("clean diff: got recall=%v precision=%v, want 1/1", got.Recall(), got.Precision())
		}
	})
}
