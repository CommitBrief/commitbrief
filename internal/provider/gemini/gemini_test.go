// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
}

func TestModelsDefensiveCopy(t *testing.T) {
	a := Models()
	a[0] = "tampered"
	if Models()[0] == "tampered" {
		t.Error("Models() must return a defensive copy")
	}
}

func TestIsModelSupported(t *testing.T) {
	if !IsModelSupported(ModelPro2_5) {
		t.Error("gemini-2.5-pro should be supported")
	}
	if IsModelSupported("gpt-4o") {
		t.Error("OpenAI model should not be supported here")
	}
}

func TestContextWindow(t *testing.T) {
	if contextWindowFor(ModelPro2_5) != 2_000_000 {
		t.Errorf("pro 2.5 context window wrong: %d", contextWindowFor(ModelPro2_5))
	}
	if contextWindowFor("unknown") != defaultContextWindow {
		t.Error("unknown model should fall back to defaultContextWindow")
	}
}

func TestPricingLookup(t *testing.T) {
	p := pricingFor(ModelPro2_5)
	if p.InputPer1M == 0 || p.OutputPer1M == 0 {
		t.Errorf("pro pricing missing: %+v", p)
	}
	if p.CachedInputPer1M >= p.InputPer1M {
		t.Error("cached input should be cheaper than full input")
	}
	if pricingFor("unknown-model").InputPer1M != 0 {
		t.Error("unknown model should yield zero pricing")
	}
}

func TestNewMissingAPIKey(t *testing.T) {
	_, err := New(config.ProviderConfig{})
	if !errors.Is(err, provider.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestClientName(t *testing.T) {
	c, err := New(config.ProviderConfig{APIKey: "AIza-test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != Name {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestClientDefaultModelFromConfig(t *testing.T) {
	c, _ := New(config.ProviderConfig{APIKey: "x", Model: "gemini-custom"})
	if c.DefaultModel() != "gemini-custom" {
		t.Errorf("DefaultModel = %q, want gemini-custom", c.DefaultModel())
	}
}

func TestClientDefaultModelFallback(t *testing.T) {
	c, _ := New(config.ProviderConfig{APIKey: "x"})
	if c.DefaultModel() != DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", c.DefaultModel(), DefaultModel)
	}
}

func TestRegisteredViaInit(t *testing.T) {
	for _, n := range provider.Names() {
		if n == Name {
			return
		}
	}
	t.Errorf("gemini provider not registered in init(); Names() = %v", provider.Names())
}

func fakeGenerateContentServer(t *testing.T, content string, promptTok, candidateTok, cachedTok int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":generateContent") {
			http.NotFound(w, r)
			return
		}
		payload := map[string]any{
			"candidates": []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": content}},
					"role":  "model",
				},
				"finishReason": "STOP",
			}},
			"usageMetadata": map[string]any{
				"promptTokenCount":        promptTok,
				"candidatesTokenCount":    candidateTok,
				"cachedContentTokenCount": cachedTok,
				"totalTokenCount":         promptTok + candidateTok,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func TestReviewWithFakeServer(t *testing.T) {
	srv := fakeGenerateContentServer(t, "review here", 200, 80, 0)
	defer srv.Close()

	c, err := New(config.ProviderConfig{APIKey: "AIza-test", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Review(context.Background(), provider.Request{
		Model:        ModelPro2_5,
		SystemPrompt: "rules",
		UserPrompt:   "diff",
		MaxTokens:    256,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != "review here" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 200 || resp.Usage.OutputTokens != 80 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}

func TestReviewCachedInputReported(t *testing.T) {
	srv := fakeGenerateContentServer(t, "...", 500, 100, 400)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.CachedInputTokens != 400 {
		t.Errorf("CachedInputTokens = %d, want 400", resp.Usage.CachedInputTokens)
	}
}

func TestReviewUnauthorizedMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"code":    403,
				"message": "API key not valid. Please pass a valid API key.",
				"status":  "PERMISSION_DENIED",
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
				"code":    429,
				"message": "Quota exceeded for quota metric",
				"status":  "RESOURCE_EXHAUSTED",
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
	srv := fakeGenerateContentServer(t, "pong", 1, 1, 0)
	defer srv.Close()
	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	if err := c.TestConnection(context.Background()); err != nil {
		t.Errorf("TestConnection: %v", err)
	}
}

func fakeStreamingServer(t *testing.T, deltas []string, prompt, candidate, cached int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, ":streamGenerateContent") {
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
		for i, d := range deltas {
			candidates := []map[string]any{{
				"content": map[string]any{
					"parts": []map[string]any{{"text": d}},
					"role":  "model",
				},
			}}
			chunk := map[string]any{"candidates": candidates}
			if i == len(deltas)-1 {
				candidates[0]["finishReason"] = "STOP"
				chunk["usageMetadata"] = map[string]any{
					"promptTokenCount":        prompt,
					"candidatesTokenCount":    candidate,
					"cachedContentTokenCount": cached,
					"totalTokenCount":         prompt + candidate,
				}
			}
			writeData(chunk)
		}
	}))
}

func TestReviewStreamAssemblesDeltas(t *testing.T) {
	srv := fakeStreamingServer(t, []string{"hello ", "world", "!"}, 75, 25, 0)
	defer srv.Close()

	c, err := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	ch, err := c.ReviewStream(context.Background(), provider.Request{
		Model:      ModelPro2_5,
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
	if usage.InputTokens != 75 || usage.OutputTokens != 25 {
		t.Errorf("Usage = %+v, want input=75 output=25", usage)
	}
}

func TestReviewStreamCachedTokensReported(t *testing.T) {
	srv := fakeStreamingServer(t, []string{"x"}, 500, 10, 400)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	ch, _ := c.ReviewStream(context.Background(), provider.Request{Model: ModelPro2_5})

	var usage provider.Usage
	for ev := range ch {
		if ev.Type == provider.EventUsage {
			usage = ev.Usage
		}
	}
	if usage.CachedInputTokens != 400 {
		t.Errorf("CachedInputTokens = %d, want 400", usage.CachedInputTokens)
	}
}

func TestSystemInstructionPassed(t *testing.T) {
	// Verify that a non-empty SystemPrompt becomes a SystemInstruction in
	// the request body.
	var capturedSystem string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if si, ok := body["systemInstruction"].(map[string]any); ok {
			if parts, ok := si["parts"].([]any); ok && len(parts) > 0 {
				if part, ok := parts[0].(map[string]any); ok {
					if text, ok := part["text"].(string); ok {
						capturedSystem = text
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`)
	}))
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	_, err := c.Review(context.Background(), provider.Request{
		SystemPrompt: "you are a reviewer",
		UserPrompt:   "diff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if capturedSystem != "you are a reviewer" {
		t.Errorf("system instruction not propagated; got %q", capturedSystem)
	}
}
