// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"path/filepath"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/flaky"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// flakyCorpusDir is the known-answer slice for the deterministic static flaky
// detector (ADR-0022). It is kept separate from the LLM review corpus
// (testdata/corpus) because the flaky detector is a provider-free static pass:
// it produces findings directly from the diff, with no scripted
// mock_response.json. Scoring its real output here measures the detector's
// own precision / recall under the same matcher the LLM eval uses (ADR-0018).
func flakyCorpusDir() string { return filepath.Join("testdata", "flaky") }

// Flaky-slice composition, pinned so a fixture added or annotated without
// updating the published numbers fails CI instead of silently drifting.
const (
	wantFlakyFixtures = 7
	wantFlakyDefects  = 8 // expected flaky findings across all fixtures
	wantFlakyClean    = 2 // clean controls (no expected findings)
	wantFlakyHeldOut  = 2
)

// detectFlaky runs the real flaky.Detector over a fixture's diff and returns
// its findings — the exact path the CLI pre-pass takes, so this scores the
// shipped detector rather than a re-implementation.
func detectFlaky(t *testing.T, cat *i18n.Catalog, fx Fixture) []render.Finding {
	t.Helper()
	parsed, err := diff.Parse(git.Diff{Content: fx.Diff})
	if err != nil {
		t.Fatalf("fixture %q: diff.Parse: %v", fx.Name, err)
	}
	return flaky.New(cat).Detect(parsed)
}

// TestEvalFlakyCorpus is the deterministic flaky-detector eval (ADR-0018 §3,
// extended to the static detector of ADR-0022). It runs every flaky-slice
// fixture through the REAL detector — no provider, no mock — and scores the
// produced findings against the answer key with the shared matcher. Because
// the detector is deterministic, the corpus is internally consistent only if
// the slice is perfectly recalled with no false positives and no silence
// violations; any drift in a rule's regex, line walk, or threshold surfaces
// here. It runs under plain `go test ./...`, so it is part of the CI gate
// (`make check`) and the deterministic `make eval` target.
func TestEvalFlakyCorpus(t *testing.T) {
	cat, err := i18n.Load("en")
	if err != nil {
		t.Fatalf("i18n.Load: %v", err)
	}

	fixtures, err := LoadCorpus(flakyCorpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus(flaky): %v", err)
	}

	sc := Scorecard{Provider: "flaky-static", Model: "rules-v1"}
	for _, fx := range fixtures {
		findings := detectFlaky(t, cat, fx)
		score := Score(findings, fx)
		sc.Fixtures = append(sc.Fixtures, score)

		if score.TruePositives != len(fx.Expected) || score.FalseNegatives != 0 {
			t.Errorf("fixture %q: TP=%d FN=%d, want TP=%d FN=0 (check expected.json file/line/severity vs the detector output)",
				fx.Name, score.TruePositives, score.FalseNegatives, len(fx.Expected))
		}
		if score.FalsePositives != 0 {
			t.Errorf("fixture %q: %d false positive(s) — the detector flagged a line with no expected finding", fx.Name, score.FalsePositives)
		}
		if score.SilenceViolations != 0 {
			t.Errorf("fixture %q: %d silence violation(s) — a finding landed on a must_stay_silent_on line", fx.Name, score.SilenceViolations)
		}
	}

	if got := sc.Recall(); got != 1 {
		t.Errorf("flaky-slice recall = %v, want 1", got)
	}
	if got := sc.Precision(); got != 1 {
		t.Errorf("flaky-slice precision = %v, want 1", got)
	}
	if got := sc.FalsePositiveRate(); got != 0 {
		t.Errorf("flaky-slice false-positive rate = %v, want 0", got)
	}
	t.Logf("flaky slice: %d fixtures, precision=%.2f recall=%.2f false-positive-rate=%.2f",
		len(fixtures), sc.Precision(), sc.Recall(), sc.FalsePositiveRate())
	for _, cr := range sc.CategoryRecall() {
		t.Logf("   category %-24s recall=%d/%d", cr.Category, cr.Caught, cr.Total)
	}
}

// TestFlakyCorpusComposition pins the flaky-slice counts the docs cite
// (README flaky section, CHANGELOG). Update the consts and the docs together
// when the slice changes — mirrors TestCorpusComposition for the LLM corpus.
func TestFlakyCorpusComposition(t *testing.T) {
	fixtures, err := LoadCorpus(flakyCorpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus(flaky): %v", err)
	}

	defects, clean, held := 0, 0, 0
	for _, fx := range fixtures {
		defects += len(fx.Expected)
		if len(fx.Expected) == 0 {
			clean++
		}
		if fx.HeldOut {
			held++
		}
	}

	if len(fixtures) != wantFlakyFixtures {
		t.Errorf("flaky fixtures = %d, want %d (update consts + README/CHANGELOG)", len(fixtures), wantFlakyFixtures)
	}
	if defects != wantFlakyDefects {
		t.Errorf("flaky expected findings = %d, want %d (update consts + README/CHANGELOG)", defects, wantFlakyDefects)
	}
	if clean != wantFlakyClean {
		t.Errorf("flaky clean controls = %d, want %d", clean, wantFlakyClean)
	}
	if held != wantFlakyHeldOut {
		t.Errorf("flaky held-out fixtures = %d, want %d", held, wantFlakyHeldOut)
	}
}
