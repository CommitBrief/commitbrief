// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import (
	"context"
	"time"
)

type Provider interface {
	Name() string

	DefaultModel() string

	ContextWindow(model string) int

	EstimateTokens(text string) int

	Pricing(model string) Pricing

	Review(ctx context.Context, req Request) (Response, error)

	TestConnection(ctx context.Context) error
}

// PlainTextEmitter is the marker interface for providers whose
// Review() returns formatted plain text instead of the structured
// findings JSON the API providers contract. The review pipeline
// uses this to short-circuit JSON parsing, retry-once, and the
// cards/markdown renderer — the response is emitted to stdout
// verbatim because the CLI tool has already formatted it.
//
// CLI-based providers (claude-cli, gemini-cli, codex-cli) implement
// this so they can pass through their host CLI's output without the
// JSON-contract enforcement that API providers do via native
// structured-output mechanisms (tool_use / response_format /
// response_schema).
type PlainTextEmitter interface {
	Provider
	EmitsPlainText()
}

// TimeoutSetter is the optional interface for providers that enforce a
// hard timeout of their OWN in addition to honouring the caller's
// context — clireview's per-invocation cap, ollama's http.Client
// timeout, the Anthropic SDK's 10-minute non-streaming ceiling.
//
// It exists because a context deadline can only SHORTEN a run. A user
// who passes `--timeout 20m` on a slow review would still be cut off at
// the provider's built-in five or ten minutes, so the value has to reach
// the provider itself. Implement it only when there is such a cap to
// raise; providers that purely follow ctx (openai and its
// OpenAI-compatible siblings, gemini, mock) deliberately do not.
//
// SetTimeout is called at most once, right after construction and before
// any Review/TestConnection call, so implementations may simply assign
// to a field without synchronization.
type TimeoutSetter interface {
	Provider
	SetTimeout(d time.Duration)
}
