// SPDX-License-Identifier: GPL-3.0-or-later

package eval

// Helpers behind the live tier's cost and scope controls
// (COMMITBRIEF_EVAL_FIXTURES, COMMITBRIEF_EVAL_BUDGET_USD,
// COMMITBRIEF_EVAL_OUT resolution, provider env resolution). They live in an
// untagged _test.go file — not live_test.go, which is behind the `live`
// build tag — so `go test ./...` exercises them against the mock provider
// without a single paid call. Nothing here talks to a real provider.

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

// selectFixtures narrows fixtures to the names listed in csv
// (COMMITBRIEF_EVAL_FIXTURES: comma-separated Fixture.Name values). A blank
// csv returns every fixture unchanged. The result keeps corpus order,
// whatever order the names were given in, and ignores empty entries and
// duplicates. An unknown name is an error naming the offender — a typo'd
// subset must not silently become a smaller (or empty) run.
func selectFixtures(fixtures []Fixture, csv string) ([]Fixture, error) {
	if strings.TrimSpace(csv) == "" {
		return fixtures, nil
	}
	want := map[string]bool{}
	for _, name := range strings.Split(csv, ",") {
		if name = strings.TrimSpace(name); name != "" {
			want[name] = true
		}
	}
	if len(want) == 0 {
		return nil, fmt.Errorf("COMMITBRIEF_EVAL_FIXTURES=%q: no fixture names", csv)
	}
	known := make(map[string]bool, len(fixtures))
	for _, fx := range fixtures {
		known[fx.Name] = true
	}
	var unknown []string
	for name := range want {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("COMMITBRIEF_EVAL_FIXTURES: unknown fixture(s) %s", strings.Join(unknown, ", "))
	}
	out := make([]Fixture, 0, len(want))
	for _, fx := range fixtures {
		if want[fx.Name] {
			out = append(out, fx)
		}
	}
	return out, nil
}

// checkSubsetOut refuses a results-file write for a partial corpus: a row
// measured on a fixture subset is not comparable to the full-corpus rows in
// benchmark.json and must never land there (ADR-0043 §4).
func checkSubsetOut(fixturesCSV, out string) error {
	if strings.TrimSpace(fixturesCSV) != "" && out != "" {
		return errors.New("COMMITBRIEF_EVAL_FIXTURES selects a fixture subset; refusing COMMITBRIEF_EVAL_OUT — a partial row must not be written to the results file (unset one of them)")
	}
	return nil
}

// parseBudget reads COMMITBRIEF_EVAL_BUDGET_USD. Blank means no cap
// (ok=false). Anything that is not a finite, positive number is an error,
// like evalRuns: a typo'd cap must not quietly turn into "uncapped".
func parseBudget(v string) (usd float64, ok bool, err error) {
	if strings.TrimSpace(v) == "" {
		return 0, false, nil
	}
	f, perr := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if perr != nil || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return 0, false, fmt.Errorf("COMMITBRIEF_EVAL_BUDGET_USD=%q: want a positive number", v)
	}
	return f, true, nil
}

// checkBudgetPricing refuses a budget cap that cannot be enforced: a model
// missing from the provider's price table reports provider.Pricing{}, which
// would price every fixture at $0 and let the run sail past any cap. A
// no-cap run needs no price and passes.
func checkBudgetPricing(capped bool, pricing provider.Pricing, providerName, model string) error {
	if capped && pricing.InputPer1M == 0 && pricing.OutputPer1M == 0 {
		return fmt.Errorf("COMMITBRIEF_EVAL_BUDGET_USD is set but %s/%s has no price entry; the cap cannot be enforced (unset the cap or use a priced model)", providerName, model)
	}
	return nil
}

// BudgetExceededError is what budgetTracker.observe returns once the running
// estimate passes the cap. It carries the amounts so the test can log what
// was actually spent.
type BudgetExceededError struct {
	CapUSD   float64
	SpentUSD float64
}

func (e *BudgetExceededError) Error() string {
	return fmt.Sprintf("eval: budget cap exceeded: spent ~$%.4f of $%.4f", e.SpentUSD, e.CapUSD)
}

