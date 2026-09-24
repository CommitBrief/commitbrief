// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

// RecallMinValue and FPRMaxValue are ADR-0043 §2's recall/fpr gate "in
// force today" (0.90/0.10). §1.5 forbids relaxing a threshold for a single
// run — env/flag overrides would make exactly that silent and
// undetectable from the results file, so these are package constants, not
// a knob. Changing them requires a dated §Update to ADR-0043 (review
// 2026-09-24, BLOCKER).
const (
	RecallMinValue = 0.90
	FPRMaxValue    = 0.10
)

// DefaultThresholds returns the ADR-0043 §2 gate every WriteResult call
// asserts against.
func DefaultThresholds() Thresholds {
	return Thresholds{RecallMin: RecallMinValue, FPRMax: FPRMaxValue}
}

// Thresholds mirrors D-W2-4's recall/fpr gate for ADR-0043 §1 as stored in
// a results file. Always populated from DefaultThresholds (ADR-0043 §2) —
// see its doc comment.
type Thresholds struct {
	RecallMin float64 `json:"recall_min"`
	FPRMax    float64 `json:"fpr_max"`
}

// CorpusInfo identifies which corpus a results file's rows were measured
// against (ADR-0043 §4). Fingerprint is the authority WriteResult uses to
// refuse mixing two different corpora into one file.
type CorpusInfo struct {
	Fingerprint string         `json:"fingerprint"`
	NFixtures   int            `json:"n_fixtures"`
	NBuggy      int            `json:"n_buggy"`
	NClean      int            `json:"n_clean"`
	NExpected   int            `json:"n_expected"`
	Languages   map[string]int `json:"languages"`
}

// NewCorpusInfo summarizes a fixture slice into a results file's corpus
// block. fingerprint is computed separately (CorpusFingerprint) so every
// row written in the same eval-live invocation shares one value even
// though ADR-0043 §3 model-selection metrics score the whole corpus, not
// the dev/held-out split ADR-0018 uses for tuning-safety diagnostics.
func NewCorpusInfo(fixtures []Fixture, fingerprint string) CorpusInfo {
	nBuggy, nExpected := 0, 0
	for _, fx := range fixtures {
		if len(fx.Expected) > 0 {
			nBuggy++
		}
		nExpected += len(fx.Expected)
	}
	return CorpusInfo{
		Fingerprint: fingerprint,
		NFixtures:   len(fixtures),
		NBuggy:      nBuggy,
		NClean:      len(fixtures) - nBuggy,
		NExpected:   nExpected,
		Languages:   LanguageDistribution(fixtures),
	}
}

// RowPricing is the pricing snapshot a results row was costed against —
// copied from provider.Pricing rather than reusing it directly so a
// pricing-struct field this package doesn't emit (there are none today,
// but the type is provider-owned) never leaks into the results schema by
// accident.
type RowPricing struct {
	InputPer1M  float64 `json:"input_per_1m"`
	OutputPer1M float64 `json:"output_per_1m"`
}

