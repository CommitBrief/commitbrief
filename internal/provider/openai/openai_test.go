// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

func TestModelsList(t *testing.T) {
	got := Models()
	want := map[string]bool{
		ModelGPT4o:     true,
		ModelGPT4oMini: true,
		ModelGPT55:     true,
		ModelGPT54Mini: true,
		ModelGPT55Pro:  true,
		ModelGPT6Astra: true,
		ModelGPT6Sol:   true,
		ModelGPT6Luna:  true,
	}
	if len(got) != len(want) {
		t.Errorf("Models() length = %d, want %d", len(got), len(want))
	}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unexpected model %q", m)
		}
	}
}

// TestModelsWizardDefaultOrder locks Models()[0]: the setup wizard reads
// spec.Models (not DefaultModel) for its picker, so the first element is
// what an Enter keypress selects (internal/setup/wizard.go). New models
// must be appended, never inserted before it.
func TestModelsWizardDefaultOrder(t *testing.T) {
	got := Models()[0]
	if got != ModelGPT54Mini {
		t.Errorf("Models()[0] = %q, want %q (wizard's implicit default)", got, ModelGPT54Mini)
	}
}

func TestModelsDefensiveCopy(t *testing.T) {
	a := Models()
	a[0] = "tampered"
	if Models()[0] == "tampered" {
		t.Error("Models() must return a defensive copy")
	}
}

func TestIsModelSupported(t *testing.T) {
	if !IsModelSupported(ModelGPT4o) {
		t.Error("gpt-4o should be supported")
	}
	if IsModelSupported("claude-opus-4-7") {
		t.Error("Anthropic model should not be supported here")
	}
	if IsModelSupported("") {
		t.Error("empty model should not be supported")
	}
}

func TestContextWindow(t *testing.T) {
	if contextWindowFor(ModelGPT4o) != 128_000 {
		t.Errorf("gpt-4o context window wrong: %d", contextWindowFor(ModelGPT4o))
	}
	if contextWindowFor("unknown") != defaultContextWindow {
		t.Error("unknown model should fall back to defaultContextWindow")
	}
}

func TestPricingLookup(t *testing.T) {
	p := pricingFor(ModelGPT4o)
	if p.InputPer1M == 0 || p.OutputPer1M == 0 {
		t.Errorf("gpt-4o pricing missing: %+v", p)
	}
	if p.CachedInputPer1M >= p.InputPer1M {
		t.Error("cached input should be cheaper than full input")
	}

	zero := pricingFor("unknown-model")
	if zero.InputPer1M != 0 {
		t.Errorf("unknown model should yield zero pricing, got %+v", zero)
	}
}

// TestModelCapabilitiesTable locks usesResponsesAPI, defaultMaxTokensFor and
// pricingFor (non-zero) for every catalog model, including the gpt-6 family
// added alongside gpt-5.5-pro.
func TestModelCapabilitiesTable(t *testing.T) {
	cases := []struct {
		model             string
		wantResponsesAPI  bool
		wantMaxTokens     int64
		wantPricingIsZero bool
	}{
		{ModelGPT4o, false, defaultMaxTokens, false},
		{ModelGPT4oMini, false, defaultMaxTokens, false},
		{ModelGPT55, false, defaultReasoningMaxTokens, false},
		{ModelGPT54Mini, false, defaultReasoningMaxTokens, false},
		{ModelGPT55Pro, true, defaultReasoningMaxTokens, false},
		{ModelGPT6Astra, true, defaultReasoningMaxTokens, false},
		{ModelGPT6Sol, true, defaultReasoningMaxTokens, false},
		{ModelGPT6Luna, true, defaultReasoningMaxTokens, false},
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			if got := usesResponsesAPI(tc.model); got != tc.wantResponsesAPI {
				t.Errorf("usesResponsesAPI(%s) = %v, want %v", tc.model, got, tc.wantResponsesAPI)
			}
			if got := defaultMaxTokensFor(tc.model); got != tc.wantMaxTokens {
				t.Errorf("defaultMaxTokensFor(%s) = %d, want %d", tc.model, got, tc.wantMaxTokens)
			}
			p := pricingFor(tc.model)
			isZero := p.InputPer1M == 0 && p.OutputPer1M == 0
			if isZero != tc.wantPricingIsZero {
				t.Errorf("pricingFor(%s) = %+v, want zero=%v", tc.model, p, tc.wantPricingIsZero)
			}
		})
	}
}

