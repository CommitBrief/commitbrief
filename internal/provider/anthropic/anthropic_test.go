// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

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
	if len(got) != 3 {
		t.Errorf("Models() length = %d, want 3", len(got))
	}
	want := map[string]bool{
		ModelOpus47:   true,
		ModelSonnet46: true,
		ModelHaiku45:  true,
	}
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
	if !IsModelSupported(ModelOpus47) {
		t.Error("Opus 4.7 should be supported")
	}
	if IsModelSupported("gpt-4o") {
		t.Error("OpenAI model should not be reported as supported")
	}
	if IsModelSupported("") {
		t.Error("empty model should not be supported")
	}
}

func TestContextWindow(t *testing.T) {
	if contextWindowFor(ModelOpus47) != 200_000 {
		t.Error("Opus 4.7 context window wrong")
	}
	if contextWindowFor(ModelSonnet46) != 1_000_000 {
		t.Error("Sonnet 4.6 should advertise the 1M window")
	}
	if contextWindowFor("unknown") != defaultContextWindow {
		t.Error("unknown model should fall back to defaultContextWindow")
	}
}

func TestPricingLookup(t *testing.T) {
	p := pricingFor(ModelOpus47)
	if p.InputPer1M == 0 || p.OutputPer1M == 0 {
		t.Errorf("Opus pricing missing: %+v", p)
	}
	if p.CachedInputPer1M >= p.InputPer1M {
		t.Error("cached input should be cheaper than full input")
	}

	zero := pricingFor("unknown-model")
	if zero.InputPer1M != 0 {
		t.Errorf("unknown model should yield zero pricing, got %+v", zero)
	}
}

func TestSystemPromptWithCacheEmpty(t *testing.T) {
	if got := systemPromptWithCache(""); got != nil {
		t.Errorf("empty prompt should yield nil, got %+v", got)
	}
}

