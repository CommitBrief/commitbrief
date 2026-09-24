// SPDX-License-Identifier: GPL-3.0-or-later

//go:build live

package eval

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"

	// Blank-import the API providers so the registry knows them under the
	// live build. CLI-backed providers emit plain text (no findings JSON)
	// and so cannot be scored by this harness; API providers only.
	_ "github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	_ "github.com/CommitBrief/commitbrief/internal/provider/cohere"
	_ "github.com/CommitBrief/commitbrief/internal/provider/deepseek"
	_ "github.com/CommitBrief/commitbrief/internal/provider/gemini"
	_ "github.com/CommitBrief/commitbrief/internal/provider/mistral"
	_ "github.com/CommitBrief/commitbrief/internal/provider/ollama"
	_ "github.com/CommitBrief/commitbrief/internal/provider/openai"
)

// TestEvalLive is the live tier (ADR-0018 §3): it runs the corpus through a
// real provider k times and prints the quality scorecard. It is
// non-deterministic and gated behind the `live` build tag, so it never
// runs in CI and is never a gate. It is the source of the README quality
// numbers and, when COMMITBRIEF_EVAL_OUT is set, of the ADR-0043 §4
// machine-readable results file.
//
// Provider resolution (first match wins):
//  1. COMMITBRIEF_EVAL_PROVIDER (+ COMMITBRIEF_EVAL_API_KEY,
//     COMMITBRIEF_EVAL_MODEL) — explicit override.
//  2. The default provider in the user's ~/.commitbrief/config.yml — so
//     `make eval-live` works against the configured provider with no env
//     vars and the API key never passes through the shell.
//
// If neither yields a provider with an API key, the test skips.
//
// COMMITBRIEF_EVAL_RUNS repeats the whole corpus k times (default 1) so a
// flaky provider's spread is visible instead of hiding behind one lucky —
// or unlucky — pass; COMMITBRIEF_EVAL_OUT, if set, merges the pooled
// result into that path as an ADR-0043 §4 results row (created if
// missing). Both are read here, not baked into the harness, so k and the
// output path are the caller's call on every invocation.
func TestEvalLive(t *testing.T) {
	p, model := resolveLiveProvider(t)

	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	runs := evalRuns(t)
	scs, err := RunCorpusN(context.Background(), p, model, fixtures, runs)
	if err != nil {
		t.Fatalf("RunCorpusN: %v", err)
	}

	for i, sc := range scs {
		label := fmt.Sprintf("RUN %d/%d", i+1, runs)
		logScorecard(t, label, sc, i == 0)
	}
	// Report the dev and held-out slices of the last run separately so
	// overfitting is visible at a glance (ADR-0018 §Goodhart): a prompt
	// change that lifts DEV recall but not HELD-OUT recall has overfit the
	// corpus. The split is a tuning-safety diagnostic (ADR-0018), distinct
	// from the pooled whole-corpus row below, which is what ADR-0043 §3
	// model-selection metrics are scored against.
	last := scs[len(scs)-1]
	logScorecard(t, "DEV (tunable, last run)", last.Dev(), false)
	logScorecard(t, "HELD-OUT (generalization, last run)", last.HeldOut(), false)

	pricing := p.Pricing(model)
	promptSHA := PromptSHA256()
	commit := commitbriefCommit(t)
	measuredAt := time.Now()

	row, err := BuildRow(scs, pricing, promptSHA, commit, measuredAt)
	if err != nil {
		t.Fatalf("BuildRow: %v", err)
	}
	t.Logf("── POOLED (%d runs) — recall=%.4f (n=%d) precision=%.4f fpr=%.4f (n=%d) silence_violation_rate=%.4f errors=%d usd/review=%.6f ──",
		row.Runs, row.Recall, row.NBuggy, row.Precision, row.FPR, row.NClean, row.SilenceViolationRate, row.Errors, row.USDPerReview)

	outPath := os.Getenv("COMMITBRIEF_EVAL_OUT")
	if outPath == "" {
		return
	}
	fingerprint, err := CorpusFingerprint(corpusDir())
	if err != nil {
		t.Fatalf("CorpusFingerprint: %v", err)
	}
	corpus := NewCorpusInfo(fixtures, fingerprint)
	thresholds := DefaultThresholds()
	if err := WriteResult(outPath, corpus, runs, thresholds, row); err != nil {
		t.Fatalf("WriteResult(%q): %v", outPath, err)
	}
	t.Logf("── wrote results row provider=%s model=%s to %s ──", row.Provider, row.Model, outPath)
}

// evalRuns reads COMMITBRIEF_EVAL_RUNS (ADR-0043 §3's k). Unset or blank
// defaults to 1 — a single pass, same behavior as before k-repetition
// existed. A value that doesn't parse as a positive integer fails the test
// rather than silently falling back, since a typo'd env var should not
// quietly turn into "ran once" when the caller asked for more.
func evalRuns(t *testing.T) int {
	t.Helper()
	v := os.Getenv("COMMITBRIEF_EVAL_RUNS")
	if v == "" {
		return 1
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		t.Fatalf("COMMITBRIEF_EVAL_RUNS=%q: want a positive integer", v)
	}
	return n
}

