// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"fmt"
	"time"

	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// buildRequest assembles the review request for a fixture using the
// embedded default rules and the English locale — the same prompt the CLI
// sends on a default-config run, so the eval measures the shipped path
// rather than a bespoke prompt.
func buildRequest(fx Fixture, model string) provider.Request {
	p := prompt.Build(rules.Default(), lang.CoerceCLIFlag("en"), fx.Diff)
	return provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         "en",
	}
}

// reviewFindings runs one fixture through a provider and returns the parsed
// findings. An empty model uses the provider's default model. Shared by
// RunFixture (scoring) and the live diagnostic dump.
func reviewFindings(ctx context.Context, p provider.Provider, fx Fixture, model string) ([]render.Finding, error) {
	if model == "" {
		model = p.DefaultModel()
	}
	resp, err := p.Review(ctx, buildRequest(fx, model))
	if err != nil {
		return nil, fmt.Errorf("eval: fixture %q: review: %w", fx.Name, err)
	}
	findings, err := render.ParseFindings(resp.Content)
	if err != nil {
		return nil, fmt.Errorf("eval: fixture %q: parse findings: %w", fx.Name, err)
	}
	return findings, nil
}

// RunFixture runs one fixture through a provider and scores the result.
// An empty model uses the provider's default model.
func RunFixture(ctx context.Context, p provider.Provider, fx Fixture, model string) (FixtureScore, error) {
	findings, err := reviewFindings(ctx, p, fx, model)
	if err != nil {
		return FixtureScore{}, err
	}
	return Score(findings, fx), nil
}

// corpusAttempts is how many times RunCorpus tries each fixture before
// giving up. Live providers occasionally return a transient 503 ("high
// demand") that would otherwise abort a whole 23-fixture run; a couple of
// retries with backoff rides over the spike. The deterministic mock tier
// calls RunFixture directly and never hits this path.
const corpusAttempts = 3

// RunCorpus runs every fixture through the provider and returns a
// Scorecard. Each fixture is retried up to corpusAttempts times with
// linear backoff; if it still fails the error aborts the run.
func RunCorpus(ctx context.Context, p provider.Provider, model string, fixtures []Fixture) (Scorecard, error) {
	sc := Scorecard{Provider: p.Name(), Model: model}
	if model == "" {
		sc.Model = p.DefaultModel()
	}
	for _, fx := range fixtures {
		var (
			s   FixtureScore
			err error
		)
		for attempt := 1; attempt <= corpusAttempts; attempt++ {
			s, err = RunFixture(ctx, p, fx, model)
			if err == nil {
				break
			}
			if attempt < corpusAttempts {
				select {
				case <-ctx.Done():
					return Scorecard{}, ctx.Err()
				case <-time.After(time.Duration(attempt) * 2 * time.Second):
				}
			}
		}
		if err != nil {
			return Scorecard{}, err
		}
		sc.Fixtures = append(sc.Fixtures, s)
	}
	return sc, nil
}
