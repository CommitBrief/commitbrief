// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"fmt"
	"strings"
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
	p := prompt.Build(rules.Default(), lang.English(), fx.Diff)
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

// isRetriable reports whether a fixture error is a transient provider
// condition worth retrying. Deterministic failures (unparseable response)
// and anything without a recognized transient signature fail fast — a
// retry would just re-spend on the live tier. Best-effort string match: the
// provider packages wrap heterogeneous SDK errors, so there's no single
// error type to switch on.
func isRetriable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "parse findings") {
		return false // a re-run yields the same unparseable output
	}
	// Word-shaped transient signals are safe as plain substrings.
	for _, sig := range []string{
		"unavailable", "overloaded", "timeout", "deadline",
		"temporarily", "rate limit", "try again",
	} {
		if strings.Contains(msg, sig) {
			return true
		}
	}
	// HTTP status codes must match on a digit boundary, or a token-count /
	// byte-size / duration string ("requested 130500 tokens", "1500ms")
	// would be mistaken for a 500/503 and retried as a billable call — the
	// exact waste this function exists to prevent.
	for _, code := range []string{"429", "500", "502", "503"} {
		if hasStatusToken(msg, code) {
			return true
		}
	}
	return false
}

// hasStatusToken reports whether code appears in msg bounded by non-digits
// (or string edges) on both sides, so "503" matches "error 503," but not
// "130503".
func hasStatusToken(msg, code string) bool {
	for from := 0; ; {
		i := strings.Index(msg[from:], code)
		if i < 0 {
			return false
		}
		i += from
		beforeOK := i == 0 || !isASCIIDigit(msg[i-1])
		end := i + len(code)
		afterOK := end >= len(msg) || !isASCIIDigit(msg[end])
		if beforeOK && afterOK {
			return true
		}
		from = i + 1
	}
}

func isASCIIDigit(b byte) bool { return b >= '0' && b <= '9' }

// RunCorpus runs every fixture through the provider and returns a
// Scorecard. A fixture hitting a transient provider error (isRetriable) is
// retried up to corpusAttempts times with linear backoff; a non-transient
// failure aborts the run after the first attempt.
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
			// Only retry transient provider hiccups (503/429/timeout). A hard
			// failure — auth error, or a deterministically unparseable
			// response — won't fix itself, and each live-tier retry is a real
			// billable call, so fail fast instead of burning corpusAttempts.
			if attempt == corpusAttempts || !isRetriable(err) {
				break
			}
			select {
			case <-ctx.Done():
				return Scorecard{}, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		if err != nil {
			return Scorecard{}, err
		}
		sc.Fixtures = append(sc.Fixtures, s)
	}
	return sc, nil
}