// commitbriefCommit resolves the commitbrief_commit a results row is
// stamped with (ADR-0043 §4) via `git describe --always --dirty` against
// this checkout. Falls back to "unknown" rather than failing the test —
// the live tier's job is the measurement, and a details field that only
// helps trace provenance shouldn't block it.
func commitbriefCommit(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "describe", "--always", "--dirty").Output()
	if err != nil {
		t.Logf("git describe: %v (using \"unknown\")", err)
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// logScorecard prints a scorecard's per-fixture lines, totals, and (when
// withCategories) the per-category recall breakdown.
//
// The per-fixture/TOTAL rate below is FixtureScore/Scorecard's silence
// violations ÷ silence anchors — deliberately labeled
// SilenceViolationRate, not FPR, so it can't be confused with the pooled
// row.FPR logged in TestEvalLive (clean-fixture alarm rate, ADR-0043 §2's
// FPR_MAX=0.10 gate). The two are different metrics over different
// denominators (review 2026-09-24, MINOR).
func logScorecard(t *testing.T, label string, sc Scorecard, withCategories bool) {
	t.Helper()
	t.Logf("── %s — provider=%s model=%s (%d fixtures) ──", label, sc.Provider, sc.Model, len(sc.Fixtures))
	for _, f := range sc.Fixtures {
		t.Logf("   %-26s TP=%d FN=%d FP=%d  P=%.2f R=%.2f SilenceViolationRate=%.2f",
			f.Fixture, f.TruePositives, f.FalseNegatives, f.FalsePositives,
			f.Precision(), f.Recall(), f.SilenceViolationRate())
		if f.Errored {
			// A run that errored on every retry looks, from TP/FN/FP alone,
			// identical to a model that genuinely missed everything — log
			// the actual failure so that distinction isn't lost (review
			// 2026-09-24, MAJOR).
			t.Logf("      ⚠ errored: %s", f.ErrorMsg)
		}
	}
	t.Logf("   TOTAL  precision=%.2f recall=%.2f silence-violation-rate=%.2f",
		sc.Precision(), sc.Recall(), sc.SilenceViolationRate())
	if withCategories {
		for _, cr := range sc.CategoryRecall() {
			t.Logf("   category %-16s recall=%d/%d", cr.Category, cr.Caught, cr.Total)
		}
	}
}

// TestEvalLiveDump is a diagnostic (not a scorer): it prints every finding a
// real provider produces for each fixture, tagged `match` (pairs an expected
// finding) or `EXTRA` (no expected finding). Use it to decide whether an
// EXTRA is a legitimate secondary defect — which should be annotated into
// expected.json (and mirrored in mock_response.json) — or genuine noise to
// leave as a measured false positive. Run: make eval-dump.
func TestEvalLiveDump(t *testing.T) {
	p, model := resolveLiveProvider(t)

	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	for _, fx := range fixtures {
		findings, _, ferr := reviewFindings(context.Background(), p, fx, model)
		if ferr != nil {
			t.Errorf("fixture %q: %v", fx.Name, ferr)
			continue
		}
		t.Logf("── %s (%d expected, %d produced) ──", fx.Name, len(fx.Expected), len(findings))
		for _, f := range findings {
			tag := "EXTRA"
			for _, e := range fx.Expected {
				if matchesExpected(f, e) {
					tag = "match"
					break
				}
			}
			t.Logf("   [%-5s] %-8s %s:%d  %s", tag, f.Severity, f.File, f.Line, f.Title)
		}
	}
}

// resolveLiveProvider builds a provider for the live eval. The provider
// name comes from COMMITBRIEF_EVAL_PROVIDER, else the default provider in
// ~/.commitbrief/config.yml. The API key, model, and base URL are read
// from that provider's config entry, with COMMITBRIEF_EVAL_API_KEY and
// COMMITBRIEF_EVAL_MODEL as optional overrides. Selecting a provider via
// env therefore reuses its configured key — the key never has to appear on
// the command line, and one config can be benchmarked across providers and
// models. It never logs the API key.
func resolveLiveProvider(t *testing.T) (provider.Provider, string) {
	t.Helper()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot resolve home dir: %v", err)
	}
	path := filepath.Join(home, ".commitbrief", "config.yml")
	cfg, err := config.LoadFile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}

	name := os.Getenv("COMMITBRIEF_EVAL_PROVIDER")
	if name == "" {
		if cfg == nil || cfg.Provider == "" {
			t.Skipf("set COMMITBRIEF_EVAL_PROVIDER or configure a default provider in %s", path)
		}
		name = cfg.Provider
	}

	var pc config.ProviderConfig
	if cfg != nil {
		pc = cfg.Providers[name]
	}
	if key := os.Getenv("COMMITBRIEF_EVAL_API_KEY"); key != "" {
		pc.APIKey = key
	}
	if model := os.Getenv("COMMITBRIEF_EVAL_MODEL"); model != "" {
		pc.Model = model
	}
	if pc.APIKey == "" {
		t.Skipf("provider %q has no api_key (in %s or COMMITBRIEF_EVAL_API_KEY)", name, path)
	}

	p, err := provider.New(name, pc)
	if err != nil {
		t.Fatalf("provider.New(%q): %v", name, err)
	}
	return p, pc.Model
}
