// SPDX-License-Identifier: GPL-3.0-or-later

package anthropic

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	defaultMaxTokens = 4096
	// defaultAdaptiveThinkingMaxTokens is the output ceiling for models in
	// adaptiveThinkingDefaultMaxTokensModels (models.go) when the caller
	// specifies none. Thinking tokens are billed out of the same max_tokens
	// budget as the visible response on these models, so the historical
	// 4096 can leave no room for a findings JSON; 16000 mirrors the
	// max_tokens value Anthropic's own adaptive-thinking examples use.
	// https://platform.claude.com/docs/en/build-with-claude/extended-thinking
	// (accessed 2026-09-24).
	defaultAdaptiveThinkingMaxTokens = 16000
	testPingPrompt                   = "ping"
	testPingMaxTok                   = 8
)

type Client struct {
	sdk     sdk.Client
	model   string
	baseURL string
	// timeout is the resolved --timeout / review.timeout value, applied
	// per request via option.WithRequestTimeout. Zero leaves the SDK's own
	// timeout policy in charge. See SetTimeout.
	timeout time.Duration
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

func (c *Client) EstimateTokens(s string) int { return tokens.Estimate(s) }

func (c *Client) Pricing(model string) provider.Pricing {
	if model == "" {
		model = c.DefaultModel()
	}
	return pricingFor(model)
}

// SetTimeout implements provider.TimeoutSetter. The SDK derives its own
// non-streaming timeout from max_tokens and caps it at 10 minutes —
// worse, it refuses outright ("streaming is required for operations that
// may take longer than 10 minutes") rather than waiting. Passing an
// explicit request timeout short-circuits that calculation, so a user who
// allows 20 minutes actually gets them. A non-positive d is ignored.
func (c *Client) SetTimeout(d time.Duration) {
	if d > 0 {
		c.timeout = d
	}
}

// requestOpts returns the per-request options for a call: the resolved
// timeout when one is set, nothing otherwise (leaving SDK defaults
// untouched).
func (c *Client) requestOpts() []option.RequestOption {
	if c.timeout <= 0 {
		return nil
	}
	return []option.RequestOption{option.WithRequestTimeout(c.timeout)}
}

func (c *Client) Review(ctx context.Context, req provider.Request) (provider.Response, error) {
	params := c.buildParams(req)
	msg, err := c.sdk.Messages.New(ctx, params, c.requestOpts()...)
	if err != nil {
		return provider.Response{}, mapError(err)
	}
	content := extractText(msg)
	// Prefer the structured tool_use payload (ADR-0014). The model is
	// instructed via tool_choice to call the report tool; if it complied,
	// the JSON document is the canonical Content. If it refused (some
	// models occasionally emit a text-only apology), fall back to the
	// text blocks and let the renderer degrade gracefully.
	//
	// FreeForm (ADR-0015) skips the tool entirely — the response is plain
	// text (e.g. a commit message), so the text blocks ARE the content.
	if !req.FreeForm {
		if structured, ok := extractStructured(msg); ok {
			content = structured
		}
	}
	return provider.Response{
		Content: content,
		Model:   string(msg.Model),
		Usage:   mapUsage(msg.Usage),
	}, nil
}

func (c *Client) TestConnection(ctx context.Context) error {
	params := sdk.MessageNewParams{
		Model:     sdk.Model(c.DefaultModel()),
		MaxTokens: testPingMaxTok,
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(testPingPrompt)),
		},
	}
	if _, err := c.sdk.Messages.New(ctx, params, c.requestOpts()...); err != nil {
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
		maxTokens = defaultMaxTokensFor(model)
	}
	params := sdk.MessageNewParams{
		Model:     sdk.Model(model),
		MaxTokens: maxTokens,
		System:    systemPromptWithCache(req.SystemPrompt),
		Messages: []sdk.MessageParam{
			sdk.NewUserMessage(sdk.NewTextBlock(req.UserPrompt)),
		},
	}
	// Structured-findings contract (ADR-0014): force the report tool. Skip
	// it for FreeForm (ADR-0015) so the model returns plain text.
	if !req.FreeForm {
		params.Tools = []sdk.ToolUnionParam{buildReportTool()}
		if supportsForcedToolChoice(model) {
			params.ToolChoice = sdk.ToolChoiceParamOfTool(toolName)
		} else {
			// Opus 5.5 and Fable 5.1 reject a forced tool_choice on every
			// request (see noForcedToolChoiceModels in models.go); fall
			// back to auto and let the model call the tool voluntarily. If
			// it returns plain text instead, the existing ADR-0031
			// fence-salvage/repair-retry path in the review pipeline
			// handles it — no separate fallback here.
			params.ToolChoice = sdk.ToolChoiceUnionParam{OfAuto: &sdk.ToolChoiceAutoParam{}}
		}
	}
	return params
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
	provider.RegisterMetadata(Metadata())
}

var _ provider.Provider = (*Client)(nil)