// budgetTracker accumulates the estimated spend over an entire invocation
// (all k runs). The estimate prices each fixture's reported usage with
// uncachedInputOutputCost — the very function BuildRow's usd_per_review
// uses — so the cap and the written row can never disagree on the price
// list. Usage of failed attempts that RunCorpus retried is not reported by
// the provider layer, so the estimate can undercount a run that hit
// transient errors.
type budgetTracker struct {
	pricing  provider.Pricing
	capUSD   float64 // <= 0 means no cap
	spentUSD float64
}

// observe is an AfterFixtureFunc: it adds the fixture's cost and errors once
// the total is above the cap. The comparison is strict (>): spending exactly
// the cap is allowed.
func (b *budgetTracker) observe(s FixtureScore) error {
	b.spentUSD += uncachedInputOutputCost(b.pricing, s.Usage)
	if b.capUSD > 0 && b.spentUSD > b.capUSD {
		return &BudgetExceededError{CapUSD: b.capUSD, SpentUSD: b.spentUSD}
	}
	return nil
}

// runTotals is the end-of-run summary the live test logs (Faz 03 reads it).
type runTotals struct {
	USD         float64 // estimated cost, uncached list rate
	Reviews     int     // fixture reviews across all runs
	Errors      int     // fixtures that errored on every attempt
	ParseErrors int     // subset of Errors: unparseable model output
}

// summarizeRuns totals cost and error counts across scorecards.
func summarizeRuns(scs []Scorecard, pricing provider.Pricing) runTotals {
	var t runTotals
	for _, sc := range scs {
		t.USD += uncachedInputOutputCost(pricing, sc.TotalUsage())
		t.Reviews += len(sc.Fixtures)
		for _, f := range sc.Fixtures {
			if !f.Errored {
				continue
			}
			t.Errors++
			// Same signature isRetriable keys on; reviewFindings wraps parse
			// failures as "parse findings".
			if strings.Contains(strings.ToLower(f.ErrorMsg), "parse findings") {
				t.ParseErrors++
			}
		}
	}
	return t
}

// resolveOutPath makes a relative COMMITBRIEF_EVAL_OUT mean "relative to the
// module root". `go test` runs with the package directory as cwd, so a path
// like internal/eval/results/benchmark.json used to land under
// internal/eval/internal/eval/... An absolute path is returned as is.
func resolveOutPath(out string) (string, error) {
	if out == "" || filepath.IsAbs(out) {
		return out, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve COMMITBRIEF_EVAL_OUT: getwd: %w", err)
	}
	return resolveOutPathFrom(out, wd)
}

// resolveOutPathFrom is resolveOutPath with the starting directory explicit:
// it walks up from startDir to the nearest go.mod and joins out onto it.
func resolveOutPathFrom(out, startDir string) (string, error) {
	if out == "" || filepath.IsAbs(out) {
		return out, nil
	}
	dir := startDir
	for {
		if st, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && !st.IsDir() {
			return filepath.Join(dir, out), nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("resolve COMMITBRIEF_EVAL_OUT=%q: no go.mod found above %s; pass an absolute path", out, startDir)
		}
		dir = parent
	}
}

// liveProviderSettings turns a loaded config (nil when the file is absent)
// into the provider name and its config for the live harness. Precedence,
// highest first: COMMITBRIEF_EVAL_* > the standard env (GEMINI_API_KEY,
// COMMITBRIEF_PROVIDER, COMMITBRIEF_MODEL, … via config.ApplyEnv) > the
// config file. An empty name means no provider is configured. It never
// logs or returns the key anywhere but in the config value.
func liveProviderSettings(cfg *config.Config) (string, config.ProviderConfig) {
	if cfg == nil {
		cfg = &config.Config{}
	}
	config.ApplyEnv(cfg)

	name := os.Getenv("COMMITBRIEF_EVAL_PROVIDER")
	if name == "" {
		name = cfg.Provider
	}
	if name == "" {
		return "", config.ProviderConfig{}
	}

	pc := cfg.Providers[name]
	if key := os.Getenv("COMMITBRIEF_EVAL_API_KEY"); key != "" {
		pc.APIKey = key
	}
	if model := os.Getenv("COMMITBRIEF_EVAL_MODEL"); model != "" {
		pc.Model = model
	}
	return name, pc
}
