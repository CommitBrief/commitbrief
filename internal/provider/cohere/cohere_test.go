// SPDX-License-Identifier: GPL-3.0-or-later

package cohere

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
	if len(Models()) != 3 {
		t.Errorf("Models() = %v, want 3", Models())
	}
	if !IsModelSupported(ModelCommandRPlus) || IsModelSupported("gpt-4o") {
		t.Error("model support check wrong")
	}
	Models()[0] = "tampered"
	if Models()[0] == "tampered" {
		t.Error("Models() must return a defensive copy")
	}
}

func TestPricingAndContextWindow(t *testing.T) {
	if p := pricingFor(ModelCommandRPlus); p.InputPer1M == 0 || p.OutputPer1M == 0 {
		t.Errorf("command-r-plus pricing missing: %+v", p)
	}
	if pricingFor("unknown").InputPer1M != 0 {
		t.Error("unknown model should yield zero pricing")
	}
	if contextWindowFor(ModelCommandA) != 256_000 {
		t.Errorf("command-a context window wrong: %d", contextWindowFor(ModelCommandA))
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

func TestNewDefaults(t *testing.T) {
	c, err := New(config.ProviderConfig{APIKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != Name || c.DefaultModel() != DefaultModel {
		t.Errorf("Name/DefaultModel wrong: %q / %q", c.Name(), c.DefaultModel())
	}
}

func TestRegisteredViaInit(t *testing.T) {
	for _, n := range provider.Names() {
		if n == Name {
			return
		}
	}
	t.Errorf("cohere not registered; Names() = %v", provider.Names())
}

func TestReviewWithFakeServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "x", "object": "chat.completion", "created": 1, "model": ModelCommandRPlus,
			"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "cohere review"}}},
			"usage": map[string]any{"prompt_tokens": 25, "completion_tokens": 9, "total_tokens": 34},
		})
	}))
	defer srv.Close()

	c, _ := New(config.ProviderConfig{APIKey: "k", BaseURL: srv.URL})
	resp, err := c.Review(context.Background(), provider.Request{UserPrompt: "diff", MaxTokens: 64})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != "cohere review" || resp.Usage.InputTokens != 25 {
		t.Errorf("resp = %q / %+v", resp.Content, resp.Usage)
	}
}
