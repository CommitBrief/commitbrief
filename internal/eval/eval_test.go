// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// Corpus composition the published docs cite (README "Measured review
// quality", the web Benchmarks section, CHANGELOG, ADR-0018). These are
// locked here so a fixture added or annotated without updating the numbers
// fails CI instead of silently drifting the published figures.
const (
	wantFixtures       = 40
	wantPlantedDefects = 23
	wantCleanControls  = 20
	wantHeldOut        = 8
)

// TestCorpusComposition pins the counts the docs advertise (drift guard
// requested in dogfooding review). Update both the consts and the docs
// together when the corpus changes.
func TestCorpusComposition(t *testing.T) {
	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
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

	if len(fixtures) != wantFixtures {
		t.Errorf("fixtures = %d, want %d (update consts + README/web/CHANGELOG)", len(fixtures), wantFixtures)
	}
	if defects != wantPlantedDefects {
		t.Errorf("planted defects = %d, want %d (update consts + README/web/CHANGELOG)", defects, wantPlantedDefects)
	}
	if clean != wantCleanControls {
		t.Errorf("clean controls = %d, want %d", clean, wantCleanControls)
	}
	if held != wantHeldOut {
		t.Errorf("held-out fixtures = %d, want %d", held, wantHeldOut)
	}
}

func TestIsRetriable(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"eval: fixture \"x\": review: gemini: Error 503, Status: UNAVAILABLE", true},
		{"eval: fixture \"x\": review: rate limit exceeded (429)", true},
		{"eval: fixture \"x\": review: context deadline exceeded", true},
		{"eval: fixture \"x\": review: 401 unauthorized: bad api key", false},
		{"eval: fixture \"x\": parse findings: unexpected end of JSON input", false},
		{"eval: fixture \"x\": review: model not found", false},
		// Boundary guard: a context-length error embeds "500" inside a token
		// count but is NOT a transient 500 — must not be retried.
		{"eval: fixture \"x\": review: maximum context length is 128000 tokens, however you requested 130500 tokens", false},
		// A duration string carrying "1500ms" must not look like a 500.
		{"eval: fixture \"x\": review: request took 1500ms then was rejected: invalid request", false},
		// A genuine standalone 503 still retries.
		{"eval: fixture \"x\": review: provider returned http 503", true},
	}
	for _, c := range cases {
		if got := isRetriable(errors.New(c.msg)); got != c.want {
			t.Errorf("isRetriable(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
	if isRetriable(nil) {
		t.Error("isRetriable(nil) must be false")
	}
}

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
	if got := sc.SilenceViolationRate(); got != 0 {
		t.Errorf("aggregate silence violation rate = %v, want 0", got)
	}
	// n_clean/n_buggy and the language distribution are logged so a corpus
	// change (fixture added/removed, language rebalanced) is visible in the
	// test log without cross-referencing TestCorpusComposition or grepping
	// testdata — the log line previously only said "40 fixtures", which
	// couldn't tell a clean/buggy imbalance or a language skew apart from a
	// healthy corpus (review 2026-09-24, MINOR).
	var nClean, nBuggy int
	langCounts := map[string]int{}
	for _, fx := range fixtures {
		if len(fx.Expected) == 0 {
			nClean++
		} else {
			nBuggy++
		}
		langCounts[fx.Language]++
	}
	langs := make([]string, 0, len(langCounts))
	for l := range langCounts {
		langs = append(langs, l)
	}
	sort.Strings(langs)
	dist := make([]string, 0, len(langs))
	for _, l := range langs {
		dist = append(dist, fmt.Sprintf("%s=%d", l, langCounts[l]))
	}
	t.Logf("deterministic corpus: %d fixtures (n_clean=%d n_buggy=%d), all ideal-mock-perfect, languages: %s",
		len(fixtures), nClean, nBuggy, strings.Join(dist, " "))
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

	t.Run("multi-line finding spanning a silence anchor counts", func(t *testing.T) {
		// Start line 40 is well outside the ±3 window of the anchor at 50,
		// but the finding's [40,55] range covers it — must still violate.
		got := Score([]render.Finding{
			{Severity: render.SeverityMedium, File: "pkg/b.go", Line: 40, LineEnd: 55},
		}, fx)
		if got.SilenceViolations != 1 {
			t.Errorf("got silenceViolations=%d, want 1 (range [40,55] covers anchor at 50)", got.SilenceViolations)
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

// TestCleanAlarm pins ADR-0043 §3's fpr numerator: a clean-control fixture
// (no planted defect) only counts as an alarm when a produced finding is
// low severity or higher. An info-only finding is real signal for
// Precision but must never trip CleanAlarm — the severity rubric defines
// info as "no action required," so scoring it as an alarm would penalize a
// model for correctly staying quiet.
func TestCleanAlarm(t *testing.T) {
	clean := Fixture{Name: "clean-control"} // no Expected findings

	t.Run("info-only finding does not alarm", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityInfo, File: "pkg/a.go", Line: 10},
		}, clean)
		if got.CleanAlarm {
			t.Error("CleanAlarm = true, want false for an info-only finding on a clean fixture")
		}
	})

	t.Run("no findings does not alarm", func(t *testing.T) {
		got := Score(nil, clean)
		if got.CleanAlarm {
			t.Error("CleanAlarm = true, want false when nothing was produced")
		}
	})

	t.Run("low severity finding alarms", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityLow, File: "pkg/a.go", Line: 10},
		}, clean)
		if !got.CleanAlarm {
			t.Error("CleanAlarm = false, want true for a low-severity finding on a clean fixture")
		}
	})

	t.Run("high severity finding alarms", func(t *testing.T) {
		got := Score([]render.Finding{
			{Severity: render.SeverityHigh, File: "pkg/a.go", Line: 10},
		}, clean)
		if !got.CleanAlarm {
			t.Error("CleanAlarm = false, want true for a high-severity finding on a clean fixture")
		}
	})

	t.Run("buggy fixture never alarms even with low severity findings", func(t *testing.T) {
		buggy := Fixture{
			Name:     "buggy",
			Expected: []ExpectedFinding{{ID: "a", File: "pkg/a.go", Line: 10, Category: "bug"}},
		}
		got := Score([]render.Finding{
			{Severity: render.SeverityLow, File: "pkg/z.go", Line: 999},
		}, buggy)
		if got.CleanAlarm {
			t.Error("CleanAlarm = true, want false — fixture planted a defect, so it is not a clean control")
		}
	})
}

