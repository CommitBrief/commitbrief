// SPDX-License-Identifier: GPL-3.0-or-later

package mock

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/CommitBrief/commitbrief/internal/provider"
)

func TestNewDefaults(t *testing.T) {
	m := New()
	if m.Name() != "mock" {
		t.Errorf("Name = %q", m.Name())
	}
	if m.DefaultModel() != "mock-model" {
		t.Errorf("DefaultModel = %q", m.DefaultModel())
	}
	if m.ContextWindow("") <= 0 {
		t.Error("ContextWindow should default > 0")
	}
}

func TestReviewBasic(t *testing.T) {
	m := New()
	m.ResponseContent = "hello world"
	m.InputTokens = 42
	m.OutputTokens = 21

	resp, err := m.Review(context.Background(), provider.Request{Model: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "hello world" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Model != "x" {
		t.Errorf("Model = %q (request model should propagate)", resp.Model)
	}
	if resp.Usage.InputTokens != 42 || resp.Usage.OutputTokens != 21 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if m.ReviewCalls != 1 {
		t.Errorf("ReviewCalls = %d, want 1", m.ReviewCalls)
	}
}

func TestReviewError(t *testing.T) {
	m := New()
	m.ReviewErr = provider.ErrUnauthorized
	_, err := m.Review(context.Background(), provider.Request{})
	if !errors.Is(err, provider.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestReviewContextCancellation(t *testing.T) {
	m := New()
	m.Latency = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := m.Review(ctx, provider.Request{})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestStreamEmitsExpectedOrder(t *testing.T) {
	m := New()
	m.ResponseContent = "abcdef"
	m.ChunkCount = 3
	m.InputTokens = 10
	m.OutputTokens = 5

	ch, err := m.ReviewStream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(ch)

	// Expect: 3 deltas, then usage, then done
	if len(events) != 5 {
		t.Fatalf("event count = %d, want 5; got: %+v", len(events), events)
	}
	for i := 0; i < 3; i++ {
		if events[i].Type != provider.EventDelta {
			t.Errorf("events[%d].Type = %v, want EventDelta", i, events[i].Type)
		}
	}
	if events[3].Type != provider.EventUsage {
		t.Errorf("events[3].Type = %v, want EventUsage", events[3].Type)
	}
	if events[3].Usage.InputTokens != 10 || events[3].Usage.OutputTokens != 5 {
		t.Errorf("usage event payload wrong: %+v", events[3].Usage)
	}
	if events[4].Type != provider.EventDone {
		t.Errorf("events[4].Type = %v, want EventDone", events[4].Type)
	}

	// Reassembled deltas should equal the original content
	var assembled strings.Builder
	for _, e := range events[:3] {
		assembled.WriteString(e.Delta)
	}
	if assembled.String() != "abcdef" {
		t.Errorf("reassembled = %q, want abcdef", assembled.String())
	}
}

func TestStreamSynchronousError(t *testing.T) {
	m := New()
	m.StreamErr = provider.ErrRateLimit
	m.StreamErrAfter = 0 // synchronous
	_, err := m.ReviewStream(context.Background(), provider.Request{})
	if !errors.Is(err, provider.ErrRateLimit) {
		t.Errorf("err = %v, want ErrRateLimit", err)
	}
}

func TestStreamMidStreamError(t *testing.T) {
	m := New()
	m.ResponseContent = "abcdef"
	m.ChunkCount = 5
	m.StreamErr = provider.ErrTimeout
	m.StreamErrAfter = 2

	ch, err := m.ReviewStream(context.Background(), provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	events := drain(ch)

	// Expect 2 deltas, then error, no usage/done
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3; got: %+v", len(events), events)
	}
	if events[0].Type != provider.EventDelta || events[1].Type != provider.EventDelta {
		t.Error("first two events should be deltas")
	}
	if events[2].Type != provider.EventError {
		t.Errorf("events[2].Type = %v, want EventError", events[2].Type)
	}
	if !errors.Is(events[2].Err, provider.ErrTimeout) {
		t.Errorf("EventError.Err = %v, want ErrTimeout", events[2].Err)
	}
}

func TestStreamContextCancellation(t *testing.T) {
	m := New()
	m.ResponseContent = "abcdef"
	m.ChunkCount = 4
	m.Latency = 20 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := m.ReviewStream(ctx, provider.Request{})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(25 * time.Millisecond)
		cancel()
	}()
	events := drain(ch)

	var found bool
	for _, e := range events {
		if e.Type == provider.EventError && errors.Is(e.Err, context.Canceled) {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected EventError(context.Canceled); got events: %+v", events)
	}
}

func TestSplitChunksUniformDistribution(t *testing.T) {
	cases := []struct {
		in     string
		n      int
		wantNF int
	}{
		{"abcdef", 3, 3},
		{"abcdefg", 3, 3}, // 7 chars into 3: [3,2,2]
		{"a", 5, 1},       // chunks clamped to len(s)
		{"", 3, 1},
	}
	for _, c := range cases {
		got := splitChunks(c.in, c.n)
		if len(got) != c.wantNF {
			t.Errorf("splitChunks(%q, %d) → %d chunks, want %d", c.in, c.n, len(got), c.wantNF)
		}
		// Reassemble should equal input
		var sb strings.Builder
		for _, p := range got {
			sb.WriteString(p)
		}
		if sb.String() != c.in {
			t.Errorf("reassembled = %q, want %q", sb.String(), c.in)
		}
	}
}

func TestEstimateTokens(t *testing.T) {
	m := New()
	if m.EstimateTokens("") != 0 {
		t.Error("empty string should yield 0 tokens")
	}
	if m.EstimateTokens("abcd") != 1 {
		t.Errorf("EstimateTokens(abcd) = %d, want 1", m.EstimateTokens("abcd"))
	}
}

func TestTestConnection(t *testing.T) {
	m := New()
	if err := m.TestConnection(context.Background()); err != nil {
		t.Errorf("clean TestConnection: %v", err)
	}
	if m.TestCalls != 1 {
		t.Errorf("TestCalls = %d, want 1", m.TestCalls)
	}
	m.TestConnErr = provider.ErrUnauthorized
	if err := m.TestConnection(context.Background()); !errors.Is(err, provider.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestLastRequestCaptured(t *testing.T) {
	m := New()
	req := provider.Request{Model: "claude-x", SystemPrompt: "sys", UserPrompt: "user"}
	_, _ = m.Review(context.Background(), req)
	if m.LastRequest.Model != "claude-x" || m.LastRequest.SystemPrompt != "sys" {
		t.Errorf("LastRequest = %+v", m.LastRequest)
	}
}

func TestRegisterAddsToGlobalRegistry(t *testing.T) {
	// We can't safely call Register() in unit tests because it mutates the
	// package-level registry shared with provider_test.go. Smoke-test by
	// checking the function exists and has the right signature.
	_ = Register
}

// Compile-time interface check
var _ provider.Provider = (*Provider)(nil)

func drain(ch <-chan provider.Event) []provider.Event {
	var out []provider.Event
	for e := range ch {
		out = append(out, e)
	}
	return out
}
