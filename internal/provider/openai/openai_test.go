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
	if len(got) != 2 {
		t.Errorf("Models() length = %d, want 2", len(got))
	}
	want := map[string]bool{ModelGPT4o: true, ModelGPT4oMini: true}
	for _, m := range got {
		if !want[m] {
			t.Errorf("unexpected model %q", m)
		}
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

// fakeStreamingChatServer serves an SSE stream of ChatCompletionChunk
// events. The final chunk includes usage info (because we request
// include_usage=true on streaming requests).
func fakeStreamingChatServer(t *testing.T, deltas []string, prompt, completion, cached int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		writeData := func(payload map[string]any) {
			data, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}

		base := map[string]any{
			"id":      "chatcmpl-stream-test",
			"object":  "chat.completion.chunk",
			"created": 1700000000,
			"model":   ModelGPT4o,
		}

		// First chunk: role announcement.
		first := map[string]any{}
		for k, v := range base {
			first[k] = v
		}
		first["choices"] = []map[string]any{{
			"index":         0,
			"delta":         map[string]any{"role": "assistant", "content": ""},
			"finish_reason": nil,
		}}
		writeData(first)

		// Content delta chunks.
		for _, d := range deltas {
			chunk := map[string]any{}
			for k, v := range base {
				chunk[k] = v
			}
			chunk["choices"] = []map[string]any{{
				"index":         0,
				"delta":         map[string]any{"content": d},
				"finish_reason": nil,
			}}
			writeData(chunk)
		}

		// Final finish chunk (empty delta, finish_reason set).
		finish := map[string]any{}
		for k, v := range base {
			finish[k] = v
		}
		finish["choices"] = []map[string]any{{
			"index":         0,
			"delta":         map[string]any{},
			"finish_reason": "stop",
		}}
		writeData(finish)

		// Usage chunk (empty choices, populated usage; only present when
		// stream_options.include_usage=true).
		usage := map[string]any{}
		for k, v := range base {
			usage[k] = v
		}
		usage["choices"] = []map[string]any{}
		usage["usage"] = map[string]any{
			"prompt_tokens":     prompt,
			"completion_tokens": completion,
			"total_tokens":      prompt + completion,
			"prompt_tokens_details": map[string]any{
				"cached_tokens": cached,
			},
		}
		writeData(usage)

		// OpenAI signals end with [DONE].
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
}

func TestReviewStreamAssemblesDeltas(t *testing.T) {
	srv := fakeStreamingChatServer(t, []string{"hello ", "world", "!"}, 100, 50, 0)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	ch, err := c.ReviewStream(context.Background(), provider.Request{
		Model:      ModelGPT4o,
		UserPrompt: "test",
	})
	if err != nil {
		t.Fatalf("ReviewStream: %v", err)
	}

	var content strings.Builder
	var usage provider.Usage
	var sawDone bool
	for ev := range ch {
		switch ev.Type {
		case provider.EventDelta:
			content.WriteString(ev.Delta)
		case provider.EventUsage:
			usage = ev.Usage
		case provider.EventDone:
			sawDone = true
		case provider.EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if content.String() != "hello world!" {
		t.Errorf("assembled = %q, want %q", content.String(), "hello world!")
	}
	if !sawDone {
		t.Error("expected EventDone")
	}
	if usage.InputTokens != 100 || usage.OutputTokens != 50 {
		t.Errorf("Usage = %+v, want input=100 output=50", usage)
	}
}

func TestReviewStreamCachedTokensReported(t *testing.T) {
	srv := fakeStreamingChatServer(t, []string{"x"}, 1024, 5, 768)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	ch, _ := c.ReviewStream(context.Background(), provider.Request{Model: ModelGPT4o})

	var usage provider.Usage
	for ev := range ch {
		if ev.Type == provider.EventUsage {
			usage = ev.Usage
		}
	}
	if usage.CachedInputTokens != 768 {
		t.Errorf("CachedInputTokens = %d, want 768", usage.CachedInputTokens)
	}
}
