// SPDX-License-Identifier: GPL-3.0-or-later

package openaicompat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/render"
)

const findingsJSON = `{"findings":[{"severity":"high","file":"main.go","line":7,` +
	`"title":"Nil deref","description":"p may be nil","suggestion":"check p"}]}`

// recorder is a fake chat-completions server that records every request and
// answers with the next queued content string.
type recorder struct {
	mu       sync.Mutex
	headers  []http.Header
	bodies   []map[string]any
	contents []string
	status   int
	errBody  string
}

func (r *recorder) handler(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, _ := io.ReadAll(req.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	r.headers = append(r.headers, req.Header.Clone())
	r.bodies = append(r.bodies, body)

	if !strings.HasSuffix(req.URL.Path, "/chat/completions") {
		http.NotFound(w, req)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.status != 0 {
		w.WriteHeader(r.status)
		_, _ = io.WriteString(w, r.errBody)
		return
	}
	content := ""
	if len(r.contents) > 0 {
		content, r.contents = r.contents[0], r.contents[1:]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "x", "object": "chat.completion", "created": 1, "model": "served-model",
		"choices": []map[string]any{{"index": 0, "finish_reason": "stop",
			"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 5, "total_tokens": 16},
	})
}

func newServer(t *testing.T, r *recorder) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(r.handler))
	t.Cleanup(srv.Close)
	return srv
}

func TestKeylessReviewPlainAndFenced(t *testing.T) {
	// Env that must never reach the endpoint.
	t.Setenv("OPENAI_API_KEY", "sk-leak")
	t.Setenv("OPENAI_ORG_ID", "org-leak")
	t.Setenv("OPENAI_PROJECT_ID", "proj-leak")

	rec := &recorder{contents: []string{findingsJSON, "```json\n" + findingsJSON + "\n```"}}
	srv := newServer(t, rec)

	p, err := New(config.ProviderConfig{BaseURL: srv.URL + "/v1", Model: "my-model"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for i := 0; i < 2; i++ {
		resp, err := p.Review(context.Background(), provider.Request{
			SystemPrompt: "sys", UserPrompt: "diff", MaxTokens: 64,
		})
		if err != nil {
			t.Fatalf("Review #%d: %v", i, err)
		}
		findings, err := render.ParseFindings(resp.Content)
		if err != nil {
			t.Fatalf("ParseFindings #%d: %v", i, err)
		}
		if len(findings) != 1 || findings[0].File != "main.go" || findings[0].Severity != render.SeverityHigh {
			t.Fatalf("findings #%d = %+v", i, findings)
		}
		if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 5 {
			t.Errorf("usage #%d = %+v", i, resp.Usage)
		}
	}

	if len(rec.bodies) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(rec.bodies))
	}
	for i, b := range rec.bodies {
		if _, ok := b["response_format"]; ok {
			t.Errorf("request #%d carries response_format: %v", i, b["response_format"])
		}
		if b["model"] != "my-model" {
			t.Errorf("request #%d model = %v", i, b["model"])
		}
	}
	for i, h := range rec.headers {
		if v := h.Get("Authorization"); v != "" {
			t.Errorf("request #%d has Authorization %q, want none", i, v)
		}
		if v := h.Get("OpenAI-Organization"); v != "" {
			t.Errorf("request #%d leaked OpenAI-Organization %q", i, v)
		}
		if v := h.Get("OpenAI-Project"); v != "" {
			t.Errorf("request #%d leaked OpenAI-Project %q", i, v)
		}
	}
}

func TestAPIKeySendsBearerAndIgnoresOpenAIEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-leak")
	t.Setenv("OPENAI_ORG_ID", "org-leak")
	t.Setenv("OPENAI_PROJECT_ID", "proj-leak")

	rec := &recorder{contents: []string{findingsJSON}}
	srv := newServer(t, rec)

	p, err := New(config.ProviderConfig{BaseURL: srv.URL, Model: "m", APIKey: "cfg-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := p.Review(context.Background(), provider.Request{UserPrompt: "diff"}); err != nil {
		t.Fatalf("Review: %v", err)
	}
	h := rec.headers[0]
	if got := h.Get("Authorization"); got != "Bearer cfg-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer cfg-key")
	}
	if h.Get("OpenAI-Organization") != "" || h.Get("OpenAI-Project") != "" {
		t.Errorf("org/project headers leaked: %v", h)
	}
}

