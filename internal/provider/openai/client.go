// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
)

const (
	defaultMaxTokens = 4096
	testPingPrompt   = "ping"
	testPingMaxTok   = 8
)

type Client struct {
	sdk     sdk.Client
	model   string
	baseURL string
}

func New(cfg config.ProviderConfig) (provider.Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("openai: %w", provider.ErrUnauthorized)
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	return &Client{
		sdk:     sdk.NewClient(opts...),
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
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

func (c *Client) EstimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}

func (c *Client) Pricing(model string) provider.Pricing {
	if model == "" {
		model = c.DefaultModel()
	}
	return pricingFor(model)
}

func (c *Client) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	params := c.buildParams(req)
	completion, err := c.sdk.Chat.Completions.New(ctx, params)
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
	params := sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(c.DefaultModel()),
		MaxCompletionTokens: sdk.Int(testPingMaxTok),
		Messages: []sdk.ChatCompletionMessageParamUnion{
			sdk.UserMessage(testPingPrompt),
		},
	}
	if _, err := c.sdk.Chat.Completions.New(ctx, params); err != nil {
		return mapError(err)
	}
	return nil
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

	return sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(model),
		MaxCompletionTokens: sdk.Int(maxTokens),
		Messages:            messages,
		ResponseFormat:      buildResponseFormat(),
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
			return fmt.Errorf("openai: %w: %s", provider.ErrUnauthorized, apiErr.Error())
		case 429:
			return fmt.Errorf("openai: %w: %s", provider.ErrRateLimit, apiErr.Error())
		case 404:
			return fmt.Errorf("openai: %w: %s", provider.ErrModelNotSupported, apiErr.Error())
		}
		if apiErr.Type == "context_length_exceeded" {
			return fmt.Errorf("openai: %w: %s", provider.ErrContextTooLong, apiErr.Error())
		}
	}
	return fmt.Errorf("openai: %w", err)
}

func init() {
	provider.Register(Name, New)
}

var _ provider.Provider = (*Client)(nil)
