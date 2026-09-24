// SPDX-License-Identifier: GPL-3.0-or-later

package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/CommitBrief/commitbrief/internal/diff"
	"github.com/CommitBrief/commitbrief/internal/git"
	"github.com/CommitBrief/commitbrief/internal/ignore"
	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/prompt"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// builtinIgnoreMatcher is the built-in-only layer of production's filter
// stack (review.go's buildMatcher composes this with a repo's
// .commitbriefignore and COMMITBRIEF.md semantic rules, neither of which
// exists for a fixture — there is no real repo checkout here). Applying at
// least this layer means a fixture's go.sum/package-lock.json/etc. never
// reaches the model in eval either, matching production instead of
// diverging from it (review 2026-09-24, MINOR).
var builtinIgnoreMatcher = ignore.Builtin()

// numberedFixtureDiff renders a fixture's stored diff the same way
// review.go does before it reaches the prompt: parsed, filtered through the
// built-in ignore patterns (review.go's buildMatcher + diff.Filter — see
// builtinIgnoreMatcher), then prefixed per line with `<n>| `
// (Diff.NumberedString). The eval harness previously sent the raw,
// unfiltered diff text, which is not what production actually ships to the
// model: the line-number hints were missing, and files production would
// have excluded (go.sum, lockfiles, vendor/**, …) were sent anyway, so a
// fixture's answer key drifted from what the corpus really measured.
// Malformed diff text falls back to the raw stored text unfiltered, since
// there's nothing parsed to filter or number.
func numberedFixtureDiff(fx Fixture) string {
	parsed, err := diff.Parse(git.Diff{Content: fx.Diff})
	if err != nil {
		return fx.Diff
	}
	parsed = diff.Filter(parsed, builtinIgnoreMatcher)
	return parsed.NumberedString()
}

// buildRequest assembles the review request for a fixture using the
// embedded default rules and the English locale — the same prompt the CLI
// sends on a default-config run, so the eval measures the shipped path
// rather than a bespoke prompt.
func buildRequest(fx Fixture, model string) provider.Request {
	// archContext "" — the eval harness measures the default-config prompt;
	// architecture-aware review (ADR-0030) is opt-in per-repo and not part of
	// the corpus baseline, so the eval prompt stays architecture-free.
	p := prompt.Build(rules.Default(), lang.English(), numberedFixtureDiff(fx), "")
	return provider.Request{
		Model:        model,
		SystemPrompt: p.System,
		UserPrompt:   p.User,
		Lang:         "en",
	}
}

// PromptSHA256 fingerprints the eval prompt independent of any fixture's
// diff (ADR-0043 §4): the system prompt and the user prompt *template*
// (diff not yet substituted in), the same (system, userTpl) pair
// buildRequest's prompt.Build call is built from. It changes whenever
// either text changes — including a user-template-only edit such as the
// line-number instructions — so a results row can be told apart from a
// measurement taken under an older prompt.
func PromptSHA256() string {
	system, userTpl := rules.Build(rules.Default(), lang.English(), "")
	h := sha256.New()
	h.Write([]byte(system))
	h.Write([]byte{0})
	h.Write([]byte(userTpl))
	return hex.EncodeToString(h.Sum(nil))
}

// reviewFindings runs one fixture through a provider and returns the parsed
// findings plus the provider's reported usage. An empty model uses the
// provider's default model. Shared by RunFixture (scoring) and the live
// diagnostic dump.
func reviewFindings(ctx context.Context, p provider.Provider, fx Fixture, model string) ([]render.Finding, provider.Usage, error) {
	if model == "" {
		model = p.DefaultModel()
	}
	resp, err := p.Review(ctx, buildRequest(fx, model))
	if err != nil {
		return nil, provider.Usage{}, fmt.Errorf("eval: fixture %q: review: %w", fx.Name, err)
	}
	findings, err := render.ParseFindings(resp.Content)
	if err != nil {
		return nil, resp.Usage, fmt.Errorf("eval: fixture %q: parse findings: %w", fx.Name, err)
	}
	return findings, resp.Usage, nil
}

// RunFixture runs one fixture through a provider and scores the result.
// An empty model uses the provider's default model. On error the returned
// FixtureScore is not a scored result — callers must check err first — but
// it still carries Usage when reviewFindings obtained one (a parse
// failure still means the provider billed for and returned a response),
// so a caller building an ErrorScore for this attempt doesn't have to
// throw that usage away (review 2026-09-24, MINOR).
func RunFixture(ctx context.Context, p provider.Provider, fx Fixture, model string) (FixtureScore, error) {
	findings, usage, err := reviewFindings(ctx, p, fx, model)
	if err != nil {
		return FixtureScore{Fixture: fx.Name, Usage: usage}, err
	}
	s := Score(findings, fx)
	s.Usage = usage
	return s, nil
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
// retried up to corpusAttempts times with linear backoff. A fixture that
// still fails once retries are exhausted — or a non-transient failure on
// the first attempt — does not abort the run (ADR-0043 §3): it is recorded
// via ErrorScore (a miss for every expected finding, an alarm if the
// fixture is clean) and the corpus continues, so one flaky model call
// doesn't erase every other fixture's evidence. Only ctx cancellation
// (an operator-level abort, not a model error) still stops the run early.
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
			// Carry the last attempt's usage (RunFixture still reports it on
			// a parse failure) and the error text through to the recorded
			// score, instead of silently discarding both — a scorecard that
			// only shows TP/FN/FP looks identical whether the model missed
			// every finding or every call to it failed (review 2026-09-24,
			// MAJOR / MINOR).
			es := ErrorScore(fx)
			es.Usage = s.Usage
			es.ErrorMsg = err.Error()
			sc.Fixtures = append(sc.Fixtures, es)
			continue
		}
		sc.Fixtures = append(sc.Fixtures, s)
	}
	return sc, nil
}

// RunCorpusN runs the corpus n times (COMMITBRIEF_EVAL_RUNS, ADR-0043 §3's
// k), returning one Scorecard per run in order. Stops and returns the error
// immediately if any run itself fails to complete (ctx cancellation) —
// per-fixture errors within a run are already absorbed by RunCorpus and
// never reach this level.
func RunCorpusN(ctx context.Context, p provider.Provider, model string, fixtures []Fixture, n int) ([]Scorecard, error) {
	if n < 1 {
		n = 1
	}
	out := make([]Scorecard, 0, n)
	for i := 0; i < n; i++ {
		sc, err := RunCorpus(ctx, p, model, fixtures)
		if err != nil {
			return nil, fmt.Errorf("eval: run %d/%d: %w", i+1, n, err)
		}
		out = append(out, sc)
	}
	return out, nil
}
