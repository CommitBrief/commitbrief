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
	if len(got) != 5 {
		t.Errorf("Models() length = %d, want 5", len(got))
	}
}

func TestModelsDefensiveCopy(t *testing.T) {
	a := Models()
	a[0] = "tampered"
	if Models()[0] == "tampered" {
		t.Error("Models() must return a defensive copy")
	}
}

// TestModelsWizardDefaultOrder locks Models()[0]: the setup wizard reads
// spec.Models (not DefaultModel) for its picker, so the first element is
// what an Enter keypress selects (internal/setup/wizard.go). New models
// must be appended, never inserted before it.
func TestModelsWizardDefaultOrder(t *testing.T) {
	got := Models()[0]
	if got != ModelPro31 {
		t.Errorf("Models()[0] = %q, want %q (wizard's implicit default)", got, ModelPro31)
	}
}

func TestIsModelSupported(t *testing.T) {
	if !IsModelSupported(ModelPro31) {
		t.Error("gemini-3.1-pro-preview should be supported")
	}
	if IsModelSupported("gpt-4o") {
		t.Error("OpenAI model should not be supported here")
	}
}

func TestContextWindow(t *testing.T) {
	if contextWindowFor(ModelPro31) != 1_000_000 {
		t.Errorf("pro 3.1 context window wrong: %d", contextWindowFor(ModelPro31))
	}
	if contextWindowFor("unknown") != defaultContextWindow {
		t.Error("unknown model should fall back to defaultContextWindow")
	}
}

func TestPricingLookup(t *testing.T) {
	p := pricingFor(ModelPro31)
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

// TestPricingTableCoversAllModels locks non-zero pricing for every catalog
// model, including gemini-3.8-flash and gemini-3.5-flash-lite added
// alongside the CachedInputPer1M fix.
func TestPricingTableCoversAllModels(t *testing.T) {
	for _, model := range Models() {
		t.Run(model, func(t *testing.T) {
			p := pricingFor(model)
			if p.InputPer1M == 0 || p.OutputPer1M == 0 {
				t.Errorf("pricingFor(%s) missing input/output rate: %+v", model, p)
			}
			if p.CachedInputPer1M >= p.InputPer1M {
				t.Errorf("pricingFor(%s): cached input should be cheaper than full input, got %+v", model, p)
			}
		})
	}
}

// TestPricingTableExactValues locks the exact per-1M-token rates in
// pricingTable against an independent transcription, so a typo'd digit
// (rather than a missing/zero rate, which TestPricingTableCoversAllModels
// already catches) fails a test instead of silently shipping.
func TestPricingTableExactValues(t *testing.T) {
	want := map[string]provider.Pricing{
		ModelPro31: {
			InputPer1M:       2.00,
			OutputPer1M:      12.00,
			CachedInputPer1M: 0.20,
		},
		ModelFlash35: {
			InputPer1M:       1.50,
			OutputPer1M:      9.00,
			CachedInputPer1M: 0.15,
		},
		ModelFlashLite31: {
			InputPer1M:       0.25,
			OutputPer1M:      1.50,
			CachedInputPer1M: 0.025,
		},
		ModelFlash38: {
			InputPer1M:       0.75,
			OutputPer1M:      3.75,
			CachedInputPer1M: 0.075,
		},
		ModelFlashLite35: {
			InputPer1M:       0.30,
			OutputPer1M:      2.50,
			CachedInputPer1M: 0.03,
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
		Model:        ModelPro31,
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

// rawServer replies to every :generateContent call with the given body and
// records the decoded request bodies.
func rawServer(t *testing.T, respBody string, captured *[]map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if captured != nil {
			*captured = append(*captured, body)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, respBody)
	}))
}

func TestMapUsageCountsThoughtTokens(t *testing.T) {
	srv := rawServer(t, `{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP"}],`+
		`"usageMetadata":{"promptTokenCount":100,"candidatesTokenCount":50,"thoughtsTokenCount":700,"totalTokenCount":850}}`, nil)
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage.OutputTokens != 750 {
		t.Errorf("OutputTokens = %d, want 750 (candidates 50 + thoughts 700)", resp.Usage.OutputTokens)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("InputTokens = %d, want 100", resp.Usage.InputTokens)
	}
}

func TestDefaultMaxTokensFor(t *testing.T) {
	cases := map[string]int32{
		ModelFlash38:     defaultThinkingMaxTokens,
		ModelFlash35:     defaultThinkingMaxTokens,
		ModelPro31:       defaultThinkingMaxTokens,
		ModelFlashLite31: defaultMaxTokens,
		ModelFlashLite35: defaultMaxTokens,
		"custom-model":   defaultMaxTokens,
	}
	for model, want := range cases {
		if got := defaultMaxTokensFor(model); got != want {
			t.Errorf("defaultMaxTokensFor(%s) = %d, want %d", model, got, want)
		}
	}
}

func TestMaxOutputTokensSentToServer(t *testing.T) {
	const okBody = `{"candidates":[{"content":{"parts":[{"text":"ok"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`
	cases := []struct {
		name  string
		model string
		max   int
		want  float64
	}{
		{"thinking model default", ModelFlash38, 0, defaultThinkingMaxTokens},
		{"lite model default", ModelFlashLite35, 0, defaultMaxTokens},
		{"explicit wins on thinking model", ModelFlash38, 2048, 2048},
		{"explicit larger than default", ModelPro31, 40000, 40000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodies []map[string]any
			srv := rawServer(t, okBody, &bodies)
			defer srv.Close()
			c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
			if _, err := c.Review(context.Background(), provider.Request{Model: tc.model, UserPrompt: "x", MaxTokens: tc.max}); err != nil {
				t.Fatal(err)
			}
			if len(bodies) != 1 {
				t.Fatalf("got %d requests, want 1", len(bodies))
			}
			gc, _ := bodies[0]["generationConfig"].(map[string]any)
			if got, _ := gc["maxOutputTokens"].(float64); got != tc.want {
				t.Errorf("maxOutputTokens = %v, want %v", gc["maxOutputTokens"], tc.want)
			}
			if _, ok := gc["thinkingConfig"]; ok {
				t.Error("thinkingConfig must not be sent (model default stays)")
			}
		})
	}
}

