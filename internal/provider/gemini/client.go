// SPDX-License-Identifier: GPL-3.0-or-later

package gemini

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"

	sdk "google.golang.org/genai"

	"github.com/CommitBrief/commitbrief/internal/config"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	defaultMaxTokens = 4096
	testPingPrompt   = "ping"
	testPingMaxTok   = 8
)

type Client struct {
	sdk     *sdk.Client
	model   string
	baseURL string
}

func New(cfg config.ProviderConfig) (provider.Provider, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini: %w", provider.ErrUnauthorized)
	}
	clientCfg := &sdk.ClientConfig{APIKey: cfg.APIKey}
	if cfg.BaseURL != "" {
		clientCfg.HTTPOptions = sdk.HTTPOptions{BaseURL: cfg.BaseURL}
	}
	client, err := sdk.NewClient(context.Background(), clientCfg)
	if err != nil {
		return nil, fmt.Errorf("gemini: init client: %w", err)
	}
	return &Client{
		sdk:     client,
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
	contents, cfg := c.buildParams(req)
	resp, err := c.sdk.Models.GenerateContent(ctx, model, contents, cfg)
	if err != nil {
		return provider.Response{}, mapError(err)
	}
	return provider.Response{
		Content: resp.Text(),
		Model:   model,
		Usage:   mapUsage(resp.UsageMetadata),
	}, nil
}

func (c *Client) TestConnection(ctx context.Context) error {
	contents := []*sdk.Content{
		{Role: sdk.RoleUser, Parts: []*sdk.Part{sdk.NewPartFromText(testPingPrompt)}},
	}
	maxTok := int32(testPingMaxTok)
	if _, err := c.sdk.Models.GenerateContent(ctx, c.DefaultModel(), contents, &sdk.GenerateContentConfig{
		MaxOutputTokens: maxTok,
	}); err != nil {
		return mapError(err)
	}
	return nil
}

func (c *Client) buildParams(req provider.Request) ([]*sdk.Content, *sdk.GenerateContentConfig) {
	// The Gemini SDK takes int32 for max-output-tokens, but
	// provider.Request.MaxTokens is plain int. A negative or > math.MaxInt32
	// value would either underflow (negative → meaningless) or wrap around
	// (huge → silently negative). Defense in depth: bound to int32 explicitly
	// rather than letting the cast wrap.
	var maxTokens int32 = defaultMaxTokens
	if req.MaxTokens > 0 && req.MaxTokens <= math.MaxInt32 {
		maxTokens = int32(req.MaxTokens)
	}
	contents := []*sdk.Content{
		{Role: sdk.RoleUser, Parts: []*sdk.Part{sdk.NewPartFromText(req.UserPrompt)}},
	}
	cfg := &sdk.GenerateContentConfig{
		MaxOutputTokens: maxTokens,
	}
	// Structured-findings JSON contract (ADR-0014). Omitted for FreeForm
	// (ADR-0015) so the model returns a plain-text completion.
	if !req.FreeForm {
		cfg.ResponseMIMEType = "application/json"
		cfg.ResponseSchema = responseSchema()
	}
	if req.SystemPrompt != "" {
		cfg.SystemInstruction = &sdk.Content{
			Parts: []*sdk.Part{sdk.NewPartFromText(req.SystemPrompt)},
		}
	}
	return contents, cfg
}

func mapUsage(u *sdk.GenerateContentResponseUsageMetadata) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	return provider.Usage{
		InputTokens:       int(u.PromptTokenCount),
		OutputTokens:      int(u.CandidatesTokenCount),
		CachedInputTokens: int(u.CachedContentTokenCount),
	}
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	// google.golang.org/genai surfaces typed errors via googleapi.Error
	// (status code accessible), but to avoid pinning that import here we
	// classify by message substring as a baseline.
	switch {
	case statusCode(err) == http.StatusUnauthorized || statusCode(err) == http.StatusForbidden ||
		strings.Contains(msg, "API key not valid") || strings.Contains(msg, "PERMISSION_DENIED"):
		return fmt.Errorf("gemini: %w: %s", provider.ErrUnauthorized, msg)
	case statusCode(err) == http.StatusTooManyRequests || strings.Contains(msg, "RESOURCE_EXHAUSTED"):
		return fmt.Errorf("gemini: %w: %s", provider.ErrRateLimit, msg)
	case statusCode(err) == http.StatusNotFound || strings.Contains(msg, "models/") && strings.Contains(msg, "not found"):
		return fmt.Errorf("gemini: %w: %s", provider.ErrModelNotSupported, msg)
	}
	return fmt.Errorf("gemini: %w", err)
}

// statusCode pulls an HTTP status off any error type that exposes one.
// Returns 0 if the error doesn't carry status info.
func statusCode(err error) int {
	type statusCoder interface{ HTTPStatusCode() int }
	type statuser interface{ Status() int }
	var sc statusCoder
	if errors.As(err, &sc) {
		return sc.HTTPStatusCode()
	}
	var s statuser
	if errors.As(err, &s) {
		return s.Status()
	}
	return 0
}

func init() {
	provider.Register(Name, New)
}

var _ provider.Provider = (*Client)(nil)
