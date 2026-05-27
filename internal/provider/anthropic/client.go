// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared"

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
		return nil, fmt.Errorf("anthropic: %w", provider.ErrUnauthorized)
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	c := &Client{
		sdk:     sdk.NewClient(opts...),
		model:   cfg.Model,
		baseURL: cfg.BaseURL,
	}
	return c, nil
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
	msg, err := c.sdk.Messages.New(ctx, params)
	if err != nil {
		return provider.Response{}, mapError(err)
	}
	content := extractText(msg)
	// Prefer the structured tool_use payload (ADR-0014). The model is
	// instructed via tool_choice to call the report tool; if it complied,
	// the JSON document is the canonical Content. If it refused (some
	// models occasionally emit a text-only apology), fall back to the
	// text blocks and let the renderer degrade gracefully.
	if structured, ok := extractStructured(msg); ok {
		content = structured
	}
	return provider.Response{
		Content: content,
		Model:   string(msg.Model),
		Usage:   mapUsage(msg.Usage),
	}, nil
}

func (c *Client) ReviewStream(ctx context.Context, req provider.Request) (<-chan provider.Event, error) {
	params := c.buildParams(req)
	stream := c.sdk.Messages.NewStreaming(ctx, params)
	if stream == nil {
		return nil, errors.New("anthropic: nil stream returned")
	}
	return adaptStream(ctx, stream), nil
}

func (c *Client) TestConnection(ctx context.Context) error {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(c.DefaultModel()),
		MaxTokens: testPingMaxTok,
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(testPingPrompt)),
		},
	}
	if _, err := c.sdk.Messages.New(ctx, params); err != nil {
		return mapError(err)
	}
	return nil
}

func (c *Client) buildParams(req provider.Request) sdk.MessageNewParams {
	model := req.Model
	if model == "" {
		model = c.DefaultModel()
	}
	maxTokens := int64(req.MaxTokens)
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return sdk.MessageNewParams{
		Model:     sdk.Model(model),
		MaxTokens: maxTokens,
		System:    systemPromptWithCache(req.SystemPrompt),
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(req.UserPrompt)),
		},
		Tools:      []sdk.ToolUnionParam{buildReportTool()},
		ToolChoice: sdk.ToolChoiceParamOfTool(toolName),
	}
}

func extractText(msg *sdk.Message) string {
	var sb strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String()
}

func mapUsage(u sdk.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:       int(u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens),
		OutputTokens:      int(u.OutputTokens),
		CachedInputTokens: int(u.CacheReadInputTokens),
	}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.Error
	if errors.As(err, &apiErr) {
		switch apiErr.Type() {
		case shared.ErrorTypeAuthenticationError:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrUnauthorized, apiErr.Error())
		case shared.ErrorTypeRateLimitError:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrRateLimit, apiErr.Error())
		case shared.ErrorTypeTimeoutError:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrTimeout, apiErr.Error())
		case shared.ErrorTypeNotFoundError:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrModelNotSupported, apiErr.Error())
		}
		// Fall back to HTTP status for unmapped types.
		switch apiErr.StatusCode {
		case 401, 403:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrUnauthorized, apiErr.Error())
		case 429:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrRateLimit, apiErr.Error())
		case 404:
			return fmt.Errorf("anthropic: %w: %s", provider.ErrModelNotSupported, apiErr.Error())
		}
	}
	return fmt.Errorf("anthropic: %w", err)
}

func init() {
	provider.Register(Name, New)
}

var _ provider.Provider = (*Client)(nil)
