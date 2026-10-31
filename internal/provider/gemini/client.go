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
	"github.com/CommitBrief/commitbrief/internal/i18n"
	"github.com/CommitBrief/commitbrief/internal/lang"
	"github.com/CommitBrief/commitbrief/internal/provider"
	"github.com/CommitBrief/commitbrief/internal/tokens"
)

const (
	defaultMaxTokens = 4096
	// defaultThinkingMaxTokens is the output ceiling for the thinking models
	// in thinkingModels (models.go) when the caller specifies none. Gemini
	// bills thought tokens out of the same MaxOutputTokens budget as the
	// visible answer, so the historical 4096 is routinely exhausted by
	// thinking alone and the JSON comes back truncated or empty. Mirrors the
	// OpenAI reasoning default (16384). Not a cache-key input.
	defaultThinkingMaxTokens = 16384
	testPingPrompt           = "ping"
	testPingMaxTok           = 8
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
	contents, cfg := c.buildParams(req, model)
	resp, err := c.sdk.Models.GenerateContent(ctx, model, contents, cfg)
	if err != nil {
		return provider.Response{}, mapError(err)
	}
	content := resp.Text()
	usage := mapUsage(resp.UsageMetadata)
	if hitMaxTokens(resp) && strings.TrimSpace(content) == "" {
		// The whole output budget went to thinking and nothing visible was
		// produced. Re-asking at the same ceiling would just repeat that, so
		// retry the identical request once with the ceiling doubled (capped
		// at the model maximum). Both calls are billed, so their usage is
		// summed. A retry that is still empty falls through to the normal
		// handling below; the ADR-0031 repair retry in internal/cli only
		// raises the ceiling for truncated (non-empty) JSON, which is why
		// this empty case is handled here. Truncated non-empty content is
		// deliberately not retried: that is the ADR-0031 path.
		if next := retryCeiling(cfg.MaxOutputTokens); next > cfg.MaxOutputTokens {
			cfg.MaxOutputTokens = next
			resp2, err2 := c.sdk.Models.GenerateContent(ctx, model, contents, cfg)
			switch {
			case err2 == nil:
				resp = resp2
				content = resp2.Text()
				usage = addUsage(usage, mapUsage(resp2.UsageMetadata))
			case ctx.Err() != nil:
				// Cancellation or deadline is a real failure, not something
				// to degrade around. The first call's billed usage rides
				// along with the error.
				return provider.Response{Model: model, Usage: usage}, mapError(err2)
			default:
				// A transport/API failure (429, 5xx, timeout) on the extra
				// attempt must not turn into a hard failure that the first
				// call's result did not warrant: fall back to that result
				// (empty + MAX_TOKENS, first call's usage) so the existing
				// handling below applies, exactly as before the retry
				// existed.
			}
		}
	}
	if hitMaxTokens(resp) && req.FreeForm {
		// A plain-text completion (commit message, summary) cut off by the
		// output ceiling has no repair path, and handing the fragment back
		// as if it were whole would be silent corruption. Structured
		// reviews are different: the truncated JSON flows on to the
		// ADR-0031 repair retry with a raised ceiling, and empty content
		// (after the in-provider retry above also came back empty) takes
		// its schema-reset branch at the same ceiling (see repairRequest in
		// internal/cli/review.go).
		//
		// This error reaches the user, so it is localized like the
		// clireview argument-limit error (EN/TR, request language, English
		// fallback). The usage is returned with the error so the billed
		// thought tokens are not lost to a caller that wants them.
		return provider.Response{Model: model, Usage: usage}, newTruncatedError(req.Lang, usage.OutputTokens)
	}
	return provider.Response{
		Content: content,
		Model:   model,
		Usage:   usage,
	}, nil
}

// retryCeiling returns the output ceiling for the empty-content MAX_TOKENS
// retry: double the current one, capped at the model maximum. A result that
// is not above current means there is no headroom left and no retry is made.
func retryCeiling(current int32) int32 {
	if current > maxOutputTokensCeiling/2 {
		return maxOutputTokensCeiling
	}
	return current * 2
}

func addUsage(a, b provider.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:       a.InputTokens + b.InputTokens,
		OutputTokens:      a.OutputTokens + b.OutputTokens,
		CachedInputTokens: a.CachedInputTokens + b.CachedInputTokens,
	}
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

func (c *Client) buildParams(req provider.Request, model string) ([]*sdk.Content, *sdk.GenerateContentConfig) {
	// The Gemini SDK takes int32 for max-output-tokens, but
	// provider.Request.MaxTokens is plain int. A negative or > math.MaxInt32
	// value would either underflow (negative → meaningless) or wrap around
	// (huge → silently negative). Defense in depth: bound to int32 explicitly
	// rather than letting the cast wrap.
	maxTokens := defaultMaxTokensFor(model)
	if req.MaxTokens > 0 && req.MaxTokens <= math.MaxInt32 {
		maxTokens = int32(req.MaxTokens)
	}
	// Never ask for more than the model can emit. The CLI's repair ceiling
	// doubles the reported output tokens, which now include thought tokens
	// and (after the empty-content retry) two summed calls, so it can exceed
	// the model maximum.
	if maxTokens > maxOutputTokensCeiling {
		maxTokens = maxOutputTokensCeiling
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

// newTruncatedError builds the localized MAX_TOKENS error for a free-form
// completion. The catalog follows the request language; an unknown language
// or a load failure falls back to English.
func newTruncatedError(reqLang string, outputTokens int) error {
	cat, err := i18n.Load(lang.UICatalogFor(reqLang))
	if err != nil {
		return fmt.Errorf("%s: response truncated at the output token limit (MAX_TOKENS) after %d output tokens", Name, outputTokens)
	}
	return errors.New(cat.T("provider.gemini.output_truncated", Name, outputTokens))
}

// defaultMaxTokensFor returns the output-token ceiling to use when the
// caller did not specify one (req.MaxTokens > 0 always wins in buildParams).
func defaultMaxTokensFor(model string) int32 {
	if thinkingModels[model] {
		return defaultThinkingMaxTokens
	}
	return defaultMaxTokens
}

// hitMaxTokens reports whether the first candidate stopped because the output
// token ceiling ran out. With a thinking model that can happen with little or
// no visible text, since thought tokens share the budget.
func hitMaxTokens(resp *sdk.GenerateContentResponse) bool {
	return resp != nil && len(resp.Candidates) > 0 &&
		resp.Candidates[0] != nil && resp.Candidates[0].FinishReason == sdk.FinishReasonMaxTokens
}

func mapUsage(u *sdk.GenerateContentResponseUsageMetadata) provider.Usage {
	if u == nil {
		return provider.Usage{}
	}
	// Gemini reports thought tokens separately from CandidatesTokenCount but
	// bills them at the output rate, so they belong in OutputTokens: the
	// verbose footer, the cost report and eval usd_per_review then match the
	// bill (the pre-call cost preflight only estimates and never sees this).
	return provider.Usage{
		InputTokens:       int(u.PromptTokenCount),
		OutputTokens:      int(u.CandidatesTokenCount) + int(u.ThoughtsTokenCount),
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
	provider.RegisterMetadata(Metadata())
}

var _ provider.Provider = (*Client)(nil)
