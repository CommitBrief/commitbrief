// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
)

// perfectScorecard builds a Scorecard for provider/model with nBuggy
// perfectly-recalled buggy fixtures and nClean silent clean controls — a
// deterministic, zero-alarm baseline the tests below perturb from.
func perfectScorecard(providerName, model string, nBuggy, nClean int) Scorecard {
	sc := Scorecard{Provider: providerName, Model: model}
	for i := 0; i < nBuggy; i++ {
		sc.Fixtures = append(sc.Fixtures, FixtureScore{
			Fixture:          "buggy",
			TruePositives:    1,
			CaughtByCategory: map[string]int{},
			MissedByCategory: map[string]int{},
			Usage:            provider.Usage{InputTokens: 100, OutputTokens: 10},
		})
	}
	for i := 0; i < nClean; i++ {
		sc.Fixtures = append(sc.Fixtures, FixtureScore{
			Fixture:          "clean",
			CaughtByCategory: map[string]int{},
			MissedByCategory: map[string]int{},
			Usage:            provider.Usage{InputTokens: 50, OutputTokens: 5},
		})
	}
	return sc
}

func testPricing() provider.Pricing {
	return provider.Pricing{InputPer1M: 3, OutputPer1M: 15}
}

func testCorpus(fingerprint string, nBuggy, nClean int) CorpusInfo {
	return CorpusInfo{
		Fingerprint: fingerprint,
		NFixtures:   nBuggy + nClean,
		NBuggy:      nBuggy,
		NClean:      nClean,
		NExpected:   nBuggy,
		Languages:   map[string]int{"go": nBuggy + nClean},
	}
}

// coveredScorecard builds a Scorecard that satisfies ADR-0043 §2 sufficiency
// on the 6-language canonical set: 16 perfectly-recalled buggy fixtures and
// 6 silent clean controls per language (96 buggy / 36 clean).
func coveredScorecard(providerName, model string) Scorecard {
	sc := Scorecard{Provider: providerName, Model: model}
	for _, lang := range CanonicalLanguages() {
		for i := 0; i < 16; i++ {
			sc.Fixtures = append(sc.Fixtures, FixtureScore{
				Fixture:          "buggy-" + lang,
				Language:         lang,
				TruePositives:    1,
				CaughtByCategory: map[string]int{},
				MissedByCategory: map[string]int{},
				Usage:            provider.Usage{InputTokens: 100, OutputTokens: 10},
			})
		}
		for i := 0; i < 6; i++ {
			sc.Fixtures = append(sc.Fixtures, FixtureScore{
				Fixture:          "clean-" + lang,
				Language:         lang,
				CaughtByCategory: map[string]int{},
				MissedByCategory: map[string]int{},
				Usage:            provider.Usage{InputTokens: 50, OutputTokens: 5},
			})
		}
	}
	return sc
}

// coveredCorpus is the corpus block matching coveredScorecard.
func coveredCorpus(fingerprint string) CorpusInfo {
	return CorpusInfo{
		Fingerprint: fingerprint,
		NFixtures:   132,
		NBuggy:      96,
		NClean:      36,
		NExpected:   96,
		Languages:   map[string]int{"go": 22, "typescript": 22, "javascript": 22, "python": 22, "java": 22, "php": 22},
	}
}

// threeRuns repeats one Scorecard k=3 times, the run count §2 requires.
func threeRuns(sc Scorecard) []Scorecard { return []Scorecard{sc, sc, sc} }

// TestResultsBuildRowShape pins BuildRow's pooling: sum-then-divide for the
// headline ratios, min/max spread for recall/fpr across runs, and the
// zero-denominator conventions carried over from Scorecard/FixtureScore.
func TestResultsBuildRowShape(t *testing.T) {
	// Run 1: perfect. Run 2: one clean fixture alarms and one buggy fixture
	// is missed — so recall_min/max and fpr_min/max must show real spread,
	// not the pooled figure repeated.
	run1 := perfectScorecard("mock", "mock-model", 20, 20)
	run2 := perfectScorecard("mock", "mock-model", 20, 20)
	run2.Fixtures[0].TruePositives = 0
	run2.Fixtures[0].FalseNegatives = 1
	run2.Fixtures[20].CleanAlarm = true // first clean fixture in run2

	measuredAt := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	row, err := BuildRow([]Scorecard{run1, run2}, testPricing(), "deadbeef", "abc1234", measuredAt)
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}

	if row.Provider != "mock" || row.Model != "mock-model" {
		t.Errorf("provider/model = %q/%q, want mock/mock-model", row.Provider, row.Model)
	}
	if row.Runs != 2 {
		t.Errorf("Runs = %d, want 2", row.Runs)
	}
	if row.NBuggy != 20 || row.NClean != 20 {
		t.Errorf("NBuggy/NClean = %d/%d, want 20/20", row.NBuggy, row.NClean)
	}

	// Pooled recall: run1 TP=20/20, run2 TP=19/20 → summed 39/40.
	wantRecall := round4(39.0 / 40.0)
	if row.Recall != wantRecall {
		t.Errorf("Recall = %v, want %v (sum-then-divide, not averaged)", row.Recall, wantRecall)
	}
	if row.RecallMin != round4(19.0/20.0) || row.RecallMax != 1 {
		t.Errorf("RecallMin/Max = %v/%v, want %v/1 (per-run spread)", row.RecallMin, row.RecallMax, round4(19.0/20.0))
	}

	// Pooled fpr: run1 0 alarms, run2 1 alarm, out of runs*nClean = 2*20=40.
	wantFPR := round4(1.0 / 40.0)
	if row.FPR != wantFPR {
		t.Errorf("FPR = %v, want %v", row.FPR, wantFPR)
	}
	if row.FPRMin != 0 || row.FPRMax != round4(1.0/20.0) {
		t.Errorf("FPRMin/Max = %v/%v, want 0/%v", row.FPRMin, row.FPRMax, round4(1.0/20.0))
	}

	if row.MeasuredAt != measuredAt.UTC().Format(time.RFC3339) {
		t.Errorf("MeasuredAt = %q, want RFC3339 UTC of %v", row.MeasuredAt, measuredAt)
	}
	if row.PromptSHA256 != "deadbeef" || row.CommitbriefCommit != "abc1234" {
		t.Errorf("PromptSHA256/CommitbriefCommit = %q/%q, want deadbeef/abc1234", row.PromptSHA256, row.CommitbriefCommit)
	}
	if row.Pricing.InputPer1M != 3 || row.Pricing.OutputPer1M != 15 {
		t.Errorf("Pricing = %+v, want {3 15}", row.Pricing)
	}
	if row.USDPerReview <= 0 {
		t.Errorf("USDPerReview = %v, want > 0 (usage was non-zero)", row.USDPerReview)
	}
}

