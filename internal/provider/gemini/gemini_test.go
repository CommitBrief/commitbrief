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
