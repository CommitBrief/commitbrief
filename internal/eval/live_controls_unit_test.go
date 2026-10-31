// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

func names(fxs []Fixture) []string {
	out := make([]string, len(fxs))
	for i, f := range fxs {
		out[i] = f.Name
	}
	return out
}

func TestSelectFixtures(t *testing.T) {
	all := []Fixture{{Name: "a"}, {Name: "b"}, {Name: "c"}}

	t.Run("blank keeps all", func(t *testing.T) {
		for _, csv := range []string{"", "  "} {
			got, err := selectFixtures(all, csv)
			if err != nil || len(got) != 3 {
				t.Fatalf("csv=%q: got %v, err %v; want all 3", csv, names(got), err)
			}
		}
	})
	t.Run("subset keeps corpus order, trims, dedups, skips empties", func(t *testing.T) {
		got, err := selectFixtures(all, " c, a ,,c")
		if err != nil {
			t.Fatal(err)
		}
		if g := strings.Join(names(got), ","); g != "a,c" {
			t.Fatalf("got %s, want a,c", g)
		}
	})
	t.Run("unknown name is an error naming it", func(t *testing.T) {
		_, err := selectFixtures(all, "a,nope,zzz")
		if err == nil || !strings.Contains(err.Error(), "nope, zzz") {
			t.Fatalf("err = %v, want unknown fixture(s) nope, zzz", err)
		}
	})
	t.Run("only separators is an error", func(t *testing.T) {
		if _, err := selectFixtures(all, ", ,"); err == nil {
			t.Fatal("want error for a list with no names")
		}
	})
}

func TestCheckSubsetOut(t *testing.T) {
	if err := checkSubsetOut("a,b", "/x/benchmark.json"); err == nil {
		t.Error("subset + OUT must be refused")
	}
	if err := checkSubsetOut("a,b", ""); err != nil {
		t.Errorf("subset without OUT: %v", err)
	}
	if err := checkSubsetOut("", "/x/benchmark.json"); err != nil {
		t.Errorf("OUT without subset: %v", err)
	}
}

func TestParseBudget(t *testing.T) {
	if _, ok, err := parseBudget(""); ok || err != nil {
		t.Errorf("blank: ok=%v err=%v, want uncapped", ok, err)
	}
	if v, ok, err := parseBudget("3"); !ok || err != nil || v != 3 {
		t.Errorf("3: %v %v %v", v, ok, err)
	}
	if v, ok, err := parseBudget(" 0.25 "); !ok || err != nil || v != 0.25 {
		t.Errorf("0.25: %v %v %v", v, ok, err)
	}
	for _, bad := range []string{"abc", "0", "-1", "NaN", "Inf", "$3"} {
		if _, _, err := parseBudget(bad); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

// capPricing makes the arithmetic obvious: $1 per 1M input, $2 per 1M output.
var capPricing = provider.Pricing{InputPer1M: 1, OutputPer1M: 2}

func TestBudgetTracker(t *testing.T) {
	// 1M in + 1M out = $3 per fixture.
	step := FixtureScore{Usage: provider.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}}

	b := &budgetTracker{pricing: capPricing, capUSD: 6}
	for i := 0; i < 2; i++ {
		if err := b.observe(step); err != nil {
			t.Fatalf("observe #%d: %v (spending exactly the cap is allowed)", i+1, err)
		}
	}
	err := b.observe(step)
	var be *BudgetExceededError
	if !errors.As(err, &be) {
		t.Fatalf("third observe: %v, want BudgetExceededError", err)
	}
	if math.Abs(be.SpentUSD-9) > 1e-9 || be.CapUSD != 6 {
		t.Errorf("spent=%v cap=%v, want 9 / 6", be.SpentUSD, be.CapUSD)
	}

	// Cached tokens must not discount the estimate (same rule as BuildRow).
	u := provider.Usage{InputTokens: 1_000_000, CachedInputTokens: 1_000_000}
	un := &budgetTracker{pricing: provider.Pricing{InputPer1M: 1, CachedInputPer1M: 0.1}}
	_ = un.observe(FixtureScore{Usage: u})
	if math.Abs(un.spentUSD-1) > 1e-9 {
		t.Errorf("spent = %v, want 1 (uncached list rate)", un.spentUSD)
	}
	if err := un.observe(step); err != nil {
		t.Errorf("no cap set: %v", err)
	}
}

// usageMock reports fixed usage so the run's cost is predictable.
func usageMock(in, out int) *mock.Provider {
	m := mock.New()
	m.ResponseContent = `{"findings":[]}`
	m.InputTokens = in
	m.OutputTokens = out
	return m
}

func TestRunCorpusNWithBudgetStopsRun(t *testing.T) {
	m := usageMock(1_000_000, 0)
	fxs := []Fixture{{Name: "f1"}, {Name: "f2"}, {Name: "f3"}}
	b := &budgetTracker{pricing: capPricing, capUSD: 1.5} // $1 per fixture → stops on the 2nd

	scs, err := RunCorpusNWith(context.Background(), m, "", fxs, 3, b.observe)
	var be *BudgetExceededError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want BudgetExceededError through the wrapper", err)
	}
	if len(scs) != 1 || len(scs[0].Fixtures) != 2 {
		t.Fatalf("scorecards = %d (first has %d fixtures), want the one partial run with 2 fixtures", len(scs), func() int {
			if len(scs) == 0 {
				return 0
			}
			return len(scs[0].Fixtures)
		}())
	}
	if got := summarizeRuns(scs, capPricing).USD; math.Abs(got-2) > 1e-9 {
		t.Errorf("summarized USD of the partial run = %v, want 2", got)
	}
	if math.Abs(b.spentUSD-2) > 1e-9 {
		t.Errorf("spent = %v, want 2 (stopped right after the 2nd fixture)", b.spentUSD)
	}
}