// TestResultsBuildRowZeroDenominators pins the vacuous-1 (recall/precision)
// vs. zero (fpr/silence_violation_rate) conventions when a scorecard has no
// buggy or no clean fixtures to divide by — mirroring Scorecard's own rules.
func TestResultsBuildRowZeroDenominators(t *testing.T) {
	allClean := perfectScorecard("mock", "m", 0, 5)
	row, err := BuildRow([]Scorecard{allClean}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if row.Recall != 1 {
		t.Errorf("Recall with no buggy fixtures = %v, want 1 (vacuous)", row.Recall)
	}
	if row.FPR != 0 {
		t.Errorf("FPR with zero alarms = %v, want 0", row.FPR)
	}

	allBuggy := perfectScorecard("mock", "m", 5, 0)
	row2, err := BuildRow([]Scorecard{allBuggy}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if row2.FPR != 0 {
		t.Errorf("FPR with no clean fixtures = %v, want 0 (nClean==0)", row2.FPR)
	}
}

// TestResultsBuildRowIgnoresCacheDiscount pins ADR-0043 §3's uncached list
// rate rule (review 2026-09-24 BLOCKER): a row's usd_per_review must price
// every input token — cached or not — at Pricing.InputPer1M. It must never
// apply Pricing.CachedInputPer1M's discount even when CachedInputTokens > 0,
// or an aggressively-cached provider would look artificially cheap and skew
// "cheapest eligible" model selection toward cache behavior instead of
// first-run cost.
func TestResultsBuildRowIgnoresCacheDiscount(t *testing.T) {
	sc := Scorecard{Provider: "mock", Model: "mock-model", Fixtures: []FixtureScore{
		{
			Fixture:          "buggy",
			TruePositives:    1,
			CaughtByCategory: map[string]int{},
			MissedByCategory: map[string]int{},
			Usage:            provider.Usage{InputTokens: 1000, OutputTokens: 100, CachedInputTokens: 900},
		},
	}}

	// CachedInputPer1M is far below InputPer1M, so a cache-aware cost would
	// be much cheaper than the uncached list-rate cost BuildRow must produce.
	pricing := provider.Pricing{InputPer1M: 3, OutputPer1M: 15, CachedInputPer1M: 0.3}

	row, err := BuildRow([]Scorecard{sc}, pricing, "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}

	wantUncached := round6((1000*3.0 + 100*15.0) / 1_000_000)
	if row.USDPerReview != wantUncached {
		t.Errorf("USDPerReview = %v, want %v (uncached list rate, ignoring CachedInputPer1M)", row.USDPerReview, wantUncached)
	}

	cacheAwareCost := pricing.Cost(provider.Usage{InputTokens: 1000, OutputTokens: 100, CachedInputTokens: 900})
	if row.USDPerReview <= cacheAwareCost {
		t.Errorf("USDPerReview = %v, want > cache-aware Cost() = %v (BuildRow must not apply the cache discount)", row.USDPerReview, cacheAwareCost)
	}
}

// TestResultsBuildRowNoScorecards pins the error path: an empty scorecard
// slice cannot produce a row (there is nothing to pool).
func TestResultsBuildRowNoScorecards(t *testing.T) {
	if _, err := BuildRow(nil, testPricing(), "sha", "commit", time.Now()); err == nil {
		t.Error("BuildRow(nil scorecards) = nil error, want an error")
	}
}

// TestResultsJSONFieldNames pins the ADR-0043 §4 wire shape: a row must
// marshal under the exact snake_case field names external tooling (the
// commitbrief.com site, a future `commitbrief eval` reader) depends on.
func TestResultsJSONFieldNames(t *testing.T) {
	row, err := BuildRow([]Scorecard{perfectScorecard("anthropic", "claude", 20, 20)}, testPricing(), "sha256hex", "commit1", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	row.Eligible = true

	data, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	want := []string{
		"provider", "model", "measured_at", "prompt_sha256", "commitbrief_commit",
		"runs", "n_buggy", "n_clean", "n_expected",
		"recall", "recall_min", "recall_max", "precision", "fpr", "fpr_min", "fpr_max",
		"silence_violation_rate", "errors", "breakdown",
		"input_tokens", "output_tokens", "cached_input_tokens",
		"pricing", "usd_per_review", "eligible",
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Errorf("results row JSON missing field %q", k)
		}
	}
	pricing, ok := m["pricing"].(map[string]any)
	if !ok {
		t.Fatalf("pricing field is not an object: %v", m["pricing"])
	}
	for _, k := range []string{"input_per_1m", "output_per_1m"} {
		if _, ok := pricing[k]; !ok {
			t.Errorf("results row pricing JSON missing field %q", k)
		}
	}
}

// TestResultsWriteResultCreatesFile pins the first-write path: a missing
// file is created with schema=1 and the one row, not treated as an error.
func TestResultsWriteResultCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "benchmark.json")

	corpus := testCorpus("fp-1", 20, 20)
	row, err := BuildRow([]Scorecard{perfectScorecard("mock", "m1", 20, 20)}, testPricing(), "sha-a", "commit-a", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}

	if err := WriteResult(path, corpus, 1, Thresholds{RecallMin: 0.9, FPRMax: 0.1}, row); err != nil {
		t.Fatalf("WriteResult: %v", err)
	}

	bf, existed, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if !existed {
		t.Fatal("existed = false after WriteResult created the file")
	}
	if bf.Schema != 1 {
		t.Errorf("Schema = %d, want 1", bf.Schema)
	}
	if bf.Corpus.Fingerprint != "fp-1" {
		t.Errorf("Corpus.Fingerprint = %q, want fp-1", bf.Corpus.Fingerprint)
	}
	if len(bf.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(bf.Results))
	}
	if bf.Results[0].Provider != "mock" || bf.Results[0].Model != "m1" {
		t.Errorf("Results[0] = %s/%s, want mock/m1", bf.Results[0].Provider, bf.Results[0].Model)
	}
}

