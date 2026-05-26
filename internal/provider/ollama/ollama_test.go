package ollama

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

func TestModelsListNonEmpty(t *testing.T) {
	if len(Models()) == 0 {
		t.Error("Models() returned empty list; should provide fallback suggestions")
	}
}

func TestIsModelSupported(t *testing.T) {
	if !IsModelSupported("any-tag:latest") {
		t.Error("any non-empty model name should be supported (user might have pulled it)")
	}
	if IsModelSupported("") {
		t.Error("empty model name should not be supported")
	}
}

func TestContextWindow(t *testing.T) {
	if contextWindowFor("qwen2.5-coder:14b") != 32_768 {
		t.Errorf("known model context wrong: %d", contextWindowFor("qwen2.5-coder:14b"))
	}
	if contextWindowFor("unknown:model") != defaultContextWindow {
		t.Error("unknown model should fall back to defaultContextWindow")
	}
}

func TestPricingAlwaysZero(t *testing.T) {
	p := pricingFor("any-model")
	if p.InputPer1M != 0 || p.OutputPer1M != 0 || p.CachedInputPer1M != 0 {
		t.Errorf("Ollama pricing must be zero, got %+v", p)
	}
}

func TestNewDefaultBaseURL(t *testing.T) {
	p, err := New(config.ProviderConfig{})
	if err != nil {
		t.Fatal(err)
	}
	c, ok := p.(*Client)
	if !ok {
		t.Fatal("New did not return *Client")
	}
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
}

func TestNewTrimsTrailingSlash(t *testing.T) {
	p, _ := New(config.ProviderConfig{BaseURL: "http://gpu.lan:11434/"})
	c := p.(*Client)
	if c.baseURL != "http://gpu.lan:11434" {
		t.Errorf("trailing slash not trimmed: %q", c.baseURL)
	}
}

func TestClientName(t *testing.T) {
	p, _ := New(config.ProviderConfig{})
	if p.Name() != Name {
		t.Errorf("Name = %q", p.Name())
	}
}

func TestClientDefaultModelFromConfig(t *testing.T) {
	p, _ := New(config.ProviderConfig{Model: "qwen2.5-coder:32b"})
	if p.DefaultModel() != "qwen2.5-coder:32b" {
		t.Errorf("DefaultModel = %q", p.DefaultModel())
	}
}

func TestClientDefaultModelFallback(t *testing.T) {
	p, _ := New(config.ProviderConfig{})
	if p.DefaultModel() != DefaultModel {
		t.Errorf("DefaultModel = %q, want %q", p.DefaultModel(), DefaultModel)
	}
}

func TestRegisteredViaInit(t *testing.T) {
	for _, n := range provider.Names() {
		if n == Name {
			return
		}
	}
	t.Errorf("ollama provider not registered in init(); Names() = %v", provider.Names())
}

