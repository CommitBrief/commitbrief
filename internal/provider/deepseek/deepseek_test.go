// SPDX-License-Identifier: GPL-3.0-or-later

package deepseek

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

func TestModelsAndSupport(t *testing.T) {
	if len(Models()) != 2 {
		t.Errorf("Models() = %v, want 2", Models())
	}
	if !IsModelSupported(ModelChat) || IsModelSupported("gpt-4o") {
		t.Error("model support check wrong")
	}
	Models()[0] = "tampered"
	if Models()[0] == "tampered" {
		t.Error("Models() must return a defensive copy")
	}
}

func TestPricingAndContextWindow(t *testing.T) {
	p := pricingFor(ModelChat)
	if p.InputPer1M == 0 || p.OutputPer1M == 0 {
		t.Errorf("deepseek-chat pricing missing: %+v", p)
	}
	if pricingFor("unknown").InputPer1M != 0 {
		t.Error("unknown model should yield zero pricing")
	}
	if contextWindowFor("unknown") != defaultContextWindow {
		t.Error("unknown model should fall back to default context window")
	}
}

func TestNewMissingAPIKey(t *testing.T) {
	if _, err := New(config.ProviderConfig{}); !errors.Is(err, provider.ErrUnauthorized) {
		t.Errorf("err = %v, want ErrUnauthorized", err)
	}
}

func TestNewDefaultsBaseURLAndModel(t *testing.T) {
	c, err := New(config.ProviderConfig{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != Name {
		t.Errorf("Name = %q", c.Name())
	}
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
	t.Errorf("deepseek not registered; Names() = %v", provider.Names())
}

func TestReviewWithFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "created": 1, "model": ModelChat,
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "deepseek review"}}},
			"usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 12, "total_tokens": 42},
		})
	}))
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "diff", MaxTokens: 64})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != "deepseek review" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 30 || resp.Usage.OutputTokens != 12 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
}
