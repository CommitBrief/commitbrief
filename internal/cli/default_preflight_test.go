// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/anthropic"
	"github.com/CommitBrief/commitbrief/internal/provider/gemini"
	"github.com/CommitBrief/commitbrief/internal/provider/openai"
)

// typicalDiffInputTokens is the "typical diff" of the default-model rule:
// the p75 commit of the maintainer's CLI repo as the preflight estimates
// it (fixed prompt plus line-numbered diff, chars/4), rounded up. The
// output side is not a constant here — it goes through the same
// estimateOutputTokens heuristic the real preflight uses.
const typicalDiffInputTokens = 8500

// TestPreflightDefaultModelTypicalDiffUnderThreshold guards the first-run
// promise: a fresh config reviewing a typical staged diff with each
// API provider's default model must stay under the default cost
// threshold, so handleCostPreflight neither prompts nor aborts. Prices
// are read from the provider's own Pricing table, never hard-coded, so
// a catalog price change that breaks the promise fails here.
func TestPreflightDefaultModelTypicalDiffUnderThreshold(t *testing.T) {
	cases := []struct {
		name         string
		defaultModel string
		newProvider  func(config.ProviderConfig) (provider.Provider, error)
	}{
		{anthropic.Name, anthropic.DefaultModel, anthropic.New},
		{openai.Name, openai.DefaultModel, openai.New},
		{gemini.Name, gemini.DefaultModel, gemini.New},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetGlobalFlags(t)

			cfg := config.Default()
			cfg.Provider = tc.name
			pc, ok := cfg.Providers[tc.name]
			if !ok {
				t.Fatalf("config.Default() has no %q provider block", tc.name)
			}
			// config.Default() and the provider's DefaultModel must agree,
			// otherwise a fresh config reviews with a model this test
			// never priced.
			if pc.Model != tc.defaultModel {
				t.Fatalf("config.Default() model for %s = %q, provider DefaultModel = %q", tc.name, pc.Model, tc.defaultModel)
			}

			// A fresh config carries the model explicitly; construct the
			// provider without it so DefaultModel() falls through to the
			// package constant the wizard and docs describe.
			prov, err := tc.newProvider(config.ProviderConfig{APIKey: "test-key"})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			model := prov.DefaultModel()
			if model != tc.defaultModel {
				t.Fatalf("DefaultModel() = %q, want %q", model, tc.defaultModel)
			}

			pricing := resolvePricing(cfg, prov, model)
			if pricing.InputPer1M <= 0 || pricing.OutputPer1M <= 0 {
				t.Fatalf("default model %s has no list price (%+v); the cost check would pass vacuously", model, pricing)
			}

			usage := provider.Usage{
				InputTokens:  typicalDiffInputTokens,
				OutputTokens: estimateOutputTokens(typicalDiffInputTokens),
			}
			estCost := pricing.Cost(usage)
			threshold := cfg.Cost.WarnThresholdUSD
			if threshold <= 0 {
				t.Fatalf("default cost.warn_threshold_usd = %v, want > 0", threshold)
			}
			if estCost > threshold {
				t.Fatalf("%s default %s: typical diff costs $%.4f, over the $%.2f default threshold", tc.name, model, estCost, threshold)
			}
			t.Logf("%s default %s: typical diff ≈ $%.4f (threshold $%.2f)", tc.name, model, estCost, threshold)

			cmd, errBuf := stubCmd(t)
			app := &appContext{Config: cfg, Catalog: stubApp(t, threshold).Catalog}
			if abort := handleCostPreflight(cmd, app, estCost, emptyStdin()); abort {
				t.Fatalf("handleCostPreflight aborted for the default model")
			}
			if got := errBuf.String(); got != "" {
				t.Fatalf("handleCostPreflight should be silent for the default model; got stderr:\n%s", got)
			}
		})
	}
}