func TestReviewMaxTokensFinishReason(t *testing.T) {
	truncated := `{"candidates":[{"content":{"parts":[{"text":"{\"findings\":[{\"severity\":"}],"role":"model"},"finishReason":"MAX_TOKENS"}],` +
		`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":16000,"totalTokenCount":16030}}`
	emptyBody := `{"candidates":[{"content":{"role":"model"},"finishReason":"MAX_TOKENS"}],` +
		`"usageMetadata":{"promptTokenCount":10,"thoughtsTokenCount":16374,"totalTokenCount":16384}}`

	t.Run("structured truncated JSON passes through for the repair retry", func(t *testing.T) {
		srv := rawServer(t, truncated, nil)
		defer srv.Close()
		c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
		resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
		if err != nil {
			t.Fatalf("structured MAX_TOKENS must not error (repair retry needs the content): %v", err)
		}
		if resp.Content != `{"findings":[{"severity":` {
			t.Errorf("Content = %q", resp.Content)
		}
		// The doubled-ceiling heuristic in review.go reads OutputTokens, so it
		// has to include the thought tokens that actually exhausted the budget.
		if resp.Usage.OutputTokens != 16020 {
			t.Errorf("OutputTokens = %d, want 16020", resp.Usage.OutputTokens)
		}
	})

	t.Run("structured empty content passes through as empty", func(t *testing.T) {
		srv := rawServer(t, emptyBody, nil)
		defer srv.Close()
		c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
		resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "x"})
		if err != nil {
			t.Fatalf("structured MAX_TOKENS must not error: %v", err)
		}
		if resp.Content != "" || resp.Usage.OutputTokens != 16374 {
			t.Errorf("Content = %q, OutputTokens = %d", resp.Content, resp.Usage.OutputTokens)
		}
	})

	t.Run("free-form truncation is an explicit error", func(t *testing.T) {
		srv := rawServer(t, truncated, nil)
		defer srv.Close()
		c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
		resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "x", FreeForm: true})
		if err == nil || !strings.Contains(err.Error(), "cut off") {
			t.Errorf("err = %v, want an output-limit truncation error", err)
		}
		if err != nil && strings.Contains(err.Error(), "raise") {
			t.Errorf("error must not tell the user to raise a setting that does not exist: %v", err)
		}
		if err != nil && !strings.Contains(err.Error(), "cut off at the output token limit after 16020") {
			t.Errorf("default-language error should be the English catalog text, got: %v", err)
		}
		if resp.Usage.OutputTokens != 16020 {
			t.Errorf("billed usage must survive the error: OutputTokens = %d, want 16020", resp.Usage.OutputTokens)
		}
	})
}

func TestTruncatedErrorLocalized(t *testing.T) {
	en := newTruncatedError("en", 16020).Error()
	tr := newTruncatedError("tr", 16020).Error()
	if !strings.Contains(en, "gemini") || !strings.Contains(en, "16020") || !strings.Contains(en, "cut off") {
		t.Errorf("EN text = %q", en)
	}
	if !strings.Contains(tr, "gemini") || !strings.Contains(tr, "16020") || !strings.Contains(tr, "kesildi") {
		t.Errorf("TR text = %q", tr)
	}
	if en == tr {
		t.Error("EN and TR messages must differ")
	}
	// An unknown language falls back to English, never to an empty message.
	if got := newTruncatedError("xx", 5).Error(); !strings.Contains(got, "cut off") {
		t.Errorf("fallback text = %q", got)
	}
}

func TestFreeFormTruncationUsesRequestLang(t *testing.T) {
	body := `{"candidates":[{"content":{"parts":[{"text":"partial"}],"role":"model"},"finishReason":"MAX_TOKENS"}],` +
		`"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":20,"thoughtsTokenCount":16000,"totalTokenCount":16030}}`
	srv := rawServer(t, body, nil)
	defer srv.Close()
	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	_, err := c.Review(context.Background(), provider.Request{UserPrompt: "x", FreeForm: true, Lang: "tr"})
	if err == nil || !strings.Contains(err.Error(), "kesildi") {
		t.Errorf("err = %v, want the Turkish truncation message", err)
	}
}
