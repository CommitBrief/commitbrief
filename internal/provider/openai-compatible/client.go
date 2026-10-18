// SPDX-License-Identifier: GPL-3.0-or-later

// Package openaicompat implements the Provider interface against any
// endpoint that speaks the OpenAI Chat Completions protocol (vLLM, LM
// Studio, OpenRouter, a corporate gateway, ...). It is configured purely
// through the providers.openai-compatible block: base_url and model are
// mandatory, everything else (key, prices, context window) is optional.
//
// Like deepseek it reuses the openai-go SDK and keeps structured output
// prompt-driven: no response_format is ever sent, because json_schema
// support is uneven across OpenAI-compatible servers and an unsupporting
// one answers 400. JSON shape comes from the <response_format> prompt
// section plus the fence-salvage and repair-retry/degrade pipeline
// (ADR-0014, ADR-0031).
package openaicompat

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	sdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	// Name is the registry key and the providers.<name> config block.
	Name = "openai-compatible"

	defaultMaxTokens = 4096
	testPingPrompt   = "ping"
	testPingMaxTok   = 8
)

// Client talks to a user-configured OpenAI-compatible endpoint.
type Client struct {
	sdk           sdk.Client
	model         string
	contextWindow int
	pricing       map[string]config.ModelPricing
}

// New validates the config and builds a client. base_url and model have no
// defaults — there is no sensible one for "any compatible server".
//
// cfg.APIKey is expected to be already resolved by the caller (api_key_env >
// api_key). An empty key means an anonymous endpoint: the Authorization
// header is removed entirely instead of being sent as "Bearer ".
func New(cfg config.ProviderConfig) (provider.Provider, error) {
	baseURL := strings.TrimSpace(cfg.BaseURL)
	if baseURL == "" {
		return nil, errors.New("openai-compatible: providers.openai-compatible.base_url is required (e.g. http://localhost:8000/v1)")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		return nil, errors.New("openai-compatible: providers.openai-compatible.model is required")
	}
	if err := validateBaseURL(baseURL); err != nil {
		return nil, err
	}

	// openai-go's NewClient applies DefaultClientOptions() first, which
	// reads OPENAI_API_KEY, OPENAI_ORG_ID and OPENAI_PROJECT_ID from the
	// environment. None of those belong to a third-party endpoint, so the
	// org/project headers are always deleted, and the Authorization header
	// is either replaced by the explicit key or deleted.
	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithHeaderDel("OpenAI-Organization"),
		option.WithHeaderDel("OpenAI-Project"),
	}
	if cfg.APIKey != "" {
		opts = append(opts, option.WithAPIKey(cfg.APIKey))
	} else {
		opts = append(opts, option.WithHeaderDel("authorization"))
	}

	return &Client{
		sdk:           sdk.NewClient(opts...),
		model:         cfg.Model,
		contextWindow: cfg.ContextWindow,
		pricing:       cfg.Pricing,
	}, nil
}

func validateBaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("openai-compatible: base_url %q is not a valid URL: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("openai-compatible: base_url %q must be an http:// or https:// URL with a host", raw)
	}
	return nil
}

func (c *Client) Name() string { return Name }

func (c *Client) DefaultModel() string { return c.model }

// ContextWindow returns the configured context_window, or 0 when it was not
// declared. Zero means "unknown": the token preflight then never blocks.
func (c *Client) ContextWindow(string) int {
	if c.contextWindow > 0 {
		return c.contextWindow
	}
	return 0
}

func (c *Client) EstimateTokens(s string) int { return tokens.Estimate(s) }

// Pricing returns the user-declared price for the model, zero when none was
// declared (cost is then reported as unknown by the CLI).
func (c *Client) Pricing(model string) provider.Pricing {
	if model == "" {
		model = c.DefaultModel()
	}
	return pricingFor(c.pricing, model)
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
	// No response_format — JSON is prompt-driven (see package doc).
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
			return fmt.Errorf("openai-compatible: %w: %s", provider.ErrUnauthorized, apiErr.Error())
		case 429:
			return fmt.Errorf("openai-compatible: %w: %s", provider.ErrRateLimit, apiErr.Error())
		case 404:
			return fmt.Errorf("openai-compatible: %w: %s", provider.ErrModelNotSupported, apiErr.Error())
		case 400:
			if isContextLengthError(apiErr.Error()) {
				return fmt.Errorf("openai-compatible: %w: %s", provider.ErrContextTooLong, apiErr.Error())
			}
		}
	}
	return fmt.Errorf("openai-compatible: %w", err)
}

// isContextLengthError recognises the context-overflow wording used by vLLM,
// LM Studio and OpenAI-style servers.
func isContextLengthError(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "context length") ||
		strings.Contains(m, "context_length") ||
		strings.Contains(m, "maximum context") ||
		strings.Contains(m, "context window")
}

func init() {
	provider.Register(Name, New)
	provider.RegisterMetadata(Metadata())
}

var _ provider.Provider = (*Client)(nil)
