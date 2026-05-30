// SPDX-License-Identifier: GPL-3.0-or-later

package openai

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	defaultMaxTokens = 4096
	// defaultReasoningMaxTokens is the output ceiling for GPT-5 reasoning
	// models when the caller specifies none — larger than defaultMaxTokens
	// because reasoning tokens are billed out of the same budget.
	defaultReasoningMaxTokens = 16384
	testPingPrompt            = "ping"
	testPingMaxTok            = 8
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

func (c *Client) EstimateTokens(s string) int { return tokens.Estimate(s) }

func (c *Client) Pricing(model string) provider.Pricing {
	if model == "" {
		model = c.DefaultModel()
	}
	return pricingFor(model)
}

func (c *Client) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	model := req.Model
	if model == "" {
		model = c.DefaultModel()
	}
	if usesResponsesAPI(model) {
		return c.reviewViaResponses(ctx, req, model)
	}
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

// reviewViaResponses drives a Responses-API-only model (gpt-5.5-pro). The
// call is synchronous and may take several minutes for the pro model; it
// honours the caller's context deadline (the SDK sets no client timeout of
// its own).
func (c *Client) reviewViaResponses(ctx context.Context, req provider.Request, model string) (provider.Response, error) {
	resp, err := c.sdk.Responses.New(ctx, c.buildResponsesParams(req, model))
	if err != nil {
		return provider.Response{}, mapError(err)
	}
	return provider.Response{
		Content: resp.OutputText(),
		Model:   resp.Model,
		Usage:   mapResponsesUsage(resp.Usage),
	}, nil
}

func (c *Client) TestConnection(ctx context.Context) error {
	model := c.DefaultModel()
	if usesResponsesAPI(model) {
		params := responses.ResponseNewParams{
			Model:           shared.ResponsesModel(model),
			MaxOutputTokens: sdk.Int(testPingMaxTok),
			Input:           responses.ResponseNewParamsInputUnion{OfString: sdk.String(testPingPrompt)},
		}
		if _, err := c.sdk.Responses.New(ctx, params); err != nil {
			return mapError(err)
		}
		return nil
	}
	params := sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(model),
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
		maxTokens = defaultMaxTokensFor(model)
	}

	messages := make([]sdk.ChatCompletionMessageParamUnion, 0, 2)
	if req.SystemPrompt != "" {
		messages = append(messages, sdk.SystemMessage(req.SystemPrompt))
	}
	messages = append(messages, sdk.UserMessage(req.UserPrompt))

	params := sdk.ChatCompletionNewParams{
		Model:               shared.ChatModel(model),
		MaxCompletionTokens: sdk.Int(maxTokens),
		Messages:            messages,
	}
	// Structured-findings JSON contract (ADR-0014). Omitted for FreeForm
	// (ADR-0015) so the model returns a plain-text completion.
	if !req.FreeForm {
		params.ResponseFormat = buildResponseFormat()
	}
	return params
}

func (c *Client) buildResponsesParams(req provider.Request, model string) responses.ResponseNewParams {
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokensFor(model)
	}

	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(model),
		MaxOutputTokens: sdk.Int(maxTokens),
		Input:           responses.ResponseNewParamsInputUnion{OfString: sdk.String(req.UserPrompt)},
	}
	if req.SystemPrompt != "" {
		params.Instructions = sdk.String(req.SystemPrompt)
	}
	// Structured-findings JSON contract (ADR-0014). Omitted for FreeForm
	// (ADR-0015) so the model returns a plain-text completion.
	if !req.FreeForm {
		params.Text = buildResponsesTextFormat()
	}
	return params
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

func mapResponsesUsage(u responses.ResponseUsage) provider.Usage {
	return provider.Usage{
		InputTokens:       int(u.InputTokens),
		OutputTokens:      int(u.OutputTokens),
		CachedInputTokens: int(u.InputTokensDetails.CachedTokens),
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
