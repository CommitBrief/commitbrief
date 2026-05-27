// SPDX-License-Identifier: GPL-3.0-or-later

package provider

import "context"

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