// TestResultsLoadResultsMissingFile pins the "not found is not an error"
// contract WriteResult's first-write path depends on.
func TestResultsLoadResultsMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	bf, existed, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults(missing) returned error: %v", err)
	}
	if existed {
		t.Error("existed = true for a missing file, want false")
	}
	if bf.Schema != 0 || len(bf.Results) != 0 {
		t.Errorf("LoadResults(missing) = %+v, want zero value", bf)
	}
}

// TestResultsWriteResultMergesSameKey pins the replace-by-(provider,model)
// merge rule, and that a prompt-hash change on re-measurement drops the
// OTHER still-eligible-looking rows' eligibility only when their own
// sufficiency/threshold fails — a re-measured row under a new prompt hash
// becomes ineligible only via the prompt-match check, applied per row.
func TestResultsWriteResultMergesSameKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")
	corpus := coveredCorpus("fp-1")
	thresholds := Thresholds{RecallMin: 0.5, FPRMax: 0.5, LanguageRecallMin: 0.5}

	first := coveredScorecard("mock", "m1")
	rows := make([]Scorecard, 3)
	for i := range rows {
		rows[i] = first
	}
	row1, err := BuildRow(rows, testPricing(), "sha-old", "commit-a", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, corpus, 3, thresholds, row1); err != nil {
		t.Fatalf("WriteResult #1: %v", err)
	}

	bf, _, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(bf.Results) != 1 {
		t.Fatalf("after first write: len(Results) = %d, want 1", len(bf.Results))
	}
	if !bf.Results[0].Eligible {
		t.Fatalf("after first write: row not eligible (runs=3, n=96/36 with every language covered, recall=1>=0.5, fpr=0<=0.5, prompt matches itself); row=%+v", bf.Results[0])
	}

	// Re-measure the SAME provider+model under a NEW prompt hash — must
	// replace the row outright (not append a second), and the row's own
	// prompt hash now matches "current", so it stays eligible; but writing
	// under a hash that then gets superseded should drop it — verified by
	// the third write below (different key, current hash unrelated to
	// row1's hash).
	row2, err := BuildRow(rows, testPricing(), "sha-new", "commit-b", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, corpus, 3, thresholds, row2); err != nil {
		t.Fatalf("WriteResult #2 (same key, new prompt): %v", err)
	}
	bf, _, err = LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(bf.Results) != 1 {
		t.Fatalf("after replace: len(Results) = %d, want 1 (same provider+model key)", len(bf.Results))
	}
	if bf.Results[0].PromptSHA256 != "sha-new" {
		t.Errorf("after replace: PromptSHA256 = %q, want sha-new (row replaced, not appended)", bf.Results[0].PromptSHA256)
	}
	if bf.Results[0].CommitbriefCommit != "commit-b" {
		t.Errorf("after replace: CommitbriefCommit = %q, want commit-b", bf.Results[0].CommitbriefCommit)
	}
	if !bf.Results[0].Eligible {
		t.Errorf("after replace: row not eligible even though its own prompt hash matches the write's current hash")
	}

	// A THIRD write for a different provider/model, still under "sha-new"
	// (the current hash at write time) — the surviving row from write #2
	// must be recomputed too: its hash ("sha-new") still matches, so it
	// stays eligible. This pins "every row recomputed on every write," not
	// just the one just written.
	row3, err := BuildRow(rows, testPricing(), "sha-new", "commit-c", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	row3.Model = "m2"
	if err := WriteResult(path, corpus, 3, thresholds, row3); err != nil {
		t.Fatalf("WriteResult #3 (different key): %v", err)
	}
	bf, _, err = LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(bf.Results) != 2 {
		t.Fatalf("after append: len(Results) = %d, want 2 (different provider/model key)", len(bf.Results))
	}
	for _, r := range bf.Results {
		if !r.Eligible {
			t.Errorf("row %s/%s not eligible after write #3 — all rows share prompt hash sha-new", r.Provider, r.Model)
		}
	}

	// Now write a FOURTH row under yet another new hash ("sha-newer") — the
	// two prior rows (hash "sha-new") must now be recomputed ineligible,
	// since "current" prompt hash for eligibility purposes is the hash of
	// whichever row is being written now.
	row4, err := BuildRow(rows, testPricing(), "sha-newer", "commit-d", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	row4.Model = "m3"
	if err := WriteResult(path, corpus, 3, thresholds, row4); err != nil {
		t.Fatalf("WriteResult #4: %v", err)
	}
	bf, _, err = LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(bf.Results) != 3 {
		t.Fatalf("after write #4: len(Results) = %d, want 3", len(bf.Results))
	}
	for _, r := range bf.Results {
		wantEligible := r.Model == "m3"
		if r.Eligible != wantEligible {
			t.Errorf("row %s/%s: Eligible = %v, want %v (prompt hash %q vs current sha-newer)", r.Provider, r.Model, r.Eligible, wantEligible, r.PromptSHA256)
		}
	}
}

// TestResultsWriteResultRefusesFingerprintMismatch pins the "one results
// file = one corpus" invariant: writing a row measured against a different
// corpus fingerprint must fail rather than silently mixing evidence.
func TestResultsWriteResultRefusesFingerprintMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")

	row, err := BuildRow([]Scorecard{perfectScorecard("mock", "m1", 20, 20)}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, testCorpus("fp-1", 20, 20), 1, Thresholds{}, row); err != nil {
		t.Fatalf("WriteResult #1: %v", err)
	}

	row2, err := BuildRow([]Scorecard{perfectScorecard("mock", "m2", 20, 20)}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	err = WriteResult(path, testCorpus("fp-2", 20, 20), 1, Thresholds{}, row2)
	if err == nil {
		t.Fatal("WriteResult with a different corpus fingerprint = nil error, want a refusal")
	}
}

// TestResultsWriteResultRefusesRunsMismatch pins the companion refusal: a
// results file holds evidence measured at one run count k, so a write at a
// different k must also fail rather than mixing spreads that mean
// different things (runs=1 has no min/max spread; runs=5 does).
func TestResultsWriteResultRefusesRunsMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")
	corpus := testCorpus("fp-1", 20, 20)

	row, err := BuildRow([]Scorecard{perfectScorecard("mock", "m1", 20, 20)}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, corpus, 1, Thresholds{}, row); err != nil {
		t.Fatalf("WriteResult #1: %v", err)
	}

	row2, err := BuildRow([]Scorecard{perfectScorecard("mock", "m2", 20, 20)}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	err = WriteResult(path, corpus, 3, Thresholds{}, row2)
	if err == nil {
		t.Fatal("WriteResult with a different runs count = nil error, want a refusal")
	}
}

// TestResultsWriteResultDeterministicOrder pins sorted-by-(provider,model)
// output regardless of write order, so two independent `make eval-live`
// invocations that write the same set of rows in a different order produce
// byte-identical files.
func TestResultsWriteResultDeterministicOrder(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")
	corpus := testCorpus("fp-1", 20, 20)

	keys := []struct{ provider, model string }{
		{"openai", "gpt"}, {"anthropic", "claude"}, {"anthropic", "haiku"}, {"gemini", "flash"},
	}
	for _, k := range keys {
		row, err := BuildRow([]Scorecard{perfectScorecard(k.provider, k.model, 20, 20)}, testPricing(), "sha", "commit", time.Now())
		if err != nil {
			t.Fatalf("BuildRow: %v", err)
		}
		if err := WriteResult(path, corpus, 1, Thresholds{}, row); err != nil {
			t.Fatalf("WriteResult(%s/%s): %v", k.provider, k.model, err)
		}
	}

	bf, _, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(bf.Results) != len(keys) {
		t.Fatalf("len(Results) = %d, want %d", len(bf.Results), len(keys))
	}
	for i := 1; i < len(bf.Results); i++ {
		prev, cur := bf.Results[i-1], bf.Results[i]
		if prev.Provider > cur.Provider || (prev.Provider == cur.Provider && prev.Model > cur.Model) {
			t.Errorf("Results not sorted: %s/%s before %s/%s", prev.Provider, prev.Model, cur.Provider, cur.Model)
		}
	}
}

// eligibleBreakdown builds a breakdown at exactly the §2 per-language
// sufficiency floor for runs=3: 15 distinct expected findings (45 trials,
// all caught) and 5 distinct clean fixtures (15 trials, none alarmed) per
// canonical language.
func eligibleBreakdown() *Breakdown {
	bd := &Breakdown{Language: map[string]BreakdownEntry{}, Severity: map[string]BreakdownEntry{}}
	for _, lang := range CanonicalLanguages() {
		bd.Language[lang] = BreakdownEntry{Recall: newRateStat(45, 45, 3), FPR: newRateStat(0, 15, 3)}
	}
	return bd
}

// TestResultsComputeEligible pins the independent eligibility gates
// (ADR-0043 §1/§2 as re-derived by §Update 2026-10-25): run/corpus
// sufficiency, per-language coverage, the recall floor, the per-language
// recall guard, the fpr ceiling, and the prompt-hash match — each alone must
// be able to flip eligibility false, and each boundary is pinned on both
// sides.
func TestResultsComputeEligible(t *testing.T) {
	newBase := func() BenchmarkRow {
		return BenchmarkRow{Runs: 3, Recall: 0.95, FPR: 0.05, PromptSHA256: "sha-x", Breakdown: eligibleBreakdown()}
	}
	corpus := CorpusInfo{NBuggy: 90, NClean: 30}
	th := DefaultThresholds()
	langCell := func(row BenchmarkRow, lang string, e BreakdownEntry) BenchmarkRow {
		row.Breakdown.Language[lang] = e
		return row
	}

	tests := []struct {
		name   string
		row    func() BenchmarkRow
		corpus CorpusInfo
		want   bool
	}{
		{"baseline at the sufficiency floor", newBase, corpus, true},
		{"runs=2", func() BenchmarkRow { r := newBase(); r.Runs = 2; return r }, corpus, false},
		{"n_buggy=89", newBase, CorpusInfo{NBuggy: 89, NClean: 30}, false},
		{"n_clean=29", newBase, CorpusInfo{NBuggy: 90, NClean: 29}, false},
		{"no breakdown", func() BenchmarkRow { r := newBase(); r.Breakdown = nil; return r }, corpus, false},
		{"language absent from breakdown", func() BenchmarkRow {
			r := newBase()
			delete(r.Breakdown.Language, "php")
			return r
		}, corpus, false},
		{"language with 9 expected findings", func() BenchmarkRow {
			return langCell(newBase(), "java", BreakdownEntry{Recall: newRateStat(27, 27, 3), FPR: newRateStat(0, 15, 3)})
		}, corpus, false},
		{"language with exactly 10 expected findings", func() BenchmarkRow {
			return langCell(newBase(), "java", BreakdownEntry{Recall: newRateStat(30, 30, 3), FPR: newRateStat(0, 15, 3)})
		}, corpus, true},
		{"language with 2 clean fixtures", func() BenchmarkRow {
			return langCell(newBase(), "python", BreakdownEntry{Recall: newRateStat(45, 45, 3), FPR: newRateStat(0, 6, 3)})
		}, corpus, false},
		{"language with exactly 3 clean fixtures", func() BenchmarkRow {
			return langCell(newBase(), "python", BreakdownEntry{Recall: newRateStat(45, 45, 3), FPR: newRateStat(0, 9, 3)})
		}, corpus, true},
		{"language recall 31/45 (below 0.70)", func() BenchmarkRow {
			return langCell(newBase(), "php", BreakdownEntry{Recall: newRateStat(31, 45, 3), FPR: newRateStat(0, 15, 3)})
		}, corpus, false},
		{"language recall 32/45 (above 0.70)", func() BenchmarkRow {
			return langCell(newBase(), "php", BreakdownEntry{Recall: newRateStat(32, 45, 3), FPR: newRateStat(0, 15, 3)})
		}, corpus, true},
		{"language recall exactly 21/30 (== 0.70, inclusive)", func() BenchmarkRow {
			return langCell(newBase(), "php", BreakdownEntry{Recall: newRateStat(21, 30, 3), FPR: newRateStat(0, 15, 3)})
		}, corpus, true},
		{"language recall rate undefined", func() BenchmarkRow {
			return langCell(newBase(), "go", BreakdownEntry{Recall: RateStat{NDistinct: 15}, FPR: newRateStat(0, 15, 3)})
		}, corpus, false},
		{"recall 267/297 (below 0.90)", func() BenchmarkRow { r := newBase(); r.Recall = 267.0 / 297.0; return r }, corpus, false},
		{"recall 268/297 (above 0.90)", func() BenchmarkRow { r := newBase(); r.Recall = 268.0 / 297.0; return r }, corpus, true},
		{"fpr 10/99 (above 0.10)", func() BenchmarkRow { r := newBase(); r.FPR = 10.0 / 99.0; return r }, corpus, false},
		{"fpr 9/99 (below 0.10)", func() BenchmarkRow { r := newBase(); r.FPR = 9.0 / 99.0; return r }, corpus, true},
		{"stale prompt hash", func() BenchmarkRow { r := newBase(); r.PromptSHA256 = "sha-old"; return r }, corpus, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := computeEligible(tt.row(), tt.corpus, th, "sha-x"); got != tt.want {
				t.Errorf("computeEligible = %v, want %v", got, tt.want)
			}
		})
	}

	// The per-language floor comes from the thresholds parameter, not from
	// the package constant: the same row flips with a different value.
	php31 := langCell(newBase(), "php", BreakdownEntry{Recall: newRateStat(31, 45, 3), FPR: newRateStat(0, 15, 3)})
	loose := th
	loose.LanguageRecallMin = 0.60
	if !computeEligible(php31, corpus, loose, "sha-x") {
		t.Error("31/45 should be eligible when thresholds.LanguageRecallMin = 0.60: the floor must come from thresholds")
	}
}