// TestPromptSHA256 pins PromptSHA256's formula (ADR-0043 §4): a stable,
// deterministic fingerprint of the (system, user-template) pair that
// changes whenever either text changes, independent of any fixture's diff.
func TestPromptSHA256(t *testing.T) {
	got := PromptSHA256()
	if len(got) != 64 {
		t.Fatalf("PromptSHA256() = %q (len %d), want a 64-char lowercase hex SHA-256", got, len(got))
	}
	for _, r := range got {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			t.Fatalf("PromptSHA256() = %q, want lowercase hex only", got)
		}
	}

	// Determinism: calling it twice with no state change yields the same
	// hash — a results row's prompt_sha256 must be reproducible across
	// invocations, not just within one.
	again := PromptSHA256()
	if got != again {
		t.Errorf("PromptSHA256() not deterministic: %q vs %q", got, again)
	}

	// Independent of any fixture's diff — buildRequest for two fixtures
	// with different diffs must not change the prompt fingerprint, since it
	// hashes the template before diff substitution.
	system, userTpl := rules.Build(rules.Default(), lang.English(), "")
	h := sha256.New()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write([]byte(userTpl))
	want := hex.EncodeToString(h.Sum(nil))
	if got != want {
		t.Errorf("PromptSHA256() = %q, want %q (recomputed from the same rules.Build inputs)", got, want)
	}
}