func TestNewRequiresBaseURLAndModel(t *testing.T) {
	if _, err := New(config.ProviderConfig{Model: "m"}); err == nil || !strings.Contains(err.Error(), "base_url is required") {
		t.Errorf("missing base_url: err = %v", err)
	}
	if _, err := New(config.ProviderConfig{BaseURL: "http://localhost:8000/v1"}); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Errorf("missing model: err = %v", err)
	}
	if _, err := New(config.ProviderConfig{BaseURL: "   ", Model: "m"}); err == nil {
		t.Error("blank base_url must error")
	}
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	for _, u := range []string{"localhost:8000/v1", "ftp://host/v1", "http://", "://x"} {
		if _, err := New(config.ProviderConfig{BaseURL: u, Model: "m"}); err == nil {
			t.Errorf("base_url %q accepted, want error", u)
		}
	}
}

func TestDeclaredPricingAndContextWindow(t *testing.T) {
	p, err := New(config.ProviderConfig{
		BaseURL: "http://localhost:8000/v1", Model: "m", ContextWindow: 32768,
		Pricing: map[string]config.ModelPricing{"m": {InputPer1M: 0.5, OutputPer1M: 1.5, CachedInputPer1M: 0.1}},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := p.ContextWindow("m"); got != 32768 {
		t.Errorf("ContextWindow = %d, want 32768", got)
	}
	want := provider.Pricing{InputPer1M: 0.5, OutputPer1M: 1.5, CachedInputPer1M: 0.1}
	if got := p.Pricing("m"); got != want {
		t.Errorf("Pricing(m) = %+v, want %+v", got, want)
	}
	if got := p.Pricing(""); got != want {
		t.Errorf("Pricing(\"\") = %+v, want default-model price", got)
	}
	if got := p.Pricing("other"); got != (provider.Pricing{}) {
		t.Errorf("Pricing(other) = %+v, want zero", got)
	}
}

func TestUndeclaredLimitsAreZero(t *testing.T) {
	p, _ := New(config.ProviderConfig{BaseURL: "http://localhost:8000/v1", Model: "m"})
	if got := p.ContextWindow("m"); got != 0 {
		t.Errorf("ContextWindow = %d, want 0 (unknown)", got)
	}
	if got := p.Pricing("m"); got != (provider.Pricing{}) {
		t.Errorf("Pricing = %+v, want zero", got)
	}
	if p.Name() != Name || p.DefaultModel() != "m" {
		t.Errorf("Name/DefaultModel = %q/%q", p.Name(), p.DefaultModel())
	}
}

func TestMapError(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   error
	}{
		{401, `{"error":{"message":"bad key"}}`, provider.ErrUnauthorized},
		{403, `{"error":{"message":"nope"}}`, provider.ErrUnauthorized},
		{429, `{"error":{"message":"slow down"}}`, provider.ErrRateLimit},
		{404, `{"error":{"message":"no such model"}}`, provider.ErrModelNotSupported},
		{400, `{"error":{"message":"This model's maximum context length is 4096 tokens"}}`, provider.ErrContextTooLong},
	}
	for _, tc := range cases {
		rec := &recorder{status: tc.status, errBody: tc.body}
		srv := newServer(t, rec)
		p, _ := New(config.ProviderConfig{BaseURL: srv.URL, Model: "m"})
		_, err := p.Review(context.Background(), provider.Request{UserPrompt: "x"})
		if !errors.Is(err, tc.want) {
			t.Errorf("status %d: err = %v, want %v", tc.status, err, tc.want)
		}
	}

	// A 400 that is not about context length must stay a plain error.
	rec := &recorder{status: 400, errBody: `{"error":{"message":"bad param"}}`}
	srv := newServer(t, rec)
	p, _ := New(config.ProviderConfig{BaseURL: srv.URL, Model: "m"})
	_, err := p.Review(context.Background(), provider.Request{UserPrompt: "x"})
	if err == nil || errors.Is(err, provider.ErrContextTooLong) {
		t.Errorf("generic 400: err = %v", err)
	}
}

func TestTestConnection(t *testing.T) {
	rec := &recorder{contents: []string{"pong"}}
	srv := newServer(t, rec)
	p, _ := New(config.ProviderConfig{BaseURL: srv.URL, Model: "m"})
	if err := p.TestConnection(context.Background()); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
	if rec.bodies[0]["model"] != "m" {
		t.Errorf("ping model = %v", rec.bodies[0]["model"])
	}
}

func TestMetadataAndRegistration(t *testing.T) {
	m := Metadata()
	if m.Name != "openai-compatible" || m.Kind != provider.KindAPI {
		t.Errorf("Metadata = %+v", m)
	}
	if m.APIKeyEnv != "" || m.DefaultModel != "" || m.DefaultBaseURL != "" || len(m.Models) != 0 {
		t.Errorf("Metadata must carry no env/default/models: %+v", m)
	}
	if !m.PricingUserDeclared {
		t.Error("PricingUserDeclared must be true")
	}
	if _, err := provider.New(Name, config.ProviderConfig{BaseURL: "http://localhost:1/v1", Model: "m"}); err != nil {
		t.Errorf("registry New: %v", err)
	}
}
