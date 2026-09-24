// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
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
		"silence_violation_rate", "errors",
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
	corpus := testCorpus("fp-1", 20, 20)
	thresholds := Thresholds{RecallMin: 0.5, FPRMax: 0.5}

	first := perfectScorecard("mock", "m1", 20, 20)
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
		t.Fatalf("after first write: row not eligible (runs=3, n=20/20, recall=1>=0.5, fpr=0<=0.5, prompt matches itself); row=%+v", bf.Results[0])
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

// TestResultsComputeEligible pins the four independent eligibility gates
// (ADR-0043 §1/§2): run/corpus sufficiency, the recall floor, the fpr
// ceiling, and the prompt-hash match — each alone must be able to flip
// eligibility false.
func TestResultsComputeEligible(t *testing.T) {
	base := BenchmarkRow{Runs: 3, Recall: 0.95, FPR: 0.05, PromptSHA256: "sha-x"}
	corpus := CorpusInfo{NBuggy: 20, NClean: 20}
	th := Thresholds{RecallMin: 0.9, FPRMax: 0.1}

	if !computeEligible(base, corpus, th, "sha-x") {
		t.Error("baseline row should be eligible")
	}

	insufficientRuns := base
	insufficientRuns.Runs = 2
	if computeEligible(insufficientRuns, corpus, th, "sha-x") {
		t.Error("runs=2 should be ineligible (D-W2-4 needs runs>=3)")
	}

	smallCorpus := corpus
	smallCorpus.NBuggy = 19
	if computeEligible(base, smallCorpus, th, "sha-x") {
		t.Error("n_buggy=19 should be ineligible (needs >=20)")
	}
	smallCorpus2 := corpus
	smallCorpus2.NClean = 19
	if computeEligible(base, smallCorpus2, th, "sha-x") {
		t.Error("n_clean=19 should be ineligible (needs >=20)")
	}

	lowRecall := base
	lowRecall.Recall = 0.5
	if computeEligible(lowRecall, corpus, th, "sha-x") {
		t.Error("recall below threshold should be ineligible")
	}

	highFPR := base
	highFPR.FPR = 0.5
	if computeEligible(highFPR, corpus, th, "sha-x") {
		t.Error("fpr above threshold should be ineligible")
	}

	staleRow := base
	staleRow.PromptSHA256 = "sha-old"
	if computeEligible(staleRow, corpus, th, "sha-x") {
		t.Error("a row measured under a different prompt hash than current should be ineligible")
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
	if th.RecallMin != 0.90 || th.FPRMax != 0.10 {
		t.Errorf("DefaultThresholds() = %+v, want {RecallMin:0.90 FPRMax:0.10}", th)
	}
}