func TestSystemPromptWithCacheSetsBlock(t *testing.T) {
	got := systemPromptWithCache("you are an assistant")
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Text != "you are an assistant" {
		t.Errorf("Text = %q", got[0].Text)
	}
	// CacheControl is a struct; with the zero value its Type defaults to
	// "ephemeral" via SDK marshaling. Serialize to JSON to confirm.
	data, err := json.Marshal(got[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"cache_control"`) {
		t.Errorf("serialized block missing cache_control: %s", data)
	}
	if !strings.Contains(string(data), `"ephemeral"`) {
		t.Errorf("cache_control should be ephemeral: %s", data)
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
	c, err := New(config.ProviderConfig{APIKey: "x", Model: "claude-custom"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultModel() != "claude-custom" {
		t.Errorf("DefaultModel should use config value, got %q", c.DefaultModel())
	}
}

func TestClientDefaultModelFallback(t *testing.T) {
	c, err := New(config.ProviderConfig{APIKey: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if c.DefaultModel() != DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", c.DefaultModel(), DefaultModel)
	}
}

func TestEstimateTokens(t *testing.T) {
	c, _ := New(config.ProviderConfig{APIKey: "x"})
	if c.EstimateTokens("") != 0 {
		t.Error("empty should yield 0")
	}
	if c.EstimateTokens("abcd") != 1 {
		t.Errorf("chars/4 broken")
	}
}

func TestRegisteredViaInit(t *testing.T) {
	// init() should have registered the anthropic factory by now.
	names := provider.Names()
	found := false
	for _, n := range names {
		if n == Name {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("anthropic provider not registered in init(); Names() = %v", names)
	}
}

// fakeMessageServer is a minimal httptest.Server that mimics Anthropic's
// /v1/messages endpoint for the non-streaming case. The SDK's request body
// is JSON; we don't deeply validate it, we just confirm a request arrived
// and respond with a canned text-block payload. After ADR-0014 the client
// prefers a tool_use block — use fakeMessageServerToolUse when you want to
// exercise the structured-output happy path.
func fakeMessageServer(t *testing.T, response string, inputTokens, outputTokens int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		payload := messagePayload(
			[]map[string]any{{"type": "text", "text": response}},
			inputTokens, outputTokens,
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

// fakeMessageServerToolUse responds with a tool_use content block carrying
// the supplied JSON `input`. Mirrors the response shape Anthropic returns
// when the model calls the `report_findings` tool.
func fakeMessageServerToolUse(t *testing.T, toolInput map[string]any, inputTokens, outputTokens int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		payload := messagePayload(
			[]map[string]any{
				{
					"type":   "tool_use",
					"id":     "tool_test_1",
					"name":   toolName,
					"input":  toolInput,
					"caller": map[string]any{"type": "direct"},
				},
			},
			inputTokens, outputTokens,
		)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func messagePayload(content []map[string]any, inputTokens, outputTokens int) map[string]any {
	return map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         ModelOpus47,
		"content":       content,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":                inputTokens,
			"output_tokens":               outputTokens,
			"cache_creation_input_tokens": 0,
			"cache_read_input_tokens":     0,
			"cache_creation":              map[string]any{},
			"server_tool_use":             map[string]any{},
			"service_tier":                "standard",
			"inference_geo":               "us",
		},
		"container": map[string]any{},
	}
}

func TestReviewWithFakeServerDegradesToText(t *testing.T) {
	// Model returned a text block instead of calling report_findings — the
	// client falls back to the text content so the renderer can graceful-
	// degrade (ADR-0014 §4). Exercise that fallback explicitly.
	srv := fakeMessageServer(t, "review output here", 100, 50)
	defer srv.Close()

	c, err := New(config.ProviderConfig{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Review(context.Background(), provider.Request{
		Model:        ModelOpus47,
		SystemPrompt: "rules",
		UserPrompt:   "diff",
		MaxTokens:    256,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != "review output here" {
		t.Errorf("Content = %q (expected text fallback)", resp.Content)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestReviewWithToolUseFakeServer(t *testing.T) {
	// Happy path: model calls report_findings; client extracts the JSON
	// payload, returns it as Content for the renderer to parse.
	toolInput := map[string]any{
		"findings": []map[string]any{
			{
				"severity":    "critical",
				"file":        "internal/auth/session.go",
				"line":        142,
				"title":       "SQL fragment built from request input",
				"description": "Concatenation feeds db.Query directly.",
			},
		},
	}
	srv := fakeMessageServerToolUse(t, toolInput, 100, 50)
	defer srv.Close()

	c, err := New(config.ProviderConfig{APIKey: "sk-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Review(context.Background(), provider.Request{
		Model:        ModelOpus47,
		SystemPrompt: "rules",
		UserPrompt:   "diff",
		MaxTokens:    256,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	// Content should be a JSON document with the structured findings — both
	// the top-level wrap and the critical finding's title must survive.
	if !strings.Contains(resp.Content, `"findings"`) {
		t.Errorf("Content missing findings wrapper: %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "SQL fragment built from request input") {
		t.Errorf("Content missing finding title: %q", resp.Content)
	}
	if resp.Usage.InputTokens != 100 || resp.Usage.OutputTokens != 50 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestReviewUnauthorizedMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "authentication_error",
				"message": "invalid x-api-key",
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "rate_limit_error",
				"message": "slow down",
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
	srv := fakeMessageServer(t, "pong", 1, 1)
	defer srv.Close()
	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	if err := c.TestConnection(context.Background()); err != nil {
		t.Errorf("TestConnection: %v", err)
	}
}

// fakeStreamingMessageServer responds to /v1/messages with a hand-rolled
// SSE stream that exercises every event type the adapter handles:
// message_start (initial usage), content_block_delta (multiple), and
// message_delta + message_stop (final usage).
func fakeStreamingMessageServer(t *testing.T, deltas []string, finalInput, finalOutput, cacheRead int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/messages") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("ResponseWriter does not support Flusher")
		}

		writeEvent := func(name string, payload map[string]any) {
			data, _ := json.Marshal(payload)
			_, _ = w.Write([]byte("event: " + name + "\n"))
			_, _ = w.Write([]byte("data: " + string(data) + "\n\n"))
			flusher.Flush()
		}

		writeEvent("message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":            "msg_test",
				"type":          "message",
				"role":          "assistant",
				"model":         ModelOpus47,
				"content":       []any{},
				"stop_reason":   nil,
				"stop_sequence": nil,
				"usage": map[string]any{
					"input_tokens":                finalInput,
					"output_tokens":               0,
					"cache_creation_input_tokens": 0,
					"cache_read_input_tokens":     cacheRead,
					"cache_creation":              map[string]any{},
					"server_tool_use":             map[string]any{},
					"service_tier":                "standard",
					"inference_geo":               "us",
				},
				"container": map[string]any{},
			},
		})

		writeEvent("content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         0,
			"content_block": map[string]any{"type": "text", "text": ""},
		})

		for _, d := range deltas {
			writeEvent("content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{"type": "text_delta", "text": d},
			})
		}

		writeEvent("content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": 0,
		})

		writeEvent("message_delta", map[string]any{
			"type": "message_delta",
			"delta": map[string]any{
				"stop_reason":   "end_turn",
				"stop_sequence": nil,
				"container":     map[string]any{},
				"stop_details":  map[string]any{},
			},
			"usage": map[string]any{
				"input_tokens":                finalInput,
				"output_tokens":               finalOutput,
				"cache_creation_input_tokens": 0,
				"cache_read_input_tokens":     cacheRead,
				"server_tool_use":             map[string]any{},
			},
		})

		writeEvent("message_stop", map[string]any{"type": "message_stop"})
	}))
}

func TestReviewStreamAssemblesDeltas(t *testing.T) {
	srv := fakeStreamingMessageServer(t, []string{"hello ", "world", "!"}, 50, 25, 0)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	ch, err := c.ReviewStream(context.Background(), provider.Request{
		Model:      ModelOpus47,
		UserPrompt: "test",
	})
	if err != nil {
		t.Fatalf("ReviewStream: %v", err)
	}

	var content strings.Builder
	var lastUsage provider.Usage
	var sawDone bool
	for ev := range ch {
		switch ev.Type {
		case provider.EventDelta:
			content.WriteString(ev.Delta)
		case provider.EventUsage:
			lastUsage = ev.Usage
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
	// Adapter sums input + cache buckets for the "total input" figure.
	if lastUsage.InputTokens != 50 {
		t.Errorf("InputTokens = %d, want 50 (final input_tokens)", lastUsage.InputTokens)
	}
	if lastUsage.OutputTokens != 25 {
		t.Errorf("OutputTokens = %d, want 25", lastUsage.OutputTokens)
	}
}

func TestReviewStreamCachedTokensReported(t *testing.T) {
	srv := fakeStreamingMessageServer(t, []string{"x"}, 100, 1, 80)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	ch, _ := c.ReviewStream(context.Background(), provider.Request{Model: ModelOpus47})

	var usage provider.Usage
	for ev := range ch {
		if ev.Type == provider.EventUsage {
			usage = ev.Usage
		}
	}
	if usage.CachedInputTokens != 80 {
		t.Errorf("CachedInputTokens = %d, want 80", usage.CachedInputTokens)
	}
}