// TestBuildRequestNumberedDiff pins the production/eval prompt-parity fix:
// the review.go pipeline numbers every diff line (`<n>| `) before it reaches
// the model, and the eval harness must send the same numbered form via
// numberedFixtureDiff — not the raw fixture diff — or the corpus measures a
// prompt the CLI never actually ships.
func TestBuildRequestNumberedDiff(t *testing.T) {
	fx := Fixture{
		Name: "numbered",
		Diff: "diff --git a/pkg/a.go b/pkg/a.go\n" +
			"--- a/pkg/a.go\n" +
			"+++ b/pkg/a.go\n" +
			"@@ -1,2 +1,2 @@\n" +
			"-old line\n" +
			"+new line\n" +
			" context line\n",
	}

	req := buildRequest(fx, "test-model")

	if strings.Contains(req.UserPrompt, "+new line\n") && !strings.Contains(req.UserPrompt, "|") {
		t.Fatalf("user prompt looks like the raw diff (no numbering) — numberedFixtureDiff was not applied:\n%s", req.UserPrompt)
	}
	if !strings.Contains(req.UserPrompt, "| +new line") {
		t.Errorf("user prompt missing a numbered `| +new line` line — want the same `<n>| ` prefix review.go's pipeline sends:\n%s", req.UserPrompt)
	}
	if !strings.Contains(req.UserPrompt, "| -old line") {
		t.Errorf("user prompt missing a numbered `| -old line` line:\n%s", req.UserPrompt)
	}

	// Cross-check against numberedFixtureDiff directly, so this test fails
	// loudly if buildRequest ever stops routing through it.
	want := numberedFixtureDiff(fx)
	if !strings.Contains(req.UserPrompt, want) {
		t.Error("buildRequest's UserPrompt does not contain numberedFixtureDiff's output verbatim")
	}
}

// TestNumberedFixtureDiffDropsGoSum pins numberedFixtureDiff's filtering
// step (runner.go:48, via builtinIgnoreMatcher): a fixture diff touching
// go.sum alongside a real source file must have the go.sum hunk stripped
// before numbering, the same way review.go's production pipeline excludes
// it — otherwise the corpus measures a prompt the CLI never actually ships
// (re-review 1, MINOR-C).
func TestNumberedFixtureDiffDropsGoSum(t *testing.T) {
	fx := Fixture{
		Name: "go-sum-filtered",
		Diff: "diff --git a/pkg/a.go b/pkg/a.go\n" +
			"--- a/pkg/a.go\n" +
			"+++ b/pkg/a.go\n" +
			"@@ -1,2 +1,2 @@\n" +
			"-old line\n" +
			"+new line\n" +
			" context line\n" +
			"diff --git a/go.sum b/go.sum\n" +
			"--- a/go.sum\n" +
			"+++ b/go.sum\n" +
			"@@ -1,1 +1,1 @@\n" +
			"-h1:oldhash=\n" +
			"+h1:newhash=\n",
	}

	got := numberedFixtureDiff(fx)

	if strings.Contains(got, "go.sum") {
		t.Errorf("numberedFixtureDiff kept the go.sum hunk, want it filtered out by the built-in ignore matcher:\n%s", got)
	}
	if !strings.Contains(got, "new line") {
		t.Errorf("numberedFixtureDiff dropped the real pkg/a.go hunk along with go.sum:\n%s", got)
	}
}

// TestRunCorpusRecordsProviderError pins RunCorpus's error path
// (runner.go:222-234): when a fixture's review call fails on every attempt,
// the resulting Scorecard's FixtureScore must be marked Errored with a
// non-empty ErrorMsg rather than silently looking like a model that missed
// every finding (re-review 1, MINOR-C).
func TestRunCorpusRecordsProviderError(t *testing.T) {
	m := mock.New()
	m.ReviewErr = errors.New("boom: mock review failure")

	fx := Fixture{
		Name:     "will-error",
		Expected: []ExpectedFinding{{ID: "a", File: "pkg/a.go", Line: 10, Category: "bug"}},
	}

	sc, err := RunCorpus(context.Background(), m, "", []Fixture{fx})
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}
	if len(sc.Fixtures) != 1 {
		t.Fatalf("len(Fixtures) = %d, want 1", len(sc.Fixtures))
	}
	fs := sc.Fixtures[0]
	if !fs.Errored {
		t.Error("Errored = false, want true after every attempt failed")
	}
	if fs.ErrorMsg == "" {
		t.Error("ErrorMsg is empty, want the provider's error text")
	}
	if !strings.Contains(fs.ErrorMsg, "boom") {
		t.Errorf("ErrorMsg = %q, want it to contain the underlying provider error", fs.ErrorMsg)
	}
}