// TestPricingTableExactValues locks the exact per-1M-token rates in
// pricingTable against an independent transcription, so a typo'd digit
// (rather than a missing/zero rate, which TestModelCapabilitiesTable already
// catches) fails a test instead of silently shipping.
func TestPricingTableExactValues(t *testing.T) {
	want := map[string]provider.Pricing{
		ModelGPT4o: {
			InputPer1M:       2.50,
			OutputPer1M:      10.00,
			CachedInputPer1M: 1.25,
		},
		ModelGPT4oMini: {
			InputPer1M:       0.15,
			OutputPer1M:      0.60,
			CachedInputPer1M: 0.075,
		},
		ModelGPT55: {
			InputPer1M:       5.00,
			OutputPer1M:      30.00,
			CachedInputPer1M: 0.50,
		},
		ModelGPT54Mini: {
			InputPer1M:       0.75,
			OutputPer1M:      4.50,
			CachedInputPer1M: 0.075,
		},
		ModelGPT55Pro: {
			InputPer1M:       30.00,
			OutputPer1M:      180.00,
			CachedInputPer1M: 0,
		},
		ModelGPT6Astra: {
			InputPer1M:       10.00,
			OutputPer1M:      50.00,
			CachedInputPer1M: 1.00,
		},
		ModelGPT6Sol: {
			InputPer1M:       2.00,
			OutputPer1M:      10.00,
			CachedInputPer1M: 0.20,
		},
		ModelGPT6Luna: {
			InputPer1M:       0.10,
			OutputPer1M:      0.50,
			CachedInputPer1M: 0.01,
		},
	}
	for _, model := range Models() {
		t.Run(model, func(t *testing.T) {
			w, ok := want[model]
			if !ok {
				t.Fatalf("no expected pricing recorded for %s in this test", model)
			}
			if got := pricingFor(model); got != w {
				t.Errorf("pricingFor(%s) = %+v, want %+v", model, got, w)
			}
		})
	}
}

func TestNewMissingAPIKey(t *testing.T) {
	_, err := New(config.ProviderConfig{})
	if !errors.Is(err, provider.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClientName(t *testing.T) {
	c, err := New(config.ProviderConfig{APIKey: "sk-test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != Name {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestClientDefaultModelFromConfig(t *testing.T) {
	c, _ := New(config.ProviderConfig{APIKey: "x", Model: "gpt-custom"})
	if c.DefaultModel() != "gpt-custom" {
		t.Errorf("DefaultModel = %q, want gpt-custom", c.DefaultModel())
	}
}

func TestClientDefaultModelFallback(t *testing.T) {
	c, _ := New(config.ProviderConfig{APIKey: "x"})
	if c.DefaultModel() != DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", c.DefaultModel(), DefaultModel)
	}
}

func TestRegisteredViaInit(t *testing.T) {
	names := provider.Names()
	for _, n := range names {
		if n == Name {
			return
		}
	}
	t.Errorf("openai provider not registered in init(); Names() = %v", names)
}

func fakeChatCompletionsServer(t *testing.T, content string, prompt, completion, cached int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		payload := map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"created": 1700000000,
			"model":   ModelGPT4o,
			"choices": []map[string]any{{
				"index":         0,
				"finish_reason": "stop",
				"message": map[string]any{
					"role":    "assistant",
					"content": content,
					"refusal": "",
				},
				"logprobs": nil,
			}},
			"usage": map[string]any{
				"prompt_tokens":     prompt,
				"completion_tokens": completion,
				"total_tokens":      prompt + completion,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": cached,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func TestReviewWithFakeServer(t *testing.T) {
	srv := fakeChatCompletionsServer(t, "review output here", 120, 60, 0)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "sk-test", BaseURL: srv.URL})
	resp, err := c.Review(context.Background(), provider.Request{
		Model:        ModelGPT4o,
		SystemPrompt: "rules",
		UserPrompt:   "diff",
		MaxTokens:    256,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != "review output here" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 120 || resp.Usage.OutputTokens != 60 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestReviewCachedInputReported(t *testing.T) {
	srv := fakeChatCompletionsServer(t, "...", 1000, 50, 800)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CachedInputTokens != 800 {
		t.Errorf("CachedInputTokens = %d, want 800", resp.Usage.CachedInputTokens)
	}
}

func TestReviewUnauthorizedMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":    "invalid_api_key",
				"message": "Invalid Authorization header",
				"code":    "invalid_api_key",
				"param":   "",
			},
		})
	}))
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "bad", BaseURL: srv.URL})
	_, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if !errors.Is(err, provider.ErrUnauthorized) {
		t.Errorf("err = %v, want wrapped ErrUnauthorized", err)
	}
}

func TestReviewRateLimitMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"type":    "rate_limit_exceeded",
				"message": "Too many requests",
				"code":    "rate_limit_exceeded",
				"param":   "",
			},
		})
	}))
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	_, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if !errors.Is(err, provider.ErrRateLimit) {
		t.Errorf("err = %v, want wrapped ErrRateLimit", err)
	}
}

func TestTestConnectionSuccess(t *testing.T) {
	srv := fakeChatCompletionsServer(t, "pong", 1, 1, 0)
	defer srv.Close()
	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	if err := c.TestConnection(context.Background()); err != nil {
		t.Errorf("TestConnection: %v", err)
	}
}
