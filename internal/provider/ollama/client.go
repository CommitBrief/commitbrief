// SPDX-License-Identifier: GPL-3.0-or-later

package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	DefaultBaseURL   = "http://localhost:11434"
	chatPath         = "/api/chat"
	tagsPath         = "/api/tags"
	defaultMaxTokens = 4096
	requestTimeout   = 5 * time.Minute
)

type Client struct {
	baseURL string
	model   string
	http    *http.Client
}

func New(cfg config.ProviderConfig) (provider.Provider, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   cfg.Model,
		http:    &http.Client{Timeout: requestTimeout},
	}, nil
}

func (c *Client) Name() string { return Name }

func (c *Client) DefaultModel() string {
	if c.model != "" {
		return c.model
	}
	return DefaultModel
}

func (c *Client) ContextWindow(model string) int {
	if model == "" {
		model = c.DefaultModel()
	}
	return contextWindowFor(model)
}

func (c *Client) EstimateTokens(s string) int { return tokens.Estimate(s) }

func (c *Client) Pricing(model string) provider.Pricing { return pricingFor(model) }

func (c *Client) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	body := c.buildBody(req, false)
	resp, err := c.postJSON(ctx, chatPath, body)
	if err != nil {
		return provider.Response{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return provider.Response{}, mapHTTPError(resp)
	}
	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return provider.Response{}, fmt.Errorf("ollama: decode response: %w", err)
	}
	return provider.Response{
		Content: out.Message.Content,
		Model:   out.Model,
		Usage: provider.Usage{
			InputTokens:  out.PromptEvalCount,
			OutputTokens: out.EvalCount,
		},
	}, nil
}

func (c *Client) TestConnection(ctx context.Context) error {
	// /api/tags is the cheapest known-supported endpoint: it does not need
	// a loaded model and lets us detect Ollama servers without spending
	// inference time. A successful response confirms the server is
	// reachable; whether the user's configured model is installed is a
	// separate concern (the setup wizard surfaces /api/tags output).
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+tagsPath, nil)
	if err != nil {
		return fmt.Errorf("ollama: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ollama: GET %s: %w", c.baseURL+tagsPath, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return mapHTTPError(resp)
	}
	return nil
}

func (c *Client) buildBody(req provider.Request, stream bool) chatRequest {
	model := req.Model
	if model == "" {
		model = c.DefaultModel()
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	messages := make([]chatMessage, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, chatMessage{Role: "system", Content: req.SystemPrompt})
	}
	messages = append(messages, chatMessage{Role: "user", Content: req.UserPrompt})

	return chatRequest{
		Model:    model,
		Messages: messages,
		Stream:   stream,
		Format:   formatJSON,
		Options:  &chatOptions{NumPredict: maxTokens},
	}
}

func (c *Client) postJSON(ctx context.Context, path string, body chatRequest) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("ollama: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("ollama: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: POST %s: %w", c.baseURL+path, err)
	}
	return resp, nil
}

func mapHTTPError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(body))
	switch resp.StatusCode {
	case http.StatusNotFound:
		// /api/chat returns 404 when the model isn't installed locally.
		return fmt.Errorf("ollama: %w: %s", provider.ErrModelNotSupported, msg)
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return fmt.Errorf("ollama: %w: %s", provider.ErrTimeout, msg)
	}
	return fmt.Errorf("ollama: HTTP %d: %s", resp.StatusCode, msg)
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Format   string        `json:"format,omitempty"`
	Options  *chatOptions  `json:"options,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatOptions struct {
	NumPredict int `json:"num_predict,omitempty"`
}

type chatResponse struct {
	Model           string      `json:"model"`
	CreatedAt       string      `json:"created_at"`
	Message         chatMessage `json:"message"`
	Done            bool        `json:"done"`
	PromptEvalCount int         `json:"prompt_eval_count"`
	EvalCount       int         `json:"eval_count"`
}

// Compile-time interface guarantee
var _ provider.Provider = (*Client)(nil)

func init() {
	provider.Register(Name, New)
}