// TestResultsLanguageGuardIndependentOfOverallRecall pins the reason the
// guard exists: a model missing 5 of 16 PHP defects keeps overall recall
// above 0.90 yet must not be eligible.
func TestResultsLanguageGuardIndependentOfOverallRecall(t *testing.T) {
	sc := coveredScorecard("mock", "weak-php")
	missed := 0
	for i := range sc.Fixtures {
		if sc.Fixtures[i].Language == "php" && sc.Fixtures[i].IsBuggy() && missed < 5 {
			sc.Fixtures[i].TruePositives = 0
			sc.Fixtures[i].FalseNegatives = 1
			missed++
		}
	}
	row, err := BuildRow(threeRuns(sc), testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if row.Recall < RecallMinValue {
		t.Fatalf("overall recall = %v, want >= %v for this scenario", row.Recall, RecallMinValue)
	}
	if computeEligible(row, coveredCorpus("fp"), DefaultThresholds(), "sha") {
		t.Errorf("php recall 11/16 should make the row ineligible despite overall recall %v", row.Recall)
	}
	// A clean copy of the same scenario is eligible, so the PHP misses alone
	// flipped the verdict.
	good, err := BuildRow(threeRuns(coveredScorecard("mock", "good")), testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if !computeEligible(good, coveredCorpus("fp"), DefaultThresholds(), "sha") {
		t.Error("a fully covered perfect row should be eligible")
	}
}

// TestResultsMinMax pins the small helper used for the recall/fpr spread
// columns, including its documented empty-slice convention (0,0).
func TestResultsMinMax(t *testing.T) {
	lo, hi := minMax(nil)
	if lo != 0 || hi != 0 {
		t.Errorf("minMax(nil) = %v,%v, want 0,0", lo, hi)
	}
	lo, hi = minMax([]float64{0.5})
	if lo != 0.5 || hi != 0.5 {
		t.Errorf("minMax([0.5]) = %v,%v, want 0.5,0.5", lo, hi)
	}
	lo, hi = minMax([]float64{0.7, 0.3, 0.9, 0.1})
	if lo != 0.1 || hi != 0.9 {
		t.Errorf("minMax(...) = %v,%v, want 0.1,0.9", lo, hi)
	}
	if math.IsNaN(lo) || math.IsNaN(hi) {
		t.Error("minMax produced NaN")
	}
}

// TestResultsWriteResultRefusesAllErroredOverwrite pins the guard at
// results.go:399-406: a new row that errored on every attempted review must
// not blow away an existing row for the same provider/model that holds real
// evidence, even though WriteResult otherwise replaces same-key rows
// outright (re-review 1, MINOR-C).
func TestResultsWriteResultRefusesAllErroredOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")
	corpus := testCorpus("fp-1", 20, 20)
	thresholds := Thresholds{RecallMin: 0.5, FPRMax: 0.5}

	good, err := BuildRow([]Scorecard{perfectScorecard("mock", "m1", 20, 20)}, testPricing(), "sha", "commit-a", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, corpus, 1, thresholds, good); err != nil {
		t.Fatalf("WriteResult #1 (proven row): %v", err)
	}

	allErrored := good
	allErrored.Errors = corpus.NFixtures // runs=1 * n_fixtures=40
	allErrored.CommitbriefCommit = "commit-b"
	err = WriteResult(path, corpus, 1, thresholds, allErrored)
	if err == nil {
		t.Fatal("WriteResult with an all-errored row over a proven row = nil error, want a refusal")
	}

	bf, _, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if len(bf.Results) != 1 || bf.Results[0].CommitbriefCommit != "commit-a" {
		t.Errorf("after refused write: Results = %+v, want the original proven row (commit-a) untouched", bf.Results)
	}
}

// TestResultsWriteResultRefusesNonFinite pins nonFiniteField's use inside
// WriteResult: a row with a NaN metric must be rejected rather than written
// (encoding/json cannot marshal NaN/Inf) (re-review 1, MINOR-C).
func TestResultsWriteResultRefusesNonFinite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")
	corpus := testCorpus("fp-1", 20, 20)

	row, err := BuildRow([]Scorecard{perfectScorecard("mock", "m1", 20, 20)}, testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	row.Recall = math.NaN()

	err = WriteResult(path, corpus, 1, Thresholds{}, row)
	if err == nil {
		t.Fatal("WriteResult with a NaN field = nil error, want a refusal")
	}

	if _, existed, _ := LoadResults(path); existed {
		t.Error("WriteResult must not create the file when the row is rejected for a non-finite field")
	}
}

// TestResultsLoadResultsRefusesFutureSchema pins LoadResults's refusal
// (results.go:320-322) of a schema it doesn't recognize, so older code never
// silently loads-and-rewrites a future schema bump down to schema:1
// (re-review 1, MINOR-C).
func TestResultsLoadResultsRefusesFutureSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "benchmark.json")
	if err := os.WriteFile(path, []byte(`{"schema":2,"results":[]}`), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	_, existed, err := LoadResults(path)
	if err == nil {
		t.Fatal("LoadResults(schema:2) = nil error, want an unsupported-schema refusal")
	}
	if !existed {
		t.Error("existed = false for a file that does exist, just with an unsupported schema")
	}
}