// BenchmarkRow is one provider+model measurement (ADR-0043 §4).
type BenchmarkRow struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`

	MeasuredAt        string `json:"measured_at"`
	PromptSHA256      string `json:"prompt_sha256"`
	CommitbriefCommit string `json:"commitbrief_commit"`

	Runs      int `json:"runs"`
	NBuggy    int `json:"n_buggy"`
	NClean    int `json:"n_clean"`
	NExpected int `json:"n_expected"`

	Recall               float64 `json:"recall"`
	RecallMin            float64 `json:"recall_min"`
	RecallMax            float64 `json:"recall_max"`
	Precision            float64 `json:"precision"`
	FPR                  float64 `json:"fpr"`
	FPRMin               float64 `json:"fpr_min"`
	FPRMax               float64 `json:"fpr_max"`
	SilenceViolationRate float64 `json:"silence_violation_rate"`
	Errors               int     `json:"errors"`

	InputTokens       float64 `json:"input_tokens"`
	OutputTokens      float64 `json:"output_tokens"`
	CachedInputTokens float64 `json:"cached_input_tokens"`

	Pricing      RowPricing `json:"pricing"`
	USDPerReview float64    `json:"usd_per_review"`

	Eligible bool `json:"eligible"`
}

// BenchmarkFile is the whole ADR-0043 §4 results document — one corpus's
// worth of evidence across every measured provider+model.
type BenchmarkFile struct {
	Schema      int            `json:"schema"`
	GeneratedAt string         `json:"generated_at"`
	Corpus      CorpusInfo     `json:"corpus"`
	Runs        int            `json:"runs"`
	Thresholds  Thresholds     `json:"thresholds"`
	Results     []BenchmarkRow `json:"results"`
}

// round4 / round6 fix a float64's decimal precision before it's stored, so
// the JSON on disk never carries binary-float noise (0.8999999999999999)
// that would make two otherwise-identical runs diff.
func round4(f float64) float64 { return math.Round(f*1e4) / 1e4 }
func round6(f float64) float64 { return math.Round(f*1e6) / 1e6 }

// uncachedInputOutputCost prices usage at pricing's list InputPer1M rate for
// every input token — cached or not — and OutputPer1M for output tokens,
// deliberately bypassing provider.Pricing.Cost's cache-hit discount
// (internal/provider/pricing.go). ADR-0043 §3 prices a review at the
// uncached list rate; Usage.CachedInputTokens is documented as a subset of
// InputTokens (internal/provider/request.go), so InputTokens already covers
// every token without needing to add the cached count back in. Using
// Pricing.Cost here would let an aggressively-cached provider's row look
// artificially cheap and skew "cheapest eligible" model selection toward
// cache behavior instead of first-run cost (review 2026-09-24, BLOCKER).
func uncachedInputOutputCost(pricing provider.Pricing, u provider.Usage) float64 {
	return (float64(u.InputTokens)*pricing.InputPer1M + float64(u.OutputTokens)*pricing.OutputPer1M) / 1_000_000
}

func minMax(vs []float64) (lo, hi float64) {
	if len(vs) == 0 {
		return 0, 0
	}
	lo, hi = vs[0], vs[0]
	for _, v := range vs[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return lo, hi
}

// BuildRow pools k Scorecards — one per COMMITBRIEF_EVAL_RUNS repeat, all
// against the same corpus and provider+model — into a single ADR-0043 §4
// results row. The headline ratios (recall, precision, fpr,
// silence_violation_rate) are pooled by summing raw counts across every
// run and dividing once, not by averaging each run's own ratio, so a run
// that happened to score more evidence isn't outweighed by one that
// scored less. recall_min/max and fpr_min/max instead report the spread
// across the individual per-run ratios — that spread is exactly what a
// flaky provider shows up as, and pooling it away would hide it.
func BuildRow(scs []Scorecard, pricing provider.Pricing, promptSHA256, commit string, measuredAt time.Time) (BenchmarkRow, error) {
	if len(scs) == 0 {
		return BenchmarkRow{}, errors.New("eval: BuildRow: no scorecards")
	}

	nClean := scs[0].NClean()
	nBuggy := scs[0].NBuggy()
	nExpected := scs[0].NExpected()

	var tp, fn, fp, sv, sa, errs, cleanAlarms, totalReviews int
	var usage provider.Usage
	recalls := make([]float64, 0, len(scs))
	fprs := make([]float64, 0, len(scs))

	for _, sc := range scs {
		t, f, p, v, a := sc.totals()
		tp += t
		fn += f
		fp += p
		sv += v
		sa += a
		errs += sc.Errors()
		cleanAlarms += sc.CleanAlarms()

		u := sc.TotalUsage()
		usage.InputTokens += u.InputTokens
		usage.OutputTokens += u.OutputTokens
		usage.CachedInputTokens += u.CachedInputTokens
		totalReviews += len(sc.Fixtures)

		recalls = append(recalls, sc.Recall())
		runFPR := 0.0
		if n := sc.NClean(); n > 0 {
			runFPR = float64(sc.CleanAlarms()) / float64(n)
		}
		fprs = append(fprs, runFPR)
	}

	// Recall/precision follow Scorecard.Recall/Precision's own
	// zero-denominator convention (vacuously 1): a corpus with no buggy
	// fixtures, or a run that produced nothing, isn't a failure.
	recall := 1.0
	if tp+fn > 0 {
		recall = float64(tp) / float64(tp+fn)
	}
	precision := 1.0
	if tp+fp > 0 {
		precision = float64(tp) / float64(tp+fp)
	}
	// fpr and silence_violation_rate follow SilenceViolationRate's own
	// convention instead (zero denominator → 0, not 1): a corpus with no
	// clean controls, or no silence anchors, raised no alarms to report.
	fpr := 0.0
	if nClean > 0 {
		fpr = float64(cleanAlarms) / float64(len(scs)*nClean)
	}
	silenceRate := 0.0
	if sa > 0 {
		silenceRate = float64(sv) / float64(sa)
	}

	recallMin, recallMax := minMax(recalls)
	fprMin, fprMax := minMax(fprs)

	var meanIn, meanOut, meanCached, usd float64
	if totalReviews > 0 {
		meanIn = float64(usage.InputTokens) / float64(totalReviews)
		meanOut = float64(usage.OutputTokens) / float64(totalReviews)
		meanCached = float64(usage.CachedInputTokens) / float64(totalReviews)
		usd = uncachedInputOutputCost(pricing, usage) / float64(totalReviews)
	}

	return BenchmarkRow{
		Provider:             scs[0].Provider,
		Model:                scs[0].Model,
		MeasuredAt:           measuredAt.UTC().Format(time.RFC3339),
		PromptSHA256:         promptSHA256,
		CommitbriefCommit:    commit,
		Runs:                 len(scs),
		NBuggy:               nBuggy,
		NClean:               nClean,
		NExpected:            nExpected,
		Recall:               round4(recall),
		RecallMin:            round4(recallMin),
		RecallMax:            round4(recallMax),
		Precision:            round4(precision),
		FPR:                  round4(fpr),
		FPRMin:               round4(fprMin),
		FPRMax:               round4(fprMax),
		SilenceViolationRate: round4(silenceRate),
		Errors:               errs,
		InputTokens:          round4(meanIn),
		OutputTokens:         round4(meanOut),
		CachedInputTokens:    round4(meanCached),
		Pricing:              RowPricing{InputPer1M: pricing.InputPer1M, OutputPer1M: pricing.OutputPer1M},
		USDPerReview:         round6(usd),
	}, nil
}

// computeEligible applies ADR-0043 §1/§2 to one row: §2 sufficiency (at
// least 3 runs over a corpus with at least 20 buggy and 20 clean
// fixtures), §1's recall/fpr floor against thresholds, and a prompt match
// against whichever prompt hash the harness doing the writing just
// computed for itself. A row measured under an older prompt fails that
// last check and becomes ineligible without being deleted — inert history,
// per ADR-0043 §4.
func computeEligible(row BenchmarkRow, corpus CorpusInfo, thresholds Thresholds, currentPromptSHA256 string) bool {
	if row.Runs < 3 || corpus.NBuggy < 20 || corpus.NClean < 20 {
		return false
	}
	if row.Recall < thresholds.RecallMin {
		return false
	}
	if row.FPR > thresholds.FPRMax {
		return false
	}
	if row.PromptSHA256 != currentPromptSHA256 {
		return false
	}
	return true
}

// LoadResults reads the results file at path. A missing file is not an
// error — it returns a zero BenchmarkFile and existed=false so WriteResult
// can create it on the first write.
//
// A file whose schema isn't 1 is refused rather than silently accepted: a
// future schema bump read back by this (older) code would otherwise get
// merged and rewritten as schema:1, quietly downgrading it (review
// 2026-09-24, MINOR).
func LoadResults(path string) (bf BenchmarkFile, existed bool, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return BenchmarkFile{}, false, nil
		}
		return BenchmarkFile{}, false, fmt.Errorf("eval: read results %q: %w", path, err)
	}
	if err := json.Unmarshal(data, &bf); err != nil {
		return BenchmarkFile{}, true, fmt.Errorf("eval: parse results %q: %w", path, err)
	}
	if bf.Schema != 1 {
		return BenchmarkFile{}, true, fmt.Errorf("eval: results %q: unsupported schema %d (want 1) — refusing to load and silently rewrite it", path, bf.Schema)
	}
	return bf, true, nil
}

// allFixturesErrored reports whether errs covers every review the run
// attempted (runs × nFixtures) — i.e. nothing in the row is real evidence,
// most likely an auth/quota/config failure rather than a model quality
// signal.
func allFixturesErrored(errs, runs, nFixtures int) bool {
	return runs > 0 && nFixtures > 0 && errs >= runs*nFixtures
}

// nonFiniteField returns the name of the first BenchmarkRow field that is
// NaN or ±Inf, or "" if every field is finite. encoding/json cannot marshal
// non-finite floats (json: unsupported value: +Inf) — catching it here
// turns a marshal panic-adjacent failure deep in WriteResult into a plain
// error naming the offending field (review 2026-09-24, BLOCKER follow-up:
// this is now defense-in-depth since thresholds are constants, not an
// operator-suppliable +Inf).
func nonFiniteField(row BenchmarkRow) string {
	fields := []struct {
		name string
		v    float64
	}{
		{"recall", row.Recall}, {"recall_min", row.RecallMin}, {"recall_max", row.RecallMax},
		{"precision", row.Precision},
		{"fpr", row.FPR}, {"fpr_min", row.FPRMin}, {"fpr_max", row.FPRMax},
		{"silence_violation_rate", row.SilenceViolationRate},
		{"input_tokens", row.InputTokens}, {"output_tokens", row.OutputTokens}, {"cached_input_tokens", row.CachedInputTokens},
		{"usd_per_review", row.USDPerReview},
		{"pricing.input_per_1m", row.Pricing.InputPer1M}, {"pricing.output_per_1m", row.Pricing.OutputPer1M},
	}
	for _, f := range fields {
		if math.IsNaN(f.v) || math.IsInf(f.v, 0) {
			return f.name
		}
	}
	return ""
}

// WriteResult merges one provider+model measurement into the results file
// at path (ADR-0043 §4), creating it if absent. Rows are keyed by
// (provider, model): re-measuring a pair replaces its row outright, even
// one written under an older prompt — prompt history survives only on
// whichever *other* rows still hold their own provider/model slot, not by
// stacking multiple measurements under one slot. Every row's `eligible` —
// including every pre-existing row, not only the one just written — is
// recomputed against corpus, runs, thresholds and row's own prompt hash,
// so an older row silently drops out of eligibility the moment a newer
// prompt is measured, without needing its own rewrite to notice.
//
// The write is refused if the file already holds evidence for a different
// corpus fingerprint or run count: a results file holds exactly one
// corpus's worth of evidence, and mixing two would make every row's n
// meaningless. Output is deterministic: rows sorted by (provider, model),
// fixed field order via the struct, indented JSON with a trailing newline.
func WriteResult(path string, corpus CorpusInfo, runs int, thresholds Thresholds, row BenchmarkRow) error {
	if field := nonFiniteField(row); field != "" {
		return fmt.Errorf("eval: results %q: row %s/%s has a non-finite %s — refusing to write (JSON cannot encode NaN/Inf)", path, row.Provider, row.Model, field)
	}

	bf, existed, err := LoadResults(path)
	if err != nil {
		return err
	}
	if existed && len(bf.Results) > 0 {
		if bf.Corpus.Fingerprint != corpus.Fingerprint {
			return fmt.Errorf("eval: results %q: corpus fingerprint mismatch (file=%s new=%s) — refusing to mix corpora in one results file", path, bf.Corpus.Fingerprint, corpus.Fingerprint)
		}
		if bf.Runs != runs {
			return fmt.Errorf("eval: results %q: runs mismatch (file=%d new=%d) — refusing to mix run counts in one results file", path, bf.Runs, runs)
		}
		// A new row that errored on every attempted review carries no real
		// evidence — almost certainly an auth/quota/config failure, not a
		// quality measurement — so it must not silently blow away a prior
		// row that did produce evidence for this same provider/model
		// (review 2026-09-24, MAJOR).
		if allFixturesErrored(row.Errors, runs, corpus.NFixtures) {
			for _, existing := range bf.Results {
				if existing.Provider == row.Provider && existing.Model == row.Model &&
					!allFixturesErrored(existing.Errors, bf.Runs, bf.Corpus.NFixtures) {
					return fmt.Errorf("eval: results %q: new row %s/%s errored on all %d reviews (runs=%d × n_fixtures=%d) — refusing to overwrite the existing row, which has real evidence; fix the run and retry", path, row.Provider, row.Model, row.Errors, runs, corpus.NFixtures)
				}
			}
		}
	}

	bf.Schema = 1
	bf.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	bf.Corpus = corpus
	bf.Runs = runs
	bf.Thresholds = thresholds

	replaced := false
	for i := range bf.Results {
		if bf.Results[i].Provider == row.Provider && bf.Results[i].Model == row.Model {
			bf.Results[i] = row
			replaced = true
			break
		}
	}
	if !replaced {
		bf.Results = append(bf.Results, row)
	}

	for i := range bf.Results {
		bf.Results[i].Eligible = computeEligible(bf.Results[i], corpus, thresholds, row.PromptSHA256)
	}

	sort.Slice(bf.Results, func(i, j int) bool {
		if bf.Results[i].Provider != bf.Results[j].Provider {
			return bf.Results[i].Provider < bf.Results[j].Provider
		}
		return bf.Results[i].Model < bf.Results[j].Model
	})

	data, err := json.MarshalIndent(bf, "", "  ")
	if err != nil {
		return fmt.Errorf("eval: marshal results: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("eval: mkdir for results %q: %w", path, err)
	}
	// Write via a temp file + rename rather than os.WriteFile directly: a
	// crash or a concurrent `make eval-live` mid-write must never leave path
	// holding a truncated/partial JSON file (review 2026-09-24, MINOR).
	// os.Rename is atomic within the same filesystem, which dir (a
	// subdirectory of path's own tree) always is.
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("eval: create temp file for results %q: %w", path, err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("eval: write temp file for results %q: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("eval: close temp file for results %q: %w", path, err)
	}
	// No os.Chmod here: os.CreateTemp already leaves tmpPath at 0600, which
	// is strictly tighter than the 0644 a plain os.WriteFile would use for a
	// non-secret file. Widening it back to 0644 before the rename only trips
	// gosec's G302 for no behavioral gain (see internal/cli/surface.go for
	// the same convention on another temp-file-then-rename writer) — the
	// committed artifact in git is tracked as 100644 regardless, and a fresh
	// clone gets the user's umask either way.
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("eval: rename temp file into results %q: %w", path, err)
	}
	return nil
}
