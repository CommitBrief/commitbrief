package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/cache"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/provider/mock"
)

func TestTryStructuredReviewHappyPath(t *testing.T) {
	m := mock.New()
	// Default response is already valid Findings JSON (DefaultResponseContent).
	content, usage, format, err := tryStructuredReview(context.Background(), m, provider.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if format != cache.FormatJSON {
		t.Errorf("format = %q, want %q", format, cache.FormatJSON)
	}
	if !strings.Contains(content, `"findings"`) {
		t.Errorf("content missing findings wrapper: %q", content)
	}
	if m.ReviewCalls != 1 {
		t.Errorf("ReviewCalls = %d, want 1 (no retry on first-attempt success)", m.ReviewCalls)
	}
	if usage.InputTokens == 0 {
		t.Error("usage should be populated")
	}
}

func TestTryStructuredReviewRetriesOnce(t *testing.T) {
	// First call returns invalid JSON; retry will see the same canned
	// response (mock is stateless) — so both calls fail and we mark
	// markdown-fallback.
	m := mock.New()
	m.ResponseContent = "not actually JSON"

	content, usage, format, err := tryStructuredReview(context.Background(), m, provider.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.ReviewCalls != 2 {
		t.Errorf("ReviewCalls = %d, want 2 (retry-once on parse failure)", m.ReviewCalls)
	}
	if format != cache.FormatMarkdownFallback {
		t.Errorf("format = %q, want %q", format, cache.FormatMarkdownFallback)
	}
	if content != "not actually JSON" {
		t.Errorf("content = %q, want first-response text preserved", content)
	}
	// Token usage should be summed across both attempts.
	wantInput := m.InputTokens * 2
	wantOutput := m.OutputTokens * 2
	if usage.InputTokens != wantInput || usage.OutputTokens != wantOutput {
		t.Errorf("usage = %+v, want input=%d output=%d (summed across 2 calls)",
			usage, wantInput, wantOutput)
	}
}

func TestTryStructuredReviewRetryRecovers(t *testing.T) {
	// Stateful mock: first response invalid, second valid.
	validJSON := `{"findings":[{"severity":"info","file":"a.go","line":1,"title":"t","description":"d"}]}`
	m := &switchingMock{
		responses: []string{"first call broken", validJSON},
		usage:     provider.Usage{InputTokens: 50, OutputTokens: 10},
	}

	content, usage, format, err := tryStructuredReview(context.Background(), m, provider.Request{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.calls != 2 {
		t.Errorf("calls = %d, want 2", m.calls)
	}
	if format != cache.FormatJSON {
		t.Errorf("format = %q, want %q on recovery", format, cache.FormatJSON)
	}
	if content != validJSON {
		t.Errorf("content should be retry response; got %q", content)
	}
	// Tokens accumulate across both calls.
	if usage.InputTokens != 100 || usage.OutputTokens != 20 {
		t.Errorf("usage = %+v, want input=100 output=20 (summed)", usage)
	}
}

func TestTryStructuredReviewBubblesFirstCallError(t *testing.T) {
	// Errors on the first call short-circuit before any retry — the caller
	// gets the provider error verbatim, no fallback content.
	m := mock.New()
	m.ReviewErr = errors.New("provider down")

	_, _, _, err := tryStructuredReview(context.Background(), m, provider.Request{})
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

	content, usage, format, err := tryStructuredReview(context.Background(), m, provider.Request{})
	if err != nil {
		t.Fatalf("retry network error should not bubble; got %v", err)
	}
	if format != cache.FormatMarkdownFallback {
		t.Errorf("format = %q, want %q after retry network error", format, cache.FormatMarkdownFallback)
	}
	if content != "first response" {
		t.Errorf("content = %q, want first response preserved", content)
	}
	// Only the first call's usage counts when retry network-failed.
	if usage.InputTokens != 30 {
		t.Errorf("usage.InputTokens = %d, want 30 (only first call counted)", usage.InputTokens)
	}
}

// switchingMock returns a different canned response each call. Used to
// simulate transient malformation that recovers on retry.
type switchingMock struct {
	responses []string
	errs      []error
	usage     provider.Usage
	calls     int
}

func (s *switchingMock) Name() string                                  { return "switching-mock" }
func (s *switchingMock) DefaultModel() string                          { return "model" }
func (s *switchingMock) ContextWindow(string) int                      { return 100_000 }
func (s *switchingMock) EstimateTokens(t string) int                   { return (len(t) + 3) / 4 }
func (s *switchingMock) Pricing(string) provider.Pricing               { return provider.Pricing{} }
func (s *switchingMock) TestConnection(context.Context) error          { return nil }
func (s *switchingMock) ReviewStream(context.Context, provider.Request) (<-chan provider.Event, error) {
	return nil, errors.New("not used")
}

func (s *switchingMock) Review(_ context.Context, _ provider.Request) (provider.Response, error) {
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