func TestRunCorpusWithNilHookMatchesRunCorpus(t *testing.T) {
	fxs := []Fixture{{Name: "f1"}, {Name: "f2"}}
	a, err := RunCorpus(context.Background(), usageMock(10, 10), "", fxs)
	if err != nil {
		t.Fatal(err)
	}
	b, err := RunCorpusWith(context.Background(), usageMock(10, 10), "", fxs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Fixtures) != 2 {
		t.Fatalf("fixtures = %d, want 2", len(a.Fixtures))
	}
	if !reflect.DeepEqual(a, b) {
		t.Errorf("RunCorpusWith(nil) = %+v, want identical to RunCorpus = %+v", b, a)
	}
}

func TestCheckBudgetPricing(t *testing.T) {
	if err := checkBudgetPricing(true, provider.Pricing{}, "gemini", "gemini-x"); err == nil ||
		!strings.Contains(err.Error(), "gemini/gemini-x") {
		t.Errorf("capped + zero price: err = %v, want a refusal naming provider/model", err)
	}
	if err := checkBudgetPricing(true, provider.Pricing{OutputPer1M: 2}, "p", "m"); err != nil {
		t.Errorf("capped + priced: %v", err)
	}
	if err := checkBudgetPricing(false, provider.Pricing{}, "p", "m"); err != nil {
		t.Errorf("no cap needs no price: %v", err)
	}
}

func TestSummarizeRuns(t *testing.T) {
	sc := Scorecard{Fixtures: []FixtureScore{
		{Usage: provider.Usage{InputTokens: 1_000_000}},
		{Errored: true, ErrorMsg: "eval: fixture \"x\": parse findings: bad json", Usage: provider.Usage{OutputTokens: 1_000_000}},
		{Errored: true, ErrorMsg: "boom"},
	}}
	got := summarizeRuns([]Scorecard{sc, sc}, capPricing)
	if got.Reviews != 6 || got.Errors != 4 || got.ParseErrors != 2 {
		t.Errorf("totals = %+v, want reviews=6 errors=4 parse=2", got)
	}
	if math.Abs(got.USD-6) > 1e-9 { // 2 × ($1 in + $2 out)
		t.Errorf("USD = %v, want 6", got.USD)
	}
}

func TestResolveOutPath(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(root, "internal", "eval")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := resolveOutPathFrom("internal/eval/results/benchmark.json", deep)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(root, "internal/eval/results/benchmark.json"); got != want {
		t.Errorf("relative: got %s, want %s", got, want)
	}

	abs := filepath.Join(t.TempDir(), "o.json")
	if got, _ := resolveOutPathFrom(abs, deep); got != abs {
		t.Errorf("absolute changed: %s", got)
	}
	if got, _ := resolveOutPathFrom("", deep); got != "" {
		t.Errorf("empty changed: %q", got)
	}

	// No go.mod above: refuse rather than guess. The temp dir is outside any
	// module on a normal machine; skip if the host has one anywhere above it.
	orphan := t.TempDir()
	for d := orphan; ; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
			t.Skip("go.mod somewhere above the temp dir")
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	if _, err := resolveOutPathFrom("a.json", orphan); err == nil {
		t.Error("want error when no go.mod is found")
	}

	// The real one: from this package dir, a relative path lands at the
	// module root, not under internal/eval.
	fromHere, err := resolveOutPath("internal/eval/results/benchmark.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fromHere, filepath.Join("internal", "eval", "internal")) {
		t.Errorf("resolved under the package dir: %s", fromHere)
	}
}

func clearLiveEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"COMMITBRIEF_EVAL_PROVIDER", "COMMITBRIEF_EVAL_API_KEY", "COMMITBRIEF_EVAL_MODEL",
		"COMMITBRIEF_PROVIDER", "COMMITBRIEF_MODEL",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY",
		"MISTRAL_API_KEY", "COHERE_API_KEY", "OLLAMA_HOST",
	} {
		t.Setenv(k, "")
	}
}

func TestLiveProviderSettings(t *testing.T) {
	cfgWith := func() *config.Config {
		return &config.Config{
			Provider: "gemini",
			Providers: map[string]config.ProviderConfig{
				"gemini": {APIKey: "file-key", Model: "file-model"},
			},
		}
	}

	t.Run("nil config and nothing set", func(t *testing.T) {
		clearLiveEnv(t)
		if name, _ := liveProviderSettings(nil); name != "" {
			t.Errorf("name = %q, want none", name)
		}
	})
	t.Run("nil config, standard env is enough", func(t *testing.T) {
		clearLiveEnv(t)
		t.Setenv("COMMITBRIEF_EVAL_PROVIDER", "gemini")
		t.Setenv("GEMINI_API_KEY", "env-key")
		name, pc := liveProviderSettings(nil)
		if name != "gemini" || pc.APIKey != "env-key" {
			t.Errorf("got %q / key set=%v, want gemini / env-key", name, pc.APIKey == "env-key")
		}
	})
	t.Run("config file only", func(t *testing.T) {
		clearLiveEnv(t)
		name, pc := liveProviderSettings(cfgWith())
		if name != "gemini" || pc.APIKey != "file-key" || pc.Model != "file-model" {
			t.Errorf("got %q %+v", name, pc.Model)
		}
	})
	t.Run("standard env beats config file", func(t *testing.T) {
		clearLiveEnv(t)
		t.Setenv("GEMINI_API_KEY", "env-key")
		_, pc := liveProviderSettings(cfgWith())
		if pc.APIKey != "env-key" {
			t.Error("GEMINI_API_KEY did not override the file key")
		}
		if pc.Model != "file-model" {
			t.Errorf("model = %q, want it kept from the file", pc.Model)
		}
	})
	t.Run("EVAL env beats standard env", func(t *testing.T) {
		clearLiveEnv(t)
		t.Setenv("GEMINI_API_KEY", "env-key")
		t.Setenv("COMMITBRIEF_EVAL_API_KEY", "eval-key")
		t.Setenv("COMMITBRIEF_MODEL", "std-model")
		t.Setenv("COMMITBRIEF_EVAL_MODEL", "eval-model")
		_, pc := liveProviderSettings(cfgWith())
		if pc.APIKey != "eval-key" || pc.Model != "eval-model" {
			t.Errorf("key=eval:%v model=%q, want eval-key / eval-model", pc.APIKey == "eval-key", pc.Model)
		}
	})
	t.Run("standard COMMITBRIEF_PROVIDER picks the default", func(t *testing.T) {
		clearLiveEnv(t)
		t.Setenv("COMMITBRIEF_PROVIDER", "openai")
		t.Setenv("OPENAI_API_KEY", "o-key")
		name, pc := liveProviderSettings(cfgWith())
		if name != "openai" || pc.APIKey != "o-key" {
			t.Errorf("got %q", name)
		}
	})
	t.Run("EVAL_PROVIDER beats COMMITBRIEF_PROVIDER", func(t *testing.T) {
		clearLiveEnv(t)
		t.Setenv("COMMITBRIEF_PROVIDER", "openai")
		t.Setenv("COMMITBRIEF_EVAL_PROVIDER", "gemini")
		if name, _ := liveProviderSettings(cfgWith()); name != "gemini" {
			t.Errorf("name = %q, want gemini", name)
		}
	})
}
