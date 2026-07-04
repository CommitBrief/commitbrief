// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
	"github.com/CommitBrief/commitbrief/internal/render"
	"github.com/CommitBrief/commitbrief/internal/rules"
)

// TestCacheReplayFencedContentParses mirrors the cache-hit replay decision in
// runReview (review.go: `case cache.FormatJSON, "": ParseFindings(content)`).
// With Phase-0 salvage in ParseFindings (ADR-0031), a FormatJSON entry whose
// body was cached with a code fence renders as structured findings on replay,
// with no provider round-trip.
func TestCacheReplayFencedContentParses(t *testing.T) {
	fenced := "```json\n" + mock.DefaultResponseContent + "\n```"
	entry := cache.Entry{Result: cache.Result{Format: cache.FormatJSON, Content: fenced}}

	var findings []render.Finding
	switch entry.Result.Format {
	case cache.FormatJSON, "":
		findings, _ = render.ParseFindings(entry.Result.Content)
	}
	if len(findings) == 0 {
		t.Fatalf("cached fenced content should replay as structured findings; got %d", len(findings))
	}
}

func TestTryStructuredReviewHappyPath(t *testing.T) {
	m := mock.New()
	// Default response is already valid Findings JSON (DefaultResponseContent).
	out, err := tryStructuredReview(context.Background(), m, provider.Request{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Format != cache.FormatJSON {
		t.Errorf("format = %q, want %q", out.Format, cache.FormatJSON)
	}
	if !strings.Contains(out.Content, `"findings"`) {
		t.Errorf("content missing findings wrapper: %q", out.Content)
	}
	if m.ReviewCalls != 1 {
		t.Errorf("ReviewCalls = %d, want 1 (no retry on first-attempt success)", m.ReviewCalls)
	}
	if out.Retries != 0 {
		t.Errorf("Retries = %d, want 0 on first-attempt success", out.Retries)
	}
	if out.DegradeReason != "" {
		t.Errorf("DegradeReason = %q, want empty on success", out.DegradeReason)
	}
	if out.Usage.InputTokens == 0 {
		t.Error("usage should be populated")
	}
}

func TestTryStructuredReviewFencedValidFirstAttempt(t *testing.T) {
	// Phase-0 salvage (ADR-0031): valid findings JSON wrapped in a ```json
	// fence must parse on the FIRST attempt — zero retries, no degrade.
	m := mock.New()
	m.ResponseContent = "```json\n" + mock.DefaultResponseContent + "\n```"

	out, err := tryStructuredReview(context.Background(), m, provider.Request{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ReviewCalls != 1 {
		t.Errorf("ReviewCalls = %d, want 1 (fence salvage, no retry)", m.ReviewCalls)
	}
	if out.Format != cache.FormatJSON {
		t.Errorf("format = %q, want %q", out.Format, cache.FormatJSON)
	}
	if out.Retries != 0 {
		t.Errorf("Retries = %d, want 0", out.Retries)
	}
}

func TestTryStructuredReviewRetriesOnce(t *testing.T) {
	// First call returns prose; retry sees the same canned response (mock is
	// stateless) — both attempts fail and we mark markdown-fallback.
	m := mock.New()
	m.ResponseContent = "not actually JSON"

	out, err := tryStructuredReview(context.Background(), m, provider.Request{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ReviewCalls != 2 {
		t.Errorf("ReviewCalls = %d, want 2 (retry-once on parse failure)", m.ReviewCalls)
	}
	if out.Format != cache.FormatMarkdownFallback {
		t.Errorf("format = %q, want %q", out.Format, cache.FormatMarkdownFallback)
	}
	if out.Content != "not actually JSON" {
		t.Errorf("content = %q, want first-response text preserved", out.Content)
	}
	if out.Retries != 1 {
		t.Errorf("Retries = %d, want 1", out.Retries)
	}
	if out.DegradeReason != "prose" {
		t.Errorf("DegradeReason = %q, want %q (non-JSON commentary)", out.DegradeReason, "prose")
	}
	// Token usage should be summed across both attempts.
	wantInput := m.InputTokens * 2
	wantOutput := m.OutputTokens * 2
	if out.Usage.InputTokens != wantInput || out.Usage.OutputTokens != wantOutput {
		t.Errorf("usage = %+v, want input=%d output=%d (summed across 2 calls)",
			out.Usage, wantInput, wantOutput)
	}
}

func TestTryStructuredReviewRetryRecovers(t *testing.T) {
	// Stateful mock: first response invalid, second valid.
	validJSON := `{"findings":[{"severity":"info","file":"a.go","line":1,"title":"t","description":"d","suggestion":"s"}]}`
	m := &switchingMock{
		responses: []string{"first call broken", validJSON},
		usage:     provider.Usage{InputTokens: 50, OutputTokens: 10},
	}

	out, err := tryStructuredReview(context.Background(), m, provider.Request{}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.calls != 2 {
		t.Errorf("calls = %d, want 2", m.calls)
	}
	if out.Format != cache.FormatJSON {
		t.Errorf("format = %q, want %q on recovery", out.Format, cache.FormatJSON)
	}
	if out.Content != validJSON {
		t.Errorf("content should be retry response; got %q", out.Content)
	}
	if out.Retries != 1 {
		t.Errorf("Retries = %d, want 1 (recovered after one repair retry)", out.Retries)
	}
	if out.DegradeReason != "" {
		t.Errorf("DegradeReason = %q, want empty on recovery", out.DegradeReason)
	}
	// Tokens accumulate across both calls.
	if out.Usage.InputTokens != 100 || out.Usage.OutputTokens != 20 {
		t.Errorf("usage = %+v, want input=100 output=20 (summed)", out.Usage)
	}
}

func TestTryStructuredReviewRepairProseReset(t *testing.T) {
	// Prose first attempt → the repair retry must carry the hard schema-reset
	// directive, NOT the "complete the JSON" one.
	validJSON := `{"findings":[]}`
	m := &switchingMock{
		responses: []string{"Sure! Here are the findings I noticed:", validJSON},
		usage:     provider.Usage{InputTokens: 10, OutputTokens: 5},
	}

	out, err := tryStructuredReview(context.Background(), m, provider.Request{SystemPrompt: "BASE"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Format != cache.FormatJSON {
		t.Errorf("format = %q, want recovery to %q", out.Format, cache.FormatJSON)
	}
	if len(m.reqs) != 2 {
		t.Fatalf("expected 2 requests captured, got %d", len(m.reqs))
	}
	if !strings.Contains(m.reqs[1].SystemPrompt, rules.RepairSchemaReset) {
		t.Errorf("repair request system prompt missing schema-reset directive:\n%q", m.reqs[1].SystemPrompt)
	}
	if strings.Contains(m.reqs[1].SystemPrompt, rules.RepairJSONComplete) {
		t.Error("prose failure should NOT use the complete-the-JSON directive")
	}
}

func TestTryStructuredReviewRepairTruncatedJSON(t *testing.T) {
	// A truncated JSON attempt → the repair retry must use the complete-the-
	// JSON directive, embed the partial output, and raise the token ceiling.
	truncated := `{"findings":[{"severity":"info","file":"a.go",`
	validJSON := `{"findings":[]}`
	m := &switchingMock{
		responses: []string{truncated, validJSON},
		usage:     provider.Usage{InputTokens: 20, OutputTokens: 8},
	}

	out, err := tryStructuredReview(context.Background(), m, provider.Request{SystemPrompt: "BASE", UserPrompt: "DIFF"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Format != cache.FormatJSON {
		t.Errorf("format = %q, want recovery to %q", out.Format, cache.FormatJSON)
	}
	if len(m.reqs) != 2 {
		t.Fatalf("expected 2 requests captured, got %d", len(m.reqs))
	}
	repair := m.reqs[1]
	if !strings.Contains(repair.SystemPrompt, rules.RepairJSONComplete) {
		t.Errorf("repair request missing complete-the-JSON directive:\n%q", repair.SystemPrompt)
	}
	if !strings.Contains(repair.UserPrompt, truncated) {
		t.Errorf("repair user prompt should embed the partial output; got:\n%q", repair.UserPrompt)
	}
	if repair.MaxTokens != repairMaxTokens {
		t.Errorf("repair MaxTokens = %d, want %d (raised ceiling for truncation)", repair.MaxTokens, repairMaxTokens)
	}
}

func TestTryStructuredReviewBubblesFirstCallError(t *testing.T) {
	// Errors on the first call short-circuit before any retry — the caller
	// gets the provider error verbatim, no fallback content.
	m := mock.New()
	m.ReviewErr = errors.New("provider down")

	_, err := tryStructuredReview(context.Background(), m, provider.Request{}, nil)
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if m.ReviewCalls != 1 {
		t.Errorf("ReviewCalls = %d, want 1 (no retry on transport error)", m.ReviewCalls)
	}
}

func TestTryStructuredReviewRetryNetworkErrorFallsBack(t *testing.T) {
	// First call returns invalid JSON; retry fails with a network error.
	// We mark fallback and reuse the first response's text rather than
	// surfacing the retry error to the user — they still get *something*.
	m := &switchingMock{
		responses: []string{"first response", ""}, // second response ignored due to error
		errs:      []error{nil, errors.New("network blip")},
		usage:     provider.Usage{InputTokens: 30, OutputTokens: 5},
	}

	out, err := tryStructuredReview(context.Background(), m, provider.Request{}, nil)
	if err != nil {
		t.Fatalf("retry network error should not bubble; got %v", err)
	}
	if out.Format != cache.FormatMarkdownFallback {
		t.Errorf("format = %q, want %q after retry network error", out.Format, cache.FormatMarkdownFallback)
	}
	if out.Content != "first response" {
		t.Errorf("content = %q, want first response preserved", out.Content)
	}
	if out.Retries != 1 {
		t.Errorf("Retries = %d, want 1", out.Retries)
	}
	if out.DegradeReason != "retry-error" {
		t.Errorf("DegradeReason = %q, want %q", out.DegradeReason, "retry-error")
	}
	// Only the first call's usage counts when retry network-failed.
	if out.Usage.InputTokens != 30 {
		t.Errorf("usage.InputTokens = %d, want 30 (only first call counted)", out.Usage.InputTokens)
	}
}

// switchingMock returns a different canned response each call and records the
// requests it received, so tests can both simulate transient malformation that
// recovers on retry and assert on the repair prompt the retry carried.
type switchingMock struct {
	responses []string
	errs      []error
	usage     provider.Usage
	calls     int
	reqs      []provider.Request
}

func (s *switchingMock) Name() string                         { return "switching-mock" }
func (s *switchingMock) DefaultModel() string                 { return "model" }
func (s *switchingMock) ContextWindow(string) int             { return 100_000 }
func (s *switchingMock) EstimateTokens(t string) int          { return (len(t) + 3) / 4 }
func (s *switchingMock) Pricing(string) provider.Pricing      { return provider.Pricing{} }
func (s *switchingMock) TestConnection(context.Context) error { return nil }

func (s *switchingMock) Review(_ context.Context, req provider.Request) (provider.Response, error) {
	s.reqs = append(s.reqs, req)
	defer func() { s.calls++ }()
	idx := s.calls
	if idx >= len(s.responses) {
		idx = len(s.responses) - 1
	}
	if s.errs != nil && idx < len(s.errs) && s.errs[idx] != nil {
		return provider.Response{}, s.errs[idx]
	}
	return provider.Response{Content: s.responses[idx], Model: "model", Usage: s.usage}, nil
}
