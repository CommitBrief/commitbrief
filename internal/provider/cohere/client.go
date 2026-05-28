// SPDX-License-Identifier: GPL-3.0-or-later

// Package cohere implements the Provider interface against Cohere's
// OpenAI-compatibility endpoint (https://api.cohere.ai/compatibility/v1),
// reusing the openai-go SDK — no new dependency. Structured output is
// prompt-driven (no response_format); JSON shape comes from the system
// prompt's contract plus the retry-once-then-degrade pipeline (ADR-0014
// §4), the same way Ollama works.
package cohere

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	defaultBaseURL   = "https://api.cohere.ai/compatibility/v1"
	defaultMaxTokens = 4096
	testPingPrompt   = "ping"
	testPingMaxTok   = 8
)

type Client struct {
	sdk   sdk.Client
	model string
}

func New(cfg config.ProviderConfig) (provider.Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("cohere: %w", provider.ErrUnauthorized)
	}
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	return &Client{
		sdk:   sdk.NewClient(option.WithAPIKey(cfg.APIKey), option.WithBaseURL(baseURL)),
		model: cfg.Model,
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

func (c *Client) Pricing(model string) provider.Pricing {
	if model == "" {
		model = c.DefaultModel()
	}
	return pricingFor(model)
}

func (c *Client) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	completion, err := c.sdk.Chat.Completions.New(ctx, c.buildParams(req))
	if err != nil {
		return provider.Response{}, mapError(err)
	}
	return provider.Response{
		Content: extractText(completion),
		Model:   completion.Model,
		Usage:   mapUsage(completion.Usage),
	}, nil
}

func (c *Client) TestConnection(ctx context.Context) error {
	_, err := c.sdk.Chat.Completions.New(ctx, sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(c.DefaultModel()),
		MaxCompletionTokens: sdk.Int(testPingMaxTok),
		Messages:            []sdk.ChatCompletionMessageParamUnion{sdk.UserMessage(testPingPrompt)},
	})
	return mapError(err)
}

func (c *Client) buildParams(req provider.Request) sdk.ChatCompletionNewParams {
	model := req.Model
	if model == "" {
		model = c.DefaultModel()
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	messages := make([]sdk.ChatCompletionMessageParamUnion, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, sdk.SystemMessage(req.SystemPrompt))
	}
	messages = append(messages, sdk.UserMessage(req.UserPrompt))
	// No response_format — JSON is prompt-driven (retry/degrade covers
	// non-conforming output). FreeForm (ADR-0015) is naturally satisfied.
	return sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(model),
		MaxCompletionTokens: sdk.Int(maxTokens),
		Messages:            messages,
	}
}

func extractText(c *sdk.ChatCompletion) string {
	if c == nil || len(c.Choices) == 0 {
		return ""
	}
	return c.Choices[0].Message.Content
}

func mapUsage(u sdk.CompletionUsage) provider.Usage {
	return provider.Usage{
		InputTokens:       int(u.PromptTokens),
		OutputTokens:      int(u.CompletionTokens),
		CachedInputTokens: int(u.PromptTokensDetails.CachedTokens),
	}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("cohere: %w: %s", provider.ErrUnauthorized, apiErr.Error())
		case 429:
			return fmt.Errorf("cohere: %w: %s", provider.ErrRateLimit, apiErr.Error())
		case 404:
			return fmt.Errorf("cohere: %w: %s", provider.ErrModelNotSupported, apiErr.Error())
		}
	}
	return fmt.Errorf("cohere: %w", err)
}

func init() {
	provider.Register(Name, New)
}

var _ provider.Provider = (*Client)(nil)
