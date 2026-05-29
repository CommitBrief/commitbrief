// SPDX-License-Identifier: GPL-3.0-or-later

//go:build live

package eval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
// real provider and prints the quality scorecard. It is non-deterministic
// and gated behind the `live` build tag, so it never runs in CI and is
// never a gate. It is the source of the README quality numbers.
//
// Provider resolution (first match wins):
//  1. COMMITBRIEF_EVAL_PROVIDER (+ COMMITBRIEF_EVAL_API_KEY,
//     COMMITBRIEF_EVAL_MODEL) — explicit override.
//  2. The default provider in the user's ~/.commitbrief/config.yml — so
//     `make eval-live` works against the configured provider with no env
//     vars and the API key never passes through the shell.
//
// If neither yields a provider with an API key, the test skips.
func TestEvalLive(t *testing.T) {
	p, model := resolveLiveProvider(t)

	fixtures, err := LoadCorpus(corpusDir())
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}

	sc, err := RunCorpus(context.Background(), p, model, fixtures)
	if err != nil {
		t.Fatalf("RunCorpus: %v", err)
	}

	logScorecard(t, "FULL", sc, true)
	// Report the dev and held-out slices separately so overfitting is
	// visible at a glance (ADR-0018 §Goodhart): a prompt change that lifts
	// DEV recall but not HELD-OUT recall has overfit the corpus.
	logScorecard(t, "DEV (tunable)", sc.Dev(), false)
	logScorecard(t, "HELD-OUT (generalization)", sc.HeldOut(), false)
}

// logScorecard prints a scorecard's per-fixture lines, totals, and (when
// withCategories) the per-category recall breakdown.
func logScorecard(t *testing.T, label string, sc Scorecard, withCategories bool) {
	t.Helper()
	t.Logf("── %s — provider=%s model=%s (%d fixtures) ──", label, sc.Provider, sc.Model, len(sc.Fixtures))
	for _, f := range sc.Fixtures {
		t.Logf("   %-26s TP=%d FN=%d FP=%d  P=%.2f R=%.2f FPR=%.2f",
			f.Fixture, f.TruePositives, f.FalseNegatives, f.FalsePositives,
			f.Precision(), f.Recall(), f.FalsePositiveRate())
	}
	t.Logf("   TOTAL  precision=%.2f recall=%.2f false-positive-rate=%.2f",
		sc.Precision(), sc.Recall(), sc.FalsePositiveRate())
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
		findings, ferr := reviewFindings(context.Background(), p, fx, model)
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
