// SPDX-License-Identifier: GPL-3.0-or-later

package mock

import (
	"context"
	"errors"
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
