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
		ModelOpus48:   true,
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
	if !IsModelSupported(ModelOpus48) {
		t.Error("Opus 4.8 should be supported")
	}
	if IsModelSupported("gpt-4o") {
		t.Error("OpenAI model should not be reported as supported")
	}
	if IsModelSupported("") {
		t.Error("empty model should not be supported")
	}
}

func TestContextWindow(t *testing.T) {
	if contextWindowFor(ModelOpus48) != 1_000_000 {
		t.Error("Opus 4.8 should advertise the 1M window")
	}
	if contextWindowFor(ModelSonnet46) != 1_000_000 {
		t.Error("Sonnet 4.6 should advertise the 1M window")
	}
	if contextWindowFor("unknown") != defaultContextWindow {
		t.Error("unknown model should fall back to defaultContextWindow")
	}
}

func TestPricingLookup(t *testing.T) {
	p := pricingFor(ModelOpus48)
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
		"model":         ModelOpus48,
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
		Model:        ModelOpus48,
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
		Model:        ModelOpus48,
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