// TestResultsDefaultThresholds pins ADR-0043 §2's gate values so a change to
// RecallMinValue/FPRMaxValue is caught here rather than only showing up as a
// drifted results file (re-review 1, MINOR-C).
func TestResultsDefaultThresholds(t *testing.T) {
	th := DefaultThresholds()
	if th.RecallMin != 0.90 || th.FPRMax != 0.10 || th.LanguageRecallMin != 0.70 {
		t.Errorf("DefaultThresholds() = %+v, want {RecallMin:0.90 FPRMax:0.10 LanguageRecallMin:0.70}", th)
	}
	if MinRuns != 3 || MinBuggy != 90 || MinClean != 30 || MinExpectedPerLanguage != 10 || MinCleanPerLanguage != 3 {
		t.Errorf("sufficiency constants drifted from ADR-0043 §Update 2026-10-25: runs=%d buggy=%d clean=%d perLangExpected=%d perLangClean=%d",
			MinRuns, MinBuggy, MinClean, MinExpectedPerLanguage, MinCleanPerLanguage)
	}
}

// TestResultsLanguageRecallMinRoundTrip pins ADR-0043 §Update 2026-10-25's
// T1: thresholds.language_recall_min is written to the file, survives a
// load and a rewrite, and is the value eligible was computed with. A file
// written before the key existed is re-judged — and re-stamped — on the next
// write.
func TestResultsLanguageRecallMinRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "benchmark.json")
	corpus := coveredCorpus("fp-rt")
	th := DefaultThresholds()

	row, err := BuildRow(threeRuns(coveredScorecard("mock", "m1")), testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, corpus, 3, th, row); err != nil {
		t.Fatalf("WriteResult #1: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var generic struct {
		Thresholds map[string]float64 `json:"thresholds"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, ok := generic.Thresholds["language_recall_min"]; !ok || got != 0.70 {
		t.Errorf("file thresholds.language_recall_min = %v (present=%v), want 0.70", got, ok)
	}

	bf, _, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if bf.Thresholds != th {
		t.Errorf("loaded Thresholds = %+v, want %+v", bf.Thresholds, th)
	}
	if len(bf.Results) != 1 || !bf.Results[0].Eligible {
		t.Fatalf("row not eligible after write with the default thresholds: %+v", bf.Results)
	}

	// Rewrite with a second row: the floor is preserved in the file.
	row2, err := BuildRow(threeRuns(coveredScorecard("mock", "m2")), testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if err := WriteResult(path, corpus, 3, DefaultThresholds(), row2); err != nil {
		t.Fatalf("WriteResult #2: %v", err)
	}
	bf, _, err = LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if bf.Thresholds.LanguageRecallMin != 0.70 {
		t.Errorf("after rewrite LanguageRecallMin = %v, want 0.70", bf.Thresholds.LanguageRecallMin)
	}

	// A pre-update file has no such key: it loads as zero and is re-stamped
	// by the next write.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	var th2 map[string]json.RawMessage
	if err := json.Unmarshal(doc["thresholds"], &th2); err != nil {
		t.Fatalf("unmarshal thresholds: %v", err)
	}
	delete(th2, "language_recall_min")
	if doc["thresholds"], err = json.Marshal(th2); err != nil {
		t.Fatalf("marshal thresholds: %v", err)
	}
	legacy, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal doc: %v", err)
	}
	if strings.Contains(string(legacy), "language_recall_min") {
		t.Fatal("test setup: legacy file still carries language_recall_min")
	}
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatalf("WriteFile legacy: %v", err)
	}
	bf, _, err = LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults legacy: %v", err)
	}
	if bf.Thresholds.LanguageRecallMin != 0 {
		t.Errorf("legacy file LanguageRecallMin = %v, want 0", bf.Thresholds.LanguageRecallMin)
	}
	if err := WriteResult(path, corpus, 3, DefaultThresholds(), row2); err != nil {
		t.Fatalf("WriteResult legacy rewrite: %v", err)
	}
	bf, _, err = LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if bf.Thresholds.LanguageRecallMin != 0.70 {
		t.Errorf("re-stamped LanguageRecallMin = %v, want 0.70", bf.Thresholds.LanguageRecallMin)
	}

	// eligible is computed with the value written to the file (T1: one
	// source). A row with php recall 33/48 (0.6875) is ineligible at 0.70 and
	// eligible at 0.60, and the file's thresholds.language_recall_min moves
	// with the verdict. Overall recall stays above 0.90, so only the language
	// guard decides.
	weak := coveredScorecard("mock", "weak-php")
	missed := 0
	for i := range weak.Fixtures {
		if weak.Fixtures[i].Language == "php" && weak.Fixtures[i].IsBuggy() && missed < 5 {
			weak.Fixtures[i].TruePositives = 0
			weak.Fixtures[i].FalseNegatives = 1
			missed++
		}
	}
	if missed != 5 {
		t.Fatalf("test setup: %d php fixtures missed per run, want 5", missed)
	}
	weakRow, err := BuildRow(threeRuns(weak), testPricing(), "sha", "commit", time.Now())
	if err != nil {
		t.Fatalf("BuildRow weak: %v", err)
	}
	eligibleAt := func(floor float64) (fileFloor float64, eligible bool) {
		t.Helper()
		thr := DefaultThresholds()
		thr.LanguageRecallMin = floor
		if err := WriteResult(path, corpus, 3, thr, weakRow); err != nil {
			t.Fatalf("WriteResult weak @%v: %v", floor, err)
		}
		bf, _, err := LoadResults(path)
		if err != nil {
			t.Fatalf("LoadResults: %v", err)
		}
		for _, r := range bf.Results {
			if r.Model == "weak-php" {
				return bf.Thresholds.LanguageRecallMin, r.Eligible
			}
		}
		t.Fatal("weak-php row missing")
		return 0, false
	}
	if f, ok := eligibleAt(0.70); f != 0.70 || ok {
		t.Errorf("@0.70: file floor=%v eligible=%v, want 0.70/false", f, ok)
	}
	if f, ok := eligibleAt(0.60); f != 0.60 || !ok {
		t.Errorf("@0.60: file floor=%v eligible=%v, want 0.60/true", f, ok)
	}
}

// TestWilsonInterval pins the pure Wilson (z = 1.96) function. Boundary rows
// use the closed forms (k=0 → upper z²/(n+z²), k=n → lower n/(n+z²)); the
// 10/20 and 5/10 rows are the textbook values.
func TestWilsonInterval(t *testing.T) {
	const z2 = 1.96 * 1.96
	tests := []struct {
		name       string
		k, n       int
		lo, hi     float64
		wantDefine bool
	}{
		{"zero of twenty", 0, 20, 0, z2 / (20 + z2), true},
		{"twenty of twenty", 20, 20, 20 / (20 + z2), 1, true},
		{"ten of twenty", 10, 20, 0.2993, 0.7007, true},
		{"five of ten", 5, 10, 0.2366, 0.7634, true},
		{"one of one", 1, 1, 0.2065, 1, true},
		{"n zero undefined", 0, 0, 0, 0, false},
		{"negative n undefined", 0, -1, 0, 0, false},
		{"k above n undefined", 5, 4, 0, 0, false},
		{"negative k undefined", -1, 4, 0, 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lo, hi, ok := WilsonInterval(tc.k, tc.n)
			if ok != tc.wantDefine {
				t.Fatalf("ok = %v, want %v", ok, tc.wantDefine)
			}
			if !ok {
				return
			}
			if math.Abs(lo-tc.lo) > 1e-3 || math.Abs(hi-tc.hi) > 1e-3 {
				t.Errorf("WilsonInterval(%d, %d) = [%.4f, %.4f], want [%.4f, %.4f]", tc.k, tc.n, lo, hi, tc.lo, tc.hi)
			}
			if lo < 0 || hi > 1 || lo > hi || math.IsNaN(lo) || math.IsNaN(hi) {
				t.Errorf("interval [%v, %v] not within [0,1] / ordered", lo, hi)
			}
		})
	}
}

// TestRateStatLowNAndUndefined pins the n < 20 flag (on distinct units, not
// pooled trials) and the n = 0 contract: null rate and interval, never NaN.
func TestRateStatLowNAndUndefined(t *testing.T) {
	if rs := newRateStat(19, 19, 1); !rs.LowN {
		t.Error("n=19 must be low_n")
	}
	if rs := newRateStat(20, 20, 1); rs.LowN {
		t.Error("n=20 must not be low_n")
	}
	// 3 runs × 10 distinct units = pooled n of 30, but only 10 independent
	// units: still low_n, and the interval is the n=10 one.
	rs := newRateStat(15, 30, 3)
	if !rs.LowN || rs.NDistinct != 10 {
		t.Errorf("pooled 30 over 3 runs: LowN=%v NDistinct=%d, want true/10", rs.LowN, rs.NDistinct)
	}
	wantLo, wantHi, _ := WilsonInterval(5, 10)
	if rs.WilsonLow == nil || math.Abs(*rs.WilsonLow-wantLo) > 1e-3 || math.Abs(*rs.WilsonHigh-wantHi) > 1e-3 {
		t.Errorf("pooled interval = %v..%v, want the n=10 interval %.4f..%.4f", rs.WilsonLow, rs.WilsonHigh, wantLo, wantHi)
	}

	empty := newRateStat(0, 0, 1)
	if empty.Rate != nil || empty.WilsonLow != nil || empty.WilsonHigh != nil || !empty.LowN {
		t.Errorf("n=0 stat = %+v, want null rate/interval and low_n", empty)
	}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	for _, k := range []string{"rate", "wilson_low", "wilson_high"} {
		if v, ok := m[k]; !ok || v != nil {
			t.Errorf("n=0 JSON %s = %v (present=%v), want explicit null", k, v, ok)
		}
	}
}

func breakdownFixtureScores() Scorecard {
	return Scorecard{Provider: "p", Model: "m", Fixtures: []FixtureScore{
		{Fixture: "a-go", Language: "go", TruePositives: 2, FalseNegatives: 1,
			CaughtBySeverity: map[string]int{"high": 2}, MissedBySeverity: map[string]int{"critical": 1}},
		{Fixture: "b-python", Language: "python", TruePositives: 1,
			CaughtBySeverity: map[string]int{"unspecified": 1}},
		{Fixture: "clean-go", Language: "go"},
		{Fixture: "clean-go-medium", Language: "go", CleanAlarm: true, MaxProducedSeverity: render.SeverityMedium},
		{Fixture: "clean-ts-low", Language: "typescript", CleanAlarm: true, MaxProducedSeverity: render.SeverityLow},
		{Fixture: "clean-ts-err", Language: "typescript", CleanAlarm: true, Errored: true},
	}}
}

// TestBuildBreakdownCounts pins per-language and per-severity attribution,
// the severity-floor fpr semantics (low floor == headline CleanAlarm; an
// errored clean run alarms at every floor), and the always-present
// canonical-language rows with n = 0 for languages the corpus lacks.
func TestBuildBreakdownCounts(t *testing.T) {
	bd := BuildBreakdown([]Scorecard{breakdownFixtureScores()})

	check := func(label string, rs RateStat, k, n int) {
		t.Helper()
		if rs.K != k || rs.N != n {
			t.Errorf("%s = %d/%d, want %d/%d", label, rs.K, rs.N, k, n)
		}
	}
	check("go recall", bd.Language["go"].Recall, 2, 3)
	check("go fpr", bd.Language["go"].FPR, 1, 2)
	check("python recall", bd.Language["python"].Recall, 1, 1)
	check("python fpr", bd.Language["python"].FPR, 0, 0)
	check("typescript fpr", bd.Language["typescript"].FPR, 2, 2)

	for _, l := range []string{"java", "php"} {
		e, ok := bd.Language[l]
		if !ok {
			t.Fatalf("language %q missing from breakdown", l)
		}
		if e.Recall.N != 0 || e.Recall.Rate != nil || !e.Recall.LowN || e.FPR.N != 0 {
			t.Errorf("%s = %+v, want empty low_n cells", l, e)
		}
	}
	if _, ok := bd.Language["unknown"]; ok {
		t.Error("unknown language row present with no unknown fixtures")
	}

	check("critical recall", bd.Severity["critical"].Recall, 0, 1)
	check("high recall", bd.Severity["high"].Recall, 2, 2)
	check("unspecified recall", bd.Severity[severityUnspecified].Recall, 1, 1)
	check("medium recall (none)", bd.Severity["medium"].Recall, 0, 0)

	// 4 clean fixtures: go (silent), go (medium), ts (low), ts (errored).
	check("fpr floor low", bd.Severity["low"].FPR, 3, 4)
	check("fpr floor medium", bd.Severity["medium"].FPR, 2, 4)
	check("fpr floor high", bd.Severity["high"].FPR, 1, 4)
	check("fpr floor critical", bd.Severity["critical"].FPR, 1, 4)
	check("fpr info (never a floor)", bd.Severity["info"].FPR, 0, 0)
}

// TestBuildBreakdownPoolsRuns pins sum-then-divide across runs and that the
// distinct-unit n (what low_n and the interval use) stays per-run.
func TestBuildBreakdownPoolsRuns(t *testing.T) {
	one := breakdownFixtureScores()
	bd := BuildBreakdown([]Scorecard{one, one, one})
	rs := bd.Language["go"].Recall
	if rs.K != 6 || rs.N != 9 || rs.NDistinct != 3 {
		t.Errorf("go recall pooled = k%d n%d distinct%d, want k6 n9 distinct3", rs.K, rs.N, rs.NDistinct)
	}
	if rs.Rate == nil || *rs.Rate != round4(2.0/3.0) {
		t.Errorf("go recall rate = %v, want %v", rs.Rate, round4(2.0/3.0))
	}
}

// TestBuildRowCarriesBreakdown pins the additive breakdown on a row, that
// the headline fpr equals the low-floor fpr, and that the row stays
// finite-clean (no NaN reaches the JSON for n = 0 cells).
func TestBuildRowCarriesBreakdown(t *testing.T) {
	sc := breakdownFixtureScores()
	row, err := BuildRow([]Scorecard{sc}, testPricing(), "sha", "c", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	if row.Breakdown == nil {
		t.Fatal("row.Breakdown is nil")
	}
	low := row.Breakdown.Severity["low"].FPR
	if low.Rate == nil || *low.Rate != row.FPR {
		t.Errorf("low-floor fpr = %v, headline fpr = %v, want equal", low.Rate, row.FPR)
	}
	if f := nonFiniteField(row); f != "" {
		t.Errorf("nonFiniteField = %q, want none", f)
	}
	if _, err := json.Marshal(row); err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	nan := math.NaN()
	bad := row
	bd := *row.Breakdown
	bd.Language = map[string]BreakdownEntry{"go": {Recall: RateStat{Rate: &nan}}}
	bad.Breakdown = &bd
	if f := nonFiniteField(bad); f != "breakdown.language.go.recall.rate" {
		t.Errorf("nonFiniteField with NaN breakdown rate = %q", f)
	}
}

// TestResultsLoadsRowWithoutBreakdown pins backwards compatibility: a row
// written before the additive breakdown existed still loads (schema 1).
func TestResultsLoadsRowWithoutBreakdown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.json")
	old := `{"schema":1,"generated_at":"x","corpus":{"fingerprint":"f","n_fixtures":0,"n_buggy":0,"n_clean":0,"n_expected":0,"languages":{}},"runs":1,"thresholds":{"recall_min":0.9,"fpr_max":0.1},"results":[{"provider":"p","model":"m"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	bf, _, err := LoadResults(path)
	if err != nil {
		t.Fatalf("LoadResults: %v", err)
	}
	if bf.Results[0].Breakdown != nil {
		t.Error("old row grew a breakdown on load")
	}
}

// TestFormatBreakdown pins the console markers: every ratio shows k/n and
// the interval, and a low-n or empty cell carries "(n<20)".
func TestFormatBreakdown(t *testing.T) {
	out := strings.Join(FormatBreakdown(BuildBreakdown([]Scorecard{breakdownFixtureScores()})), "\n")
	for _, want := range []string{"by language", "by severity", "go ", "java", "php", "(n<20)", "n/a (n=0)", "2/3", "["} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatBreakdown output missing %q:\n%s", want, out)
		}
	}
}