func fakeChatServer(t *testing.T, response string, promptCount, evalCount int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != chatPath {
			http.NotFound(w, r)
			return
		}
		payload := map[string]any{
			"model":             "qwen2.5-coder:14b",
			"created_at":        "2026-05-26T12:00:00Z",
			"message":           map[string]any{"role": "assistant", "content": response},
			"done":              true,
			"prompt_eval_count": promptCount,
			"eval_count":        evalCount,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
}

func TestReviewWithFakeServer(t *testing.T) {
	srv := fakeChatServer(t, "review output", 50, 25)
	defer srv.Close()

	p, _ := New(config.ProviderConfig{BaseURL: srv.URL})
	resp, err := p.Review(context.Background(), provider.Request{
		SystemPrompt: "rules",
		UserPrompt:   "diff",
		Model:        "qwen2.5-coder:14b",
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	if resp.Content != "review output" {
		t.Errorf("Content = %q", resp.Content)
	}
	if resp.Usage.InputTokens != 50 || resp.Usage.OutputTokens != 25 {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.Usage.CachedInputTokens != 0 {
		t.Error("Ollama has no prompt cache; CachedInputTokens must be zero")
	}
}

func TestReviewSendsSystemAndUserMessages(t *testing.T) {
	var capturedReq chatRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedReq)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"model":"m","message":{"role":"assistant","content":"ok"},"done":true,"prompt_eval_count":1,"eval_count":1}`)
	}))
	defer srv.Close()

	p, _ := New(config.ProviderConfig{BaseURL: srv.URL})
	_, err := p.Review(context.Background(), provider.Request{
		SystemPrompt: "system rules",
		UserPrompt:   "user diff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(capturedReq.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(capturedReq.Messages), capturedReq.Messages)
	}
	if capturedReq.Messages[0].Role != "system" || capturedReq.Messages[0].Content != "system rules" {
		t.Errorf("system message wrong: %+v", capturedReq.Messages[0])
	}
	if capturedReq.Messages[1].Role != "user" || capturedReq.Messages[1].Content != "user diff" {
		t.Errorf("user message wrong: %+v", capturedReq.Messages[1])
	}
	if capturedReq.Stream {
		t.Error("non-streaming Review should send stream=false")
	}
}

func TestReviewModelNotFoundMapsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"model 'doesntexist' not found"}`))
	}))
	defer srv.Close()

	p, _ := New(config.ProviderConfig{BaseURL: srv.URL})
	_, err := p.Review(context.Background(), provider.Request{
		Model:      "doesntexist",
		UserPrompt: "x",
	})
	if !errors.Is(err, provider.ErrModelNotSupported) {
		t.Errorf("err = %v, want wrapped ErrModelNotSupported", err)
	}
}

func TestTestConnectionUsesTagsEndpoint(t *testing.T) {
	var hitPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitPath = r.URL.Path
		fmt.Fprint(w, `{"models":[]}`)
	}))
	defer srv.Close()

	p, _ := New(config.ProviderConfig{BaseURL: srv.URL})
	if err := p.TestConnection(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hitPath != tagsPath {
		t.Errorf("TestConnection hit %q, want %q (no compute spent on a ping)", hitPath, tagsPath)
	}
}

func TestTestConnectionUnreachable(t *testing.T) {
	// Closed server — connection should fail.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	srv.Close()
	p, _ := New(config.ProviderConfig{BaseURL: srv.URL})
	if err := p.TestConnection(context.Background()); err == nil {
		t.Error("expected error against closed server")
	}
}

func TestStreamingAssemblesContent(t *testing.T) {
	chunks := []string{
		`{"model":"m","message":{"role":"assistant","content":"hello "},"done":false}`,
		`{"model":"m","message":{"role":"assistant","content":"world"},"done":false}`,
		`{"model":"m","message":{"role":"assistant","content":""},"done":true,"prompt_eval_count":7,"eval_count":2}`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, c := range chunks {
			_, _ = fmt.Fprintln(w, c)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer srv.Close()

	p, _ := New(config.ProviderConfig{BaseURL: srv.URL})
	ch, err := p.ReviewStream(context.Background(), provider.Request{
		UserPrompt: "x",
	})
	if err != nil {
		t.Fatal(err)
	}

	var content strings.Builder
	var finalUsage provider.Usage
	var sawDone bool
	for ev := range ch {
		switch ev.Type {
		case provider.EventDelta:
			content.WriteString(ev.Delta)
		case provider.EventUsage:
			finalUsage = ev.Usage
		case provider.EventDone:
			sawDone = true
		case provider.EventError:
			t.Fatalf("unexpected error event: %v", ev.Err)
		}
	}
	if content.String() != "hello world" {
		t.Errorf("assembled = %q, want %q", content.String(), "hello world")
	}
	if !sawDone {
		t.Error("expected EventDone")
	}
	if finalUsage.InputTokens != 7 || finalUsage.OutputTokens != 2 {
		t.Errorf("final usage wrong: %+v", finalUsage)
	}
}
